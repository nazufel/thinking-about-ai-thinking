package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Paths locates the parts of the repository the harness works with.
type Paths struct {
	Root      string // repository root
	App       string // the fixture handed to Claude
	Tasks     string // the prompts
	Hidden    string // the hidden scoring tests (their own Go module)
	Reference string // the reference fixes, as patches
	Runs      string // one JSON record per run; safe to commit
	Raw       string // raw transcripts and diffs; kept out of git
}

// findRoot walks up from the working directory to the repository root.
func findRoot() (Paths, error) {
	dir, err := os.Getwd()
	if err != nil {
		return Paths{}, err
	}
	for {
		if isFile(filepath.Join(dir, "app", "go.mod")) && isFile(filepath.Join(dir, "scoring", "hidden", "go.mod")) {
			return Paths{
				Root:      dir,
				App:       filepath.Join(dir, "app"),
				Tasks:     filepath.Join(dir, "tasks"),
				Hidden:    filepath.Join(dir, "scoring", "hidden"),
				Reference: filepath.Join(dir, "scoring", "reference"),
				Runs:      filepath.Join(dir, "results", "runs"),
				Raw:       filepath.Join(dir, "results", "raw"),
			}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Paths{}, errors.New("run the harness from inside the repository (app/ and scoring/hidden/ were not found)")
		}
		dir = parent
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// goEnv returns the environment for every Go command the harness or Claude
// runs. Go stays off the network and never downloads another toolchain, so
// every run builds with exactly what is on the machine.
func goEnv() []string {
	return append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
}

// sessionVars are the variables Claude Code sets in the subprocesses of a
// running session. They describe that session, not the user's setup.
var sessionVars = map[string]bool{
	"CLAUDECODE":                   true,
	"CLAUDE_CODE_CHILD_SESSION":    true,
	"CLAUDE_CODE_ENTRYPOINT":       true,
	"CLAUDE_CODE_EXECPATH":         true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true,
	"CLAUDE_CODE_MESSAGING_TOKEN":  true,
	"CLAUDE_CODE_SESSION_ATTENDED": true,
	"CLAUDE_CODE_SESSION_ID":       true,
	"CLAUDE_EFFORT":                true,
	"CLAUDE_PID":                   true,
}

// insideSession reports whether the harness was started from inside a
// Claude Code session, for example by its Bash tool.
func insideSession() bool {
	return os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_CHILD_SESSION") != ""
}

// claudeEnv returns the environment for every Claude Code run the harness
// starts. It is goEnv without the variables of any Claude Code session the
// harness itself is running inside. Claude Code treats a process that
// inherits them as a child of that session, so leaving them in would make a
// run started from inside Claude Code differ from one started in a terminal.
func claudeEnv() []string {
	var env []string
	for _, kv := range goEnv() {
		name, _, _ := strings.Cut(kv, "=")
		if !sessionVars[name] {
			env = append(env, kv)
		}
	}
	return env
}

// copyTree copies src to dst, leaving out version-control data. History
// could reveal how the fixture was built.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dst, rel), info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// treeHash returns a short hash of every file under dir, so a record can say
// exactly which fixture a run started from.
func treeHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, path := range files {
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12], nil
}

// cmdResult is the outcome of a finished command.
type cmdResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// runCmd runs a command to completion with a time limit.
func runCmd(dir string, env []string, timeout time.Duration, name string, args ...string) (cmdResult, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return cmdResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var res cmdResult
	select {
	case err := <-done:
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit):
			res.ExitCode = exit.ExitCode()
		default:
			return cmdResult{}, err
		}
	case <-time.After(timeout):
		cmd.Process.Kill()
		<-done
		res.TimedOut = true
		res.ExitCode = -1
	}
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	return res, nil
}
