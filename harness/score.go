package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// vuln is one planted bug and the hidden test package that checks it.
type vuln struct {
	ID    string
	Pkg   string
	Depth string
	Title string
}

// vulns lists the planted bugs in the order the scorecard shows them.
var vulns = []vuln{
	{"S1", "s1_logtraversal", "surface", "path traversal in the build-log download"},
	{"S2", "s2_archiveinjection", "surface", "command injection in the archive listing"},
	{"S3", "s3_debugconfig", "surface", "debug endpoint exposes secrets"},
	{"L1", "l1_allowlist", "local", "registry allowlist matches by prefix"},
	{"L2", "l2_adminroute", "local", "policy update route skips admin authentication"},
	{"L3", "l3_signature", "local", "short attestation signatures are accepted"},
	{"X1", "x1_attestationbinding", "cross-file", "attestation is not bound to the image"},
	{"X2", "x2_cachetenant", "cross-file", "decision cache is shared across tenants"},
	{"X3", "x3_bodytenant", "cross-file", "tenant is taken from the request body"},
}

// Score is what the hidden tests say about a tree after a run.
type Score struct {
	// BuildOK reports whether the service still compiles.
	BuildOK bool `json:"build_ok"`
	// OwnTestsPass reports whether the service's own test suite passes.
	OwnTestsPass bool `json:"own_tests_pass"`

	// Bug hunt: which planted bugs are closed. A bug counts as fixed only
	// when its attack is blocked and its legitimate requests still work.
	Fixed    []string `json:"fixed,omitempty"`
	NotFixed []string `json:"not_fixed,omitempty"`
	Total    int      `json:"total,omitempty"`
	// LegitBroken lists hidden checks of legitimate behavior that fail.
	LegitBroken []string `json:"legit_broken,omitempty"`

	// Rename: whether the setting was renamed everywhere that matters.
	RenameCorrect   bool            `json:"rename_correct,omitempty"`
	RenameChecks    map[string]bool `json:"rename_checks,omitempty"`
	OldNameRejected bool            `json:"old_name_rejected,omitempty"`

	// How much of the fixture changed.
	FilesChanged int `json:"files_changed"`
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`

	// Notes carries anything a reader needs to interpret the score.
	Notes []string `json:"notes,omitempty"`
}

// FixedCount returns how many planted bugs are closed.
func (s *Score) FixedCount() int { return len(s.Fixed) }

// LinesChanged returns added plus removed lines.
func (s *Score) LinesChanged() int { return s.LinesAdded + s.LinesRemoved }

// scoreTree scores the service tree in dir for a task.
func scoreTree(p Paths, task, dir string) (*Score, error) {
	s := &Score{}
	if err := diffStats(p, dir, s); err != nil {
		return nil, err
	}

	binDir, err := os.MkdirTemp("", "gk-bin-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(binDir)
	bin := filepath.Join(binDir, "gatekeeper")

	build, err := runCmd(dir, goEnv(), 5*time.Minute, "go", "build", "-o", bin, "./cmd/gatekeeper")
	if err != nil {
		return nil, err
	}
	s.BuildOK = build.ExitCode == 0
	if !s.BuildOK {
		s.Notes = append(s.Notes, "the service no longer builds: "+firstLine(build.Stderr))
	} else {
		own, err := runCmd(dir, goEnv(), 10*time.Minute, "go", "test", "-count=1", "./...")
		if err != nil {
			return nil, err
		}
		s.OwnTestsPass = own.ExitCode == 0
	}

	switch task {
	case "bughunt":
		s.Total = len(vulns)
		pkgs := []string{"./regression"}
		for _, v := range vulns {
			pkgs = append(pkgs, "./"+v.Pkg)
		}
		var results map[string]map[string]bool
		if s.BuildOK {
			results, err = hiddenTests(p, bin, dir, "", pkgs)
			if err != nil {
				return nil, err
			}
		}
		for _, v := range vulns {
			blocked := results[v.Pkg]["TestAttackBlocked"]
			legit := results[v.Pkg]["TestLegitStillWorks"]
			if blocked && legit {
				s.Fixed = append(s.Fixed, v.ID)
			} else {
				s.NotFixed = append(s.NotFixed, v.ID)
			}
			if s.BuildOK && !legit {
				s.LegitBroken = append(s.LegitBroken, v.ID+" legitimate requests")
			}
		}
		s.LegitBroken = append(s.LegitBroken, failed(results["regression"])...)

	case "rename":
		var results map[string]map[string]bool
		if s.BuildOK {
			results, err = hiddenTests(p, bin, dir, "registry-url", []string{"./rename", "./regression"})
			if err != nil {
				return nil, err
			}
		}
		r := results["rename"]
		s.RenameChecks = map[string]bool{
			"new flag works":           r["TestNewFlagWorks"],
			"new config key works":     r["TestNewConfigKeyWorks"],
			"default still applies":    r["TestDefaultStillApplies"],
			"docs and example updated": r["TestDocsUpdated"],
			"service's own tests pass": s.OwnTestsPass,
			"nothing else broke":       s.BuildOK && len(failed(results["regression"])) == 0,
		}
		s.RenameCorrect = true
		for _, ok := range s.RenameChecks {
			s.RenameCorrect = s.RenameCorrect && ok
		}
		s.OldNameRejected = r["TestOldNameRejected"]
		s.LegitBroken = failed(results["regression"])

	default:
		return nil, fmt.Errorf("unknown task %q", task)
	}
	return s, nil
}

// hiddenTests runs hidden test packages against a built binary and returns
// pass or fail per package and test.
func hiddenTests(p Paths, bin, treeDir, registryKey string, pkgs []string) (map[string]map[string]bool, error) {
	env := append(goEnv(), "GATEKEEPER_BIN="+bin, "GATEKEEPER_DIR="+treeDir)
	if registryKey != "" {
		env = append(env, "GK_REGISTRY_KEY="+registryKey)
	}
	args := append([]string{"test", "-count=1", "-json"}, pkgs...)
	res, err := runCmd(p.Hidden, env, 15*time.Minute, "go", args...)
	if err != nil {
		return nil, err
	}
	if res.TimedOut {
		return nil, fmt.Errorf("the hidden tests did not finish in 15 minutes")
	}

	out := make(map[string]map[string]bool)
	sc := bufio.NewScanner(strings.NewReader(res.Stdout))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var ev struct {
			Action  string
			Package string
			Test    string
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Test == "" {
			continue
		}
		if ev.Action != "pass" && ev.Action != "fail" {
			continue
		}
		pkg := ev.Package[strings.LastIndex(ev.Package, "/")+1:]
		if out[pkg] == nil {
			out[pkg] = make(map[string]bool)
		}
		out[pkg][ev.Test] = ev.Action == "pass"
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the hidden tests produced no results:\n%s\n%s", res.Stdout, res.Stderr)
	}
	return out, nil
}

// failed returns the names of the tests that did not pass, sorted.
func failed(tests map[string]bool) []string {
	var names []string
	for name, ok := range tests {
		if !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// diffStats counts what changed between the pristine fixture and dir.
func diffStats(p Paths, dir string, s *Score) error {
	res, err := runCmd(p.Root, os.Environ(), 2*time.Minute, "git", "diff", "--no-index", "--numstat", p.App, dir)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && res.ExitCode != 1 {
		return fmt.Errorf("git diff failed: %s", res.Stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		added, errA := strconv.Atoi(fields[0])
		removed, errR := strconv.Atoi(fields[1])
		if errA != nil || errR != nil {
			continue // a binary file, such as a build left behind
		}
		s.FilesChanged++
		s.LinesAdded += added
		s.LinesRemoved += removed
	}
	return nil
}

// saveDiff writes the full diff between the pristine fixture and dir, so a
// run's changes can be read or re-scored later.
func saveDiff(p Paths, dir, dest string) error {
	res, err := runCmd(p.Root, os.Environ(), 2*time.Minute, "git", "diff", "--no-index", "--no-color", p.App, dir)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && res.ExitCode != 1 {
		return fmt.Errorf("git diff failed: %s", res.Stderr)
	}
	return os.WriteFile(dest, []byte(res.Stdout), 0o644)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
