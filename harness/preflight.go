package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// preflight checks that this machine can run the demo. Without online it
// calls no model and costs nothing. With online it makes two very small
// model calls to check things only a real call can show.
func preflight(model, tools string, online bool) error {
	failures := 0
	check := func(ok bool, what, fix string) {
		if ok {
			fmt.Printf("  ok   %s\n", what)
			return
		}
		failures++
		fmt.Printf("  FAIL %s\n       %s\n", what, fix)
	}

	fmt.Println("tools:")
	for _, tool := range []string{"claude", "go", "git", "tar", "sh"} {
		_, err := exec.LookPath(tool)
		check(err == nil, tool+" is installed", "install "+tool+" and put it on PATH")
	}

	fmt.Println("settings that would spoil the comparison:")
	level := os.Getenv("CLAUDE_CODE_EFFORT_LEVEL")
	check(level == "", "CLAUDE_CODE_EFFORT_LEVEL is not set",
		"it is set to "+level+" and overrides --effort on every run: unset it")

	if insideSession() {
		fmt.Println("  note this was started from inside a Claude Code session. The harness removes that")
		fmt.Println("       session's variables from every run, so runs match ones started in a terminal.")
	}

	fmt.Println("Claude Code flags the harness relies on:")
	help, err := runCmd("", os.Environ(), 30*time.Second, "claude", "--help")
	if err != nil {
		return fmt.Errorf("claude --help: %w", err)
	}
	for _, flag := range []string{"--effort", "--safe-mode", "--tools", "--max-budget-usd", "--no-session-persistence", "--output-format", "--verbose"} {
		check(strings.Contains(help.Stdout, flag), flag, "this Claude Code version does not list it: update Claude Code")
	}
	fmt.Printf("  version: %s\n", cliVersion())

	if !online {
		fmt.Println("\nskipped: the two checks that need a model call. Run them with: go run ./harness preflight -online")
	} else {
		fmt.Println("effort cap (one small model call):")
		// The docs say that a role capped below max runs at the cap and says
		// so on stderr for a plain-text run. A capped run would make the
		// max column a mislabeled lower level.
		res, err := runCmd(os.TempDir(), claudeEnv(), 5*time.Minute, "claude",
			"-p", "Reply with the single word OK.",
			"--model", model, "--effort", "max", "--safe-mode", "--tools", "", "--no-session-persistence")
		if err != nil {
			return err
		}
		check(res.ExitCode == 0, "a call at max effort succeeds", "claude exited with an error: "+firstLine(res.Stderr))
		capped := strings.Contains(strings.ToLower(res.Stderr), "effort")
		check(!capped, "max effort is not capped for this account",
			"Claude Code warned about the effort level: "+firstLine(res.Stderr))

		fmt.Println("isolation and tool set (one small model call):")
		res, err = runCmd(os.TempDir(), claudeEnv(), 5*time.Minute, "claude",
			"-p", "Do not call any tool. Answer in four short lines. Line 1: the names of the tools you have. Line 2: instruction files loaded, such as CLAUDE.md, or 'none'. Line 3: skills and plugins loaded, or 'none'. Line 4: MCP servers connected, or 'none'.",
			"--model", model, "--effort", "low", "--safe-mode", "--tools", tools, "--no-session-persistence")
		if err != nil {
			return err
		}
		check(res.ExitCode == 0, "a call in safe mode succeeds", "claude exited with an error: "+firstLine(res.Stderr))
		fmt.Printf("  what the model reports. Read it: the tools should be %s and nothing else,\n  and anything personal on lines 2 to 4 would differ between laptops.\n", tools)
		for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			fmt.Println("    | " + line)
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	fmt.Println("\npreflight passed.")
	return nil
}
