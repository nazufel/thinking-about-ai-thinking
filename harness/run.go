package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Effort levels the harness accepts, lowest first.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Outcomes of a run.
const (
	outcomeCompleted      = "completed"
	outcomeDidNotComplete = "did-not-complete"
	outcomeVoid           = "void"
)

// RunOptions describes one run: one task at one effort level.
type RunOptions struct {
	Plan      string
	Task      string
	Effort    string
	Trial     int
	Model     string
	Tools     string
	BudgetUSD float64
	Timeout   time.Duration
	DryRun    bool
	Keep      bool
}

// Record is everything the harness keeps about one run. It holds no paths
// from the machine it ran on, so it is safe to commit.
type Record struct {
	ID   string `json:"id"`
	Plan string `json:"plan"`
	// Note holds anything a reader must know before comparing this record
	// with others.
	Note        string    `json:"note,omitempty"`
	Task        string    `json:"task"`
	Effort      string    `json:"effort"`
	Trial       int       `json:"trial"`
	Model       string    `json:"model"`
	Tools       string    `json:"tools"`
	Started     time.Time `json:"started"`
	CLIVersion  string    `json:"cli_version"`
	FixtureHash string    `json:"fixture_hash"`
	PromptHash  string    `json:"prompt_hash"`

	// Outcome is completed, did-not-complete or void. A run that did not
	// complete is never counted as a low score; Cause says why it stopped.
	Outcome string `json:"outcome"`
	Cause   string `json:"cause,omitempty"`
	// DeniedCalls lists the tool calls the permission system refused. A
	// run with denied calls still counts: the model can work around a
	// refusal, and the scorecard shows how many runs had one.
	DeniedCalls []string `json:"denied_calls,omitempty"`

	// Minutes is wall-clock time. CostUSD is Claude Code's own estimate at
	// list prices, not what a subscription seat is billed.
	Minutes           float64        `json:"minutes"`
	CostUSD           float64        `json:"cost_usd"`
	Turns             int            `json:"turns"`
	OutputTokens      int64          `json:"output_tokens"`
	ToolCalls         int            `json:"tool_calls"`
	ToolCallsByName   map[string]int `json:"tool_calls_by_name,omitempty"`
	RanTests          bool           `json:"ran_tests"`
	ThinkingBlocks    int            `json:"thinking_blocks"`
	PermissionDenials int            `json:"permission_denials"`
	StopReason        string         `json:"stop_reason,omitempty"`

	Score       *Score `json:"score"`
	FinalReport string `json:"final_report"`
}

// claudeArgs builds the command line for one run. Every run of a task gets
// the same arguments except --effort.
func claudeArgs(prompt string, o RunOptions) []string {
	return []string{
		"-p", prompt,
		"--model", o.Model,
		"--effort", o.Effort,
		// No personal CLAUDE.md, skills, plugins, hooks or MCP servers.
		// Sign-in and admin-managed settings still apply.
		"--safe-mode",
		// File tools and a shell only: no subagents and no web.
		"--tools", o.Tools,
		"--output-format", "stream-json",
		"--verbose",
		"--no-session-persistence",
		"--max-budget-usd", strconv.FormatFloat(o.BudgetUSD, 'f', 2, 64),
	}
}

// checkOptions rejects runs that could not be compared with the others.
func checkOptions(o RunOptions) error {
	if o.Task != "bughunt" && o.Task != "rename" {
		return fmt.Errorf("unknown task %q: use bughunt or rename", o.Task)
	}
	known := false
	for _, e := range efforts {
		known = known || e == o.Effort
	}
	if !known {
		return fmt.Errorf("unknown effort %q: use one of %s", o.Effort, strings.Join(efforts, ", "))
	}
	if v := os.Getenv("CLAUDE_CODE_EFFORT_LEVEL"); v != "" {
		return fmt.Errorf("CLAUDE_CODE_EFFORT_LEVEL=%s is set and would override --effort on every run: unset it first", v)
	}
	return nil
}

