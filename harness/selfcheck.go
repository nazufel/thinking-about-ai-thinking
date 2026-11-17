package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// selfcheck proves that the scoring works before any model is run. It calls
// no model and costs nothing.
//
// It scores the untouched fixture, which must come out as nothing fixed and
// nothing broken, and then the fixture with the reference fixes applied,
// which must come out as everything fixed and nothing broken.
//
// With thorough set it also applies each single-bug reference fix on its
// own. Each must be credited with exactly its own bug. That proves the nine
// hidden tests are independent: fixing one bug never earns credit for
// another, and never hides one.
func selfcheck(p Paths, thorough bool) error {
	work, err := os.MkdirTemp("", "effort-demo-selfcheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	failures := 0
	check := func(ok bool, what string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			failures++
		}
		fmt.Printf("  %s %s\n", mark, what)
	}

	fmt.Println("scoring material stays out of what the model sees:")
	leak, err := findCanary(p.App, p.Tasks)
	if err != nil {
		return err
	}
	check(leak == "", "no trace of the hidden tests in app/ or tasks/"+leak)

	fmt.Println("untouched fixture, bug hunt:")
	pristine := filepath.Join(work, "pristine")
	if err := copyTree(p.App, pristine); err != nil {
		return err
	}
	s, err := scoreTree(p, "bughunt", pristine)
	if err != nil {
		return err
	}
	check(s.BuildOK && s.OwnTestsPass, "builds, and its own tests pass")
	check(s.FixedCount() == 0, fmt.Sprintf("no planted bug counts as fixed (%d of %d)", s.FixedCount(), s.Total))
	check(len(s.LegitBroken) == 0, "every legitimate-behavior check passes"+list(s.LegitBroken))
	check(s.FilesChanged == 0, "no files differ from the fixture")

	fmt.Println("untouched fixture, rename:")
	s, err = scoreTree(p, "rename", pristine)
	if err != nil {
		return err
	}
	check(!s.RenameCorrect, "does not count as renamed")

	fmt.Println("reference fix, bug hunt:")
	fixed := filepath.Join(work, "fixed")
	if err := applyReference(p, "bughunt", fixed); err != nil {
		return err
	}
	s, err = scoreTree(p, "bughunt", fixed)
	if err != nil {
		return err
	}
	check(s.BuildOK && s.OwnTestsPass, "builds, and its own tests pass")
	check(s.FixedCount() == s.Total, fmt.Sprintf("every planted bug counts as fixed (%d of %d)%s", s.FixedCount(), s.Total, list(s.NotFixed)))
	check(len(s.LegitBroken) == 0, "every legitimate-behavior check passes"+list(s.LegitBroken))
	check(s.FilesChanged > 0, fmt.Sprintf("the change is measured (%d files, %d lines)", s.FilesChanged, s.LinesChanged()))

	fmt.Println("reference fix, rename:")
	renamed := filepath.Join(work, "renamed")
	if err := applyReference(p, "rename", renamed); err != nil {
		return err
	}
	s, err = scoreTree(p, "rename", renamed)
	if err != nil {
		return err
	}
	var missing []string
	for name, ok := range s.RenameChecks {
		if !ok {
			missing = append(missing, name)
		}
	}
	check(s.RenameCorrect, "counts as renamed"+list(missing))
	check(s.OldNameRejected, "the old name is rejected")
	check(len(s.LegitBroken) == 0, "every legitimate-behavior check passes"+list(s.LegitBroken))

	if thorough {
		fmt.Println("each bug fixed on its own:")
		for _, v := range vulns {
			dir := filepath.Join(work, "only-"+v.ID)
			if err := applyReference(p, filepath.Join("bugs", v.ID), dir); err != nil {
				return err
			}
			s, err := scoreTree(p, "bughunt", dir)
			if err != nil {
				return err
			}
			onlyThis := len(s.Fixed) == 1 && s.Fixed[0] == v.ID
			check(onlyThis && s.OwnTestsPass && len(s.LegitBroken) == 0,
				fmt.Sprintf("%s alone is credited with %s and nothing else (credited%s; broken%s)",
					v.ID, v.ID, orNone(s.Fixed), orNone(s.LegitBroken)))
		}
	} else {
		fmt.Println("skipped: the nine single-bug checks. Run them with: go run ./harness selfcheck -thorough")
	}

	if failures > 0 {
		return fmt.Errorf("%d check(s) failed: do not trust scores until this passes", failures)
	}
	fmt.Println("\nselfcheck passed: the scoring tells a fixed service from an unfixed one.")
	return nil
}

// applyReference copies the fixture to dest and applies a reference patch.
func applyReference(p Paths, task, dest string) error {
	if err := copyTree(p.App, dest); err != nil {
		return err
	}
	patch := filepath.Join(p.Reference, task+".patch")
	// The patches were made with "git diff --no-index a b", so two path
	// components are stripped.
	res, err := runCmd(dest, os.Environ(), time.Minute, "git", "apply", "-p2", patch)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("apply %s: %s", patch, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// findCanary reports the first file under the given directories that
// contains the scoring canary, as " (found in ...)", or "" when there is
// none.
func findCanary(dirs ...string) (string, error) {
	found := ""
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() || found != "" {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte(canary)) {
				found = " (found in " + path + ")"
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return found, nil
}

func list(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return ": " + strings.Join(items, ", ")
}

func orNone(items []string) string {
	if len(items) == 0 {
		return ": none"
	}
	return list(items)
}
