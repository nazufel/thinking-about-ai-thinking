package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A transcript in the shape Claude Code writes with
// --output-format stream-json: one JSON event per line.
const sampleTranscript = `{"type":"system","subtype":"init","model":"claude-opus-5-5","tools":["Bash","Edit","Read","Write"]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"text","text":"Reading the code."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"internal/logs/logs.go"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"package logs"}]}}
this line is not JSON and must be skipped
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"internal/logs/logs.go"}},{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"cd . && go test ./..."}}]}}
{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":61000,"duration_api_ms":50000,"num_turns":4,"result":"Fixed 2 issues.","total_cost_usd":0.42,"usage":{"input_tokens":1200,"output_tokens":3400},"permission_denials":[]}
`

func TestParseTranscript(t *testing.T) {
	tr, err := parseTranscript(strings.NewReader(sampleTranscript))
	if err != nil {
		t.Fatal(err)
	}
	if !tr.ResultSeen || tr.Subtype != "success" || tr.IsError {
		t.Errorf("result event not read: %+v", tr)
	}
	if tr.FinalText != "Fixed 2 issues." || tr.Turns != 4 || tr.CostUSD != 0.42 || tr.OutputTokens != 3400 {
		t.Errorf("result fields = %q, %d turns, $%v, %d tokens", tr.FinalText, tr.Turns, tr.CostUSD, tr.OutputTokens)
	}
	if tr.ToolCalls != 3 || tr.ToolCallsByName["Read"] != 1 || tr.ToolCallsByName["Edit"] != 1 || tr.ToolCallsByName["Bash"] != 1 {
		t.Errorf("tool calls = %d %v, want 3 with one each of Read, Edit and Bash", tr.ToolCalls, tr.ToolCallsByName)
	}
	if !tr.RanTests {
		t.Error("the go test command was not noticed")
	}
	if tr.ThinkingBlocks != 1 || tr.StopReason != "end_turn" || tr.Unparsed != 1 || tr.PermissionDenials != 0 {
		t.Errorf("thinking=%d stop=%q unparsed=%d denials=%d", tr.ThinkingBlocks, tr.StopReason, tr.Unparsed, tr.PermissionDenials)
	}
	if tr.CanarySeen {
		t.Error("the canary was reported in a transcript that does not contain it")
	}
}

