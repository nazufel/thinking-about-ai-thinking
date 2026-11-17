package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// cell is one run in a sweep.
type cell struct {
	Task   string `json:"task"`
	Effort string `json:"effort"`
	Trial  int    `json:"trial"`
}

// planCells returns the runs a named plan is made of.
//
// pilot is four runs that answer practical questions before any real money
// is spent: do unattended runs get through, what does max cost, and are the
// scores already saturated.
//
// full is the sweep behind the talk: five bug hunts and three renames at
// each of four effort levels.
func planCells(plan string) ([]cell, error) {
	switch plan {
	case "pilot":
		return cells("bughunt", []string{"low", "max"}, 2), nil
	case "full":
		levels := []string{"low", "medium", "high", "max"}
		return append(cells("bughunt", levels, 5), cells("rename", levels, 3)...), nil
	}
	return nil, fmt.Errorf("unknown plan %q: use pilot or full", plan)
}

func cells(task string, levels []string, trials int) []cell {
	var out []cell
	for _, level := range levels {
		for trial := 1; trial <= trials; trial++ {
			out = append(out, cell{Task: task, Effort: level, Trial: trial})
		}
	}
	return out
}

// sweep runs every cell of a plan that has no record yet, one at a time, in
// a random order. Interleaving the levels keeps time of day and cache state
// from lining up with a level.
func sweep(p Paths, seed int64, base RunOptions) error {
	all, err := planCells(base.Plan)
	if err != nil {
		return err
	}
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rand.New(rand.NewSource(seed)).Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })

	recs, err := loadRecords(p)
	if err != nil {
		return err
	}
	done := map[cell]bool{}
	for _, r := range recs {
		if r.Plan == base.Plan {
			done[cell{r.Task, r.Effort, r.Trial}] = true
		}
	}
	var todo []cell
	for _, c := range all {
		if !done[c] {
			todo = append(todo, c)
		}
	}

	fmt.Printf("plan %s: %d runs, %d already recorded, %d to go (seed %d)\n", base.Plan, len(all), len(all)-len(todo), len(todo), seed)
	fmt.Printf("model %s, tools %s, budget $%.2f and %s per run\n", base.Model, base.Tools, base.BudgetUSD, base.Timeout)
	for i, c := range todo {
		fmt.Printf("  %2d. %-8s %-7s trial %d\n", i+1, c.Task, c.Effort, c.Trial)
	}
	if base.DryRun || len(todo) == 0 {
		return nil
	}

	if err := os.MkdirAll(p.Runs, 0o755); err != nil {
		return err
	}
	// The hash of the second measure's list, taken before the first run, is
	// the evidence that the list was not changed to fit the results.
	extrasHash, _ := fileHash(filepath.Join(p.Root, "scoring", "EXTRAS.md"))
	order, _ := json.MarshalIndent(map[string]any{
		"plan":             base.Plan,
		"seed":             seed,
		"started":          time.Now().UTC().Format(time.RFC3339),
		"extras_list_hash": extrasHash,
		"order":            all,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(filepath.Dir(p.Runs), "sweep-"+base.Plan+".json"), append(order, '\n'), 0o644); err != nil {
		return err
	}

	defer keepAwake()()
	for i, c := range todo {
		o := base
		o.Task, o.Effort, o.Trial = c.Task, c.Effort, c.Trial
		fmt.Printf("\n[%d/%d] %s at %s, trial %d\n", i+1, len(todo), c.Task, c.Effort, c.Trial)
		rec, err := runOne(p, o)
		// A run the machine slept through is discarded and repeated.
		var slept *sleptError
		for attempt := 0; attempt < 2 && errors.As(err, &slept); attempt++ {
			fmt.Printf("    %v. Running it again.\n", err)
			rec, err = runOne(p, o)
		}
		if err != nil {
			return fmt.Errorf("%s at %s, trial %d: %w", c.Task, c.Effort, c.Trial, err)
		}
		fmt.Println("    " + oneLine(rec))
	}
	fmt.Println("\nsweep finished. Print the results with: go run ./harness scorecard -plan " + base.Plan)
	return nil
}