// runOne runs a task once at one effort level in a clean copy of the
// fixture, scores the result and writes the record.
func runOne(p Paths, o RunOptions) (*Record, error) {
	if err := checkOptions(o); err != nil {
		return nil, err
	}
	promptPath := filepath.Join(p.Tasks, o.Task+".md")
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(string(promptBytes))

	// A fresh directory outside the repository, so the model cannot see the
	// scoring material and nothing carries over between runs.
	workRoot, err := os.MkdirTemp("", "effort-demo-*")
	if err != nil {
		return nil, err
	}
	if !o.Keep {
		defer os.RemoveAll(workRoot)
	}
	runDir := filepath.Join(workRoot, "gatekeeper")
	if err := copyTree(p.App, runDir); err != nil {
		return nil, err
	}

	args := claudeArgs(prompt, o)
	if o.DryRun {
		fmt.Printf("would run, in a fresh copy of app/:\n  claude %s\n", quoteArgs(args))
		return nil, nil
	}

	// clock keeps its monotonic reading, which stands still while the
	// machine sleeps. started is the same instant on the wall clock.
	clock := time.Now()
	started := clock.UTC()
	id := fmt.Sprintf("%s-%s-%s-%02d-%s", o.Plan, o.Task, o.Effort, o.Trial, started.Format("20060102T150405Z"))
	for _, dir := range []string{p.Runs, p.Raw} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	rawPath := filepath.Join(p.Raw, id+".jsonl")
	rawFile, err := os.Create(rawPath)
	if err != nil {
		return nil, err
	}
	errPath := filepath.Join(p.Raw, id+".stderr")
	errFile, err := os.Create(errPath)
	if err != nil {
		rawFile.Close()
		return nil, err
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = runDir
	cmd.Env = claudeEnv()
	cmd.Stdout = rawFile
	cmd.Stderr = errFile
	// Its own process group, so a timeout stops everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		rawFile.Close()
		errFile.Close()
		return nil, fmt.Errorf("start claude: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	timedOut, exitCode := false, 0
	ticker := time.NewTicker(time.Minute)
	deadline := time.After(o.Timeout)
wait:
	for {
		select {
		case sig := <-interrupt:
			// Stop the run in flight too. It is in its own process group,
			// so it would otherwise keep working, and spending, after the
			// harness has gone.
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
			ticker.Stop()
			rawFile.Close()
			errFile.Close()
			os.Remove(rawPath)
			os.Remove(errPath)
			return nil, fmt.Errorf("stopped by %s: the run in flight was stopped and left no record", sig)
		case err := <-done:
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				exitCode = exit.ExitCode()
			} else if err != nil {
				exitCode = -1
			}
			break wait
		case <-ticker.C:
			fmt.Printf("    ... %s elapsed\n", time.Since(clock).Round(time.Minute))
		case <-deadline:
			timedOut = true
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
			break wait
		}
	}
	ticker.Stop()
	awake := time.Since(clock)
	minutes := awake.Minutes()
	rawFile.Close()
	errFile.Close()

	// A run the machine slept through is not a measurement of anything: its
	// connection dropped and its timing is wrong. Throw it away.
	if gap := sleepGap(time.Now().UTC().Sub(started), awake); gap > 0 {
		os.Remove(rawPath)
		os.Remove(errPath)
		return nil, &sleptError{gap}
	}

	raw, err := os.Open(rawPath)
	if err != nil {
		return nil, err
	}
	transcript, err := parseTranscript(raw)
	raw.Close()
	if err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}

	score, err := scoreTree(p, o.Task, runDir)
	if err != nil {
		return nil, fmt.Errorf("score the run: %w", err)
	}
	if err := saveDiff(p, runDir, filepath.Join(p.Raw, id+".diff")); err != nil {
		return nil, err
	}

	rec := &Record{
		ID:                id,
		Plan:              o.Plan,
		Task:              o.Task,
		Effort:            o.Effort,
		Trial:             o.Trial,
		Model:             o.Model,
		Tools:             o.Tools,
		Started:           started,
		CLIVersion:        cliVersion(),
		Minutes:           round(minutes, 2),
		CostUSD:           transcript.CostUSD,
		Turns:             transcript.Turns,
		OutputTokens:      transcript.OutputTokens,
		ToolCalls:         transcript.ToolCalls,
		ToolCallsByName:   transcript.ToolCallsByName,
		RanTests:          transcript.RanTests,
		ThinkingBlocks:    transcript.ThinkingBlocks,
		PermissionDenials: transcript.PermissionDenials,
		StopReason:        transcript.StopReason,
		Score:             score,
		FinalReport:       scrub(transcript.FinalText, workRoot),
	}
	for _, call := range transcript.DeniedCalls {
		rec.DeniedCalls = append(rec.DeniedCalls, scrub(call, workRoot))
	}
	rec.FixtureHash, _ = treeHash(p.App)
	rec.PromptHash, _ = fileHash(promptPath)
	rec.Outcome, rec.Cause = classify(transcript, score, timedOut, exitCode)
	if err := saveRecord(p, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// sleepTolerance is how far the wall clock may run ahead of the awake time
// before the harness concludes that the machine slept.
const sleepTolerance = 30 * time.Second

// sleepGap returns how long the machine was asleep during a span, given the
// wall-clock time that passed and the time it was awake. It returns zero for
// a difference small enough to be clock adjustment.
func sleepGap(wall, awake time.Duration) time.Duration {
	if gap := wall - awake; gap > sleepTolerance {
		return gap
	}
	return 0
}

// sleptError reports that the machine slept during a run. The run leaves no
// record and can simply be repeated.
type sleptError struct{ slept time.Duration }

func (e *sleptError) Error() string {
	return fmt.Sprintf("the machine slept for %s during the run, so the run was discarded", e.slept.Round(time.Second))
}

// keepAwake stops the machine from going to sleep while the harness runs
// and returns a function that lets it sleep again. On macOS it uses
// caffeinate, which cannot stop the sleep that closing the lid causes.
// Elsewhere it does nothing, and a run the machine sleeps through is still
// caught and discarded.
func keepAwake() func() {
	if runtime.GOOS != "darwin" {
		return func() {}
	}
	path, err := exec.LookPath("caffeinate")
	if err != nil {
		return func() {}
	}
	// -i: no idle sleep. -s: no system sleep on mains power. -w: stop when
	// the harness exits, however it exits.
	cmd := exec.Command(path, "-i", "-s", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// classify decides whether a run counts. Anything that stopped the run
// short is reported as did-not-complete with its cause, never as a low score.
//
// A refused tool call does not stop a run: the model is told and carries on.
// Such a run counts, and the record lists what was refused.
func classify(t *Transcript, s *Score, timedOut bool, exitCode int) (outcome, cause string) {
	switch {
	case t.CanarySeen:
		return outcomeVoid, "the hidden scoring material appeared in the transcript"
	case timedOut:
		return outcomeDidNotComplete, "timed out"
	case !t.ResultSeen:
		return outcomeDidNotComplete, fmt.Sprintf("no result from Claude Code (exit code %d)", exitCode)
	case t.StopReason == "refusal":
		return outcomeDidNotComplete, "the model declined the request"
	case t.IsError || (t.Subtype != "" && t.Subtype != "success"):
		return outcomeDidNotComplete, "ended with " + t.Subtype
	case s.FilesChanged == 0:
		return outcomeDidNotComplete, "made no changes"
	}
	return outcomeCompleted, ""
}

// scrub removes this machine's paths from text that will be committed: the
// run's own directory, the system temp folder, and the home directory.
func scrub(text, workRoot string) string {
	text = replacePath(text, workRoot, "<run>")
	text = replacePath(text, filepath.Clean(os.TempDir()), "<tmp>")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		text = strings.ReplaceAll(text, home, "~")
	}
	return text
}

// replacePath replaces a path, and the path it resolves to through
// symlinks, with a placeholder. On macOS /var/... resolves to
// /private/var/..., and a transcript may use either form.
func replacePath(text, path, placeholder string) string {
	forms := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		forms = append(forms, resolved)
	}
	// Longest first, so /private/var/... is replaced before /var/....
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	for _, form := range forms {
		text = strings.ReplaceAll(text, form, placeholder)
	}
	return text
}

func cliVersion() string {
	res, err := runCmd("", os.Environ(), 30*time.Second, "claude", "--version")
	if err != nil {
		return "unknown"
	}
	return firstLine(res.Stdout)
}

func saveRecord(p Paths, rec *Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.Runs, rec.ID+".json"), append(data, '\n'), 0o644)
}

// loadRecords reads every run record, oldest first.
func loadRecords(p Paths) ([]*Record, error) {
	entries, err := os.ReadDir(p.Runs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []*Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Runs, e.Name()))
		if err != nil {
			return nil, err
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		recs = append(recs, &rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Started.Before(recs[j].Started) })
	return recs, nil
}

// quoteArgs renders a command line for display.
func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'`$;&|<>()") {
			if len(a) > 70 {
				a = a[:67] + "..."
			}
			a = strconv.Quote(a)
		}
		quoted[i] = a
	}
	return strings.Join(quoted, " ")
}

func round(f float64, places int) float64 {
	scale := 1.0
	for i := 0; i < places; i++ {
		scale *= 10
	}
	if f < 0 {
		return float64(int64(f*scale-0.5)) / scale
	}
	return float64(int64(f*scale+0.5)) / scale
}