func TestParseTranscriptSpotsCanaryAndDenials(t *testing.T) {
	stream := `{"type":"user","message":{"content":[{"type":"tool_result","content":"// Canary: ` + canary + `"}]}}
{"type":"result","subtype":"success","is_error":false,"permission_denials":[{"tool_name":"Bash"},{"tool_name":"Bash"}]}
`
	tr, err := parseTranscript(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if !tr.CanarySeen {
		t.Error("the canary in a tool result was missed")
	}
	if tr.PermissionDenials != 2 {
		t.Errorf("PermissionDenials = %d, want 2", tr.PermissionDenials)
	}
}

func TestDeniedCallsAreDescribed(t *testing.T) {
	stream := `{"type":"result","subtype":"success","permission_denials":[` +
		`{"tool_name":"Bash","tool_input":{"command":"./verify.sh","description":"Build binary and probe the server"}},` +
		`{"tool_name":"Bash","tool_input":{"command":"curl   -s\n  http://127.0.0.1:8080/healthz"}},` +
		`{"tool_name":"Write"}]}`
	tr, err := parseTranscript(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Bash: Build binary and probe the server",
		"Bash: curl -s http://127.0.0.1:8080/healthz",
		"Write",
	}
	if len(tr.DeniedCalls) != len(want) {
		t.Fatalf("DeniedCalls = %q, want %q", tr.DeniedCalls, want)
	}
	for i := range want {
		if tr.DeniedCalls[i] != want[i] {
			t.Errorf("DeniedCalls[%d] = %q, want %q", i, tr.DeniedCalls[i], want[i])
		}
	}
}

func TestParseTranscriptWithoutTrailingNewline(t *testing.T) {
	tr, err := parseTranscript(strings.NewReader(`{"type":"result","subtype":"success","result":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !tr.ResultSeen || tr.FinalText != "ok" {
		t.Errorf("the last line was dropped: %+v", tr)
	}
}

func TestClassify(t *testing.T) {
	changed := &Score{FilesChanged: 3}
	ok := &Transcript{ResultSeen: true, Subtype: "success"}

	tests := []struct {
		name     string
		t        *Transcript
		s        *Score
		timedOut bool
		want     string
	}{
		{"a normal run", ok, changed, false, outcomeCompleted},
		{"timed out", ok, changed, true, outcomeDidNotComplete},
		{"no result event", &Transcript{}, changed, false, outcomeDidNotComplete},
		{"ended in an error", &Transcript{ResultSeen: true, Subtype: "error_max_budget_usd", IsError: true}, changed, false, outcomeDidNotComplete},
		{"refused", &Transcript{ResultSeen: true, Subtype: "success", StopReason: "refusal"}, changed, false, outcomeDidNotComplete},
		// A refused tool call does not stop a run, so the run still counts.
		{"a tool call was denied", &Transcript{ResultSeen: true, Subtype: "success", PermissionDenials: 1}, changed, false, outcomeCompleted},
		{"changed nothing", ok, &Score{}, false, outcomeDidNotComplete},
		{"saw the hidden tests", &Transcript{ResultSeen: true, Subtype: "success", CanarySeen: true}, changed, false, outcomeVoid},
	}
	for _, tt := range tests {
		got, cause := classify(tt.t, tt.s, tt.timedOut, 0)
		if got != tt.want {
			t.Errorf("%s: outcome = %s (%s), want %s", tt.name, got, cause, tt.want)
		}
		if got != outcomeCompleted && cause == "" {
			t.Errorf("%s: no cause given", tt.name)
		}
	}
}

func huntRun(effort string, trial, fixed int, minutes float64) *Record {
	s := &Score{BuildOK: true, OwnTestsPass: true, Total: len(vulns), FilesChanged: 4, LinesAdded: 40 + 10*fixed, LinesRemoved: 5}
	for i, v := range vulns {
		if i < fixed {
			s.Fixed = append(s.Fixed, v.ID)
		} else {
			s.NotFixed = append(s.NotFixed, v.ID)
		}
	}
	return &Record{
		ID: "full-bughunt-" + effort + "-" + string(rune('0'+trial)), Plan: "full", Task: "bughunt", Effort: effort, Trial: trial,
		Model: "claude-opus-5-5", Outcome: outcomeCompleted, Minutes: minutes, CostUSD: minutes / 10,
		ToolCalls: 10 + 5*fixed, RanTests: fixed > 4, Score: s, FinalReport: "Fixed things.",
		Started: time.Date(2026, 11, 20, 12, trial, 0, 0, time.UTC),
	}
}

func TestMedianRunIsFixedByRule(t *testing.T) {
	runs := []*Record{
		huntRun("low", 1, 6, 2.0),
		huntRun("low", 2, 3, 1.0),
		huntRun("low", 3, 4, 3.0),
		huntRun("low", 4, 4, 1.5),
		huntRun("low", 5, 5, 2.5),
	}
	// Ordered by score, then minutes: 3, 4 (1.5 min), 4 (3.0 min), 5, 6.
	if got := medianRun(runs); got.Trial != 3 {
		t.Errorf("medianRun picked trial %d, want trial 3 (the slower of the two runs that fixed 4)", got.Trial)
	}
	// With an even count it is the lower middle.
	if got := medianRun(runs[:4]); got.Trial != 4 {
		t.Errorf("medianRun of four picked trial %d, want trial 4", got.Trial)
	}
	if medianRun(nil) != nil {
		t.Error("medianRun of no runs returned a run")
	}
}

func TestScorecard(t *testing.T) {
	recs := []*Record{
		huntRun("low", 1, 3, 1.0), huntRun("low", 2, 4, 1.2), huntRun("low", 3, 5, 1.1),
		huntRun("max", 1, 8, 12.0), huntRun("max", 2, 9, 15.0), huntRun("max", 3, 9, 11.0),
	}
	stopped := huntRun("max", 4, 0, 45.0)
	stopped.Outcome, stopped.Cause = outcomeDidNotComplete, "timed out"
	recs = append(recs, stopped)

	rename := huntRun("low", 1, 0, 0.5)
	rename.Task = "rename"
	rename.Score = &Score{BuildOK: true, OwnTestsPass: true, RenameCorrect: true, FilesChanged: 6, LinesAdded: 20, LinesRemoved: 20}
	recs = append(recs, rename)

	pilot := huntRun("low", 1, 9, 1.0)
	pilot.Plan = "pilot"
	recs = append(recs, pilot)

	var out bytes.Buffer
	scorecard(&out, recs, "full", nil)
	text := out.String()
	if strings.Contains(text, "SECOND MEASURE") {
		t.Errorf("the second measure was printed without any grades:\n%s", text)
	}
	for _, want := range []string{
		"BUG HUNT", "3 4 5", "8 9 9", "3-5", "8-9",
		"S1   surface", "X3   cross-file",
		"RENAME (control)", "1/1",
		"NOT COUNTED ABOVE: 1 run", "timed out",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scorecard does not contain %q:\n%s", want, text)
		}
	}
	// The pilot run and the run that did not complete must not be scored.
	if strings.Contains(text, "0 8 9 9") || strings.Contains(text, "3 4 5 9") {
		t.Errorf("a run that should be excluded was scored:\n%s", text)
	}

	out.Reset()
	scorecard(&out, recs, "nothing", nil)
	if !strings.Contains(out.String(), "no runs recorded") {
		t.Errorf("an empty plan printed: %s", out.String())
	}

	// With hand grades, the second measure is printed for the graded runs.
	ex := &ExtrasFile{Runs: map[string]map[string]string{
		recs[0].ID: {},
		recs[1].ID: {"U1": "confines the name"},
		recs[3].ID: {"U1": "confines the name", "U2": "generation counter", "U3": "size cap"},
		recs[4].ID: {"U2": "generation counter", "U3": "size cap", "U5": "config check", "U7": "per-tenant buffers"},
	}}
	out.Reset()
	scorecard(&out, recs, "full", ex)
	text = out.String()
	for _, want := range []string{
		"SECOND MEASURE", "of 10",
		"low     2     0 1", "max     2     3 4",
		"U2   0/2     2/2", "U7   0/2     1/2",
		"2 completed run(s) have no grade yet",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scorecard with grades does not contain %q:\n%s", want, text)
		}
	}
}

func TestBlindAndUnblindRoundTrip(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root, Runs: root + "/results/runs", Raw: root + "/results/raw"}
	for _, dir := range []string{p.Runs, p.Raw, root + "/scoring"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(root+"/scoring/EXTRAS.md", []byte("the list"), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := []*Record{huntRun("low", 1, 9, 1.0), huntRun("max", 1, 9, 14.0)}
	for _, r := range runs {
		if err := saveRecord(p, r); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Raw+"/"+r.ID+".diff", []byte("diff for "+r.ID), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := blind(p, "full"); err != nil {
		t.Fatal(err)
	}
	var key map[string]string
	if err := readJSON(blindDir(p)+"/key.json", &key); err != nil {
		t.Fatal(err)
	}
	if len(key) != 2 {
		t.Fatalf("the key has %d entries, want 2", len(key))
	}
	grades := map[string]map[string]string{}
	for code, id := range key {
		if strings.Contains(code, "low") || strings.Contains(code, "max") {
			t.Errorf("the blind name %q gives the effort level away", code)
		}
		data, err := os.ReadFile(blindDir(p) + "/" + code + ".diff")
		if err != nil || string(data) != "diff for "+id {
			t.Errorf("blind copy %s = %q, %v", code, data, err)
		}
		grades[code] = map[string]string{}
		if strings.Contains(id, "max") {
			grades[code]["U2"] = "generation counter in cache.go"
		}
	}

	// Unblinding is refused until every diff has a grade.
	if err := unblind(p, "test"); err == nil {
		t.Error("unblind worked without grades.json")
	}
	raw, _ := json.Marshal(grades)
	if err := os.WriteFile(blindDir(p)+"/grades.json", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unblind(p, "graded in a test"); err != nil {
		t.Fatal(err)
	}

	ex, err := loadExtras(p)
	if err != nil || ex == nil {
		t.Fatalf("loadExtras = %v, %v", ex, err)
	}
	if n, ok := ex.extrasCount(runs[0].ID); !ok || n != 0 {
		t.Errorf("the low run has %d credits (graded %v), want 0", n, ok)
	}
	if n, ok := ex.extrasCount(runs[1].ID); !ok || n != 1 {
		t.Errorf("the max run has %d credits (graded %v), want 1", n, ok)
	}
	if ex.ListHash == "" || ex.Method != "graded in a test" {
		t.Errorf("list hash %q, method %q", ex.ListHash, ex.Method)
	}

	// A credit for an item that is not on the list is refused.
	bad := map[string]map[string]string{}
	for code := range key {
		bad[code] = map[string]string{"U99": "not a real item"}
	}
	raw, _ = json.Marshal(bad)
	os.WriteFile(blindDir(p)+"/grades.json", raw, 0o644)
	if err := unblind(p, "test"); err == nil {
		t.Error("a credit for an unknown item was accepted")
	}
}

func TestDeniedRunsAreCountedAndShown(t *testing.T) {
	runs := []*Record{huntRun("max", 1, 9, 14.0), huntRun("max", 2, 9, 15.0), huntRun("max", 3, 9, 16.0)}
	runs[1].PermissionDenials = 2
	if got := deniedRuns(runs); got != "1/3" {
		t.Errorf("deniedRuns = %q, want 1/3", got)
	}
	if line := oneLine(runs[1]); !strings.Contains(line, "(2 tool call(s) denied)") || strings.Contains(line, outcomeDidNotComplete) {
		t.Errorf("oneLine = %q, want the denial noted and the run still counted", line)
	}

	var out bytes.Buffer
	scorecard(&out, runs, "full", nil)
	text := out.String()
	if !strings.Contains(text, "denied") || !strings.Contains(text, "9 9 9") {
		t.Errorf("the scorecard should score all three runs and have a denied column:\n%s", text)
	}
	if strings.Contains(text, "NOT COUNTED ABOVE") {
		t.Errorf("a run with a denied call was left out:\n%s", text)
	}
}

func TestShowAndFindRecord(t *testing.T) {
	recs := []*Record{huntRun("low", 1, 3, 1.0), huntRun("max", 1, 8, 12.0)}
	rec, err := findRecord(recs, "max")
	if err != nil || rec.Effort != "max" {
		t.Fatalf("findRecord(max) = %v, %v", rec, err)
	}
	if _, err := findRecord(recs, "bughunt"); err == nil {
		t.Error("an ambiguous ID matched a single run")
	}
	if _, err := findRecord(recs, "nope"); err == nil {
		t.Error("an unknown ID matched a run")
	}

	var out bytes.Buffer
	show(&out, rec)
	if text := out.String(); !strings.Contains(text, "effort max") || !strings.Contains(text, "fixed 8 of 9") || !strings.Contains(text, "Fixed things.") {
		t.Errorf("show printed:\n%s", text)
	}
}

func TestPlans(t *testing.T) {
	pilot, err := planCells("pilot")
	if err != nil || len(pilot) != 4 {
		t.Errorf("the pilot plan has %d runs (%v), want 4", len(pilot), err)
	}
	full, err := planCells("full")
	if err != nil || len(full) != 32 {
		t.Errorf("the full plan has %d runs (%v), want 32", len(full), err)
	}
	if _, err := planCells("other"); err == nil {
		t.Error("an unknown plan was accepted")
	}
}

func TestCheckOptions(t *testing.T) {
	good := RunOptions{Task: "bughunt", Effort: "low"}
	t.Setenv("CLAUDE_CODE_EFFORT_LEVEL", "")
	if err := checkOptions(good); err != nil {
		t.Errorf("valid options were rejected: %v", err)
	}
	if err := checkOptions(RunOptions{Task: "bughunt", Effort: "extreme"}); err == nil {
		t.Error("an unknown effort level was accepted")
	}
	if err := checkOptions(RunOptions{Task: "refactor", Effort: "low"}); err == nil {
		t.Error("an unknown task was accepted")
	}
	t.Setenv("CLAUDE_CODE_EFFORT_LEVEL", "high")
	if err := checkOptions(good); err == nil {
		t.Error("a run was allowed while CLAUDE_CODE_EFFORT_LEVEL overrides --effort")
	}
}

func TestClaudeArgsDifferOnlyInEffort(t *testing.T) {
	base := RunOptions{Task: "bughunt", Model: "claude-opus-5-5", Tools: "Bash,Edit,Read,Write", BudgetUSD: 10}
	low, max := base, base
	low.Effort, max.Effort = "low", "max"
	a, b := claudeArgs("prompt", low), claudeArgs("prompt", max)
	if len(a) != len(b) {
		t.Fatalf("argument counts differ: %d and %d", len(a), len(b))
	}
	differences := 0
	for i := range a {
		if a[i] != b[i] {
			differences++
		}
	}
	if differences != 1 {
		t.Errorf("%d arguments differ between effort levels, want exactly 1", differences)
	}
	joined := strings.Join(a, " ")
	for _, want := range []string{"--safe-mode", "--tools Bash,Edit,Read,Write", "--output-format stream-json", "--max-budget-usd 10.00"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the command line lacks %q: %s", want, joined)
		}
	}
}

func TestClaudeEnvDropsSessionVariables(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc")
	t.Setenv("CLAUDE_EFFORT", "max")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")

	if !insideSession() {
		t.Error("insideSession did not notice the session variables")
	}
	env := strings.Join(claudeEnv(), "\n") + "\n"
	for _, gone := range []string{"CLAUDECODE=", "CLAUDE_CODE_CHILD_SESSION=", "CLAUDE_CODE_SESSION_ID=", "CLAUDE_EFFORT="} {
		if strings.Contains(env, gone) {
			t.Errorf("claudeEnv kept the session variable %s", gone)
		}
	}
	// The user's own setup and the Go settings must survive.
	for _, kept := range []string{"CLAUDE_CODE_USE_VERTEX=1\n", "GOPROXY=off\n", "GOTOOLCHAIN=local\n"} {
		if !strings.Contains(env, kept) {
			t.Errorf("claudeEnv dropped %q", strings.TrimSpace(kept))
		}
	}
}

func TestSleepGap(t *testing.T) {
	// The run that prompted this: five hours on the wall clock, two awake.
	if got := sleepGap(302*time.Minute, 2*time.Minute); got != 300*time.Minute {
		t.Errorf("sleepGap = %s, want 5h0m0s", got)
	}
	// A second or two of clock adjustment is not sleep.
	if got := sleepGap(61*time.Second, 60*time.Second); got != 0 {
		t.Errorf("sleepGap for a one-second difference = %s, want 0", got)
	}
	if got := sleepGap(10*time.Minute, 10*time.Minute); got != 0 {
		t.Errorf("sleepGap with no difference = %s, want 0", got)
	}
	err := error(&sleptError{5 * time.Hour})
	if !strings.Contains(err.Error(), "slept for 5h0m0s") {
		t.Errorf("sleptError = %q", err)
	}
}

func TestScrubRemovesLocalPaths(t *testing.T) {
	got := scrub("edited /tmp/effort-demo-123/gatekeeper/internal/logs/logs.go", "/tmp/effort-demo-123")
	if got != "edited <run>/gatekeeper/internal/logs/logs.go" {
		t.Errorf("scrub = %q", got)
	}

	// A path elsewhere in the system temp folder is scrubbed too.
	t.Setenv("TMPDIR", "/scratch/user-abc123/T/")
	got = scrub("a copy is in /scratch/user-abc123/T/tmp.XYZ/orig", "/scratch/user-abc123/T/effort-demo-9")
	if got != "a copy is in <tmp>/tmp.XYZ/orig" {
		t.Errorf("scrub of a temp path = %q", got)
	}
}
