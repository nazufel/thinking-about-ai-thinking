// Command harness runs the effort-level demo: the same task, on the same
// fixture, at different Claude Code effort levels, scored by hidden tests.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const usage = `harness: run one task at different Claude Code effort levels and score the results

Costs nothing (no model is called):
  selfcheck [-thorough]    prove that the scoring tells a fixed service from an unfixed one
  preflight                check that this machine can run the demo
  score -task T -dir D     score a service tree by hand
  scorecard [-plan P]      print the results of a plan (default: full)
  show ID                  print one run's closing report under a caption
  median -task T -effort E [-plan P]   print the ID of a level's median-scoring run
  extras-blind [-plan P]   copy the bug-hunt diffs under random names, for grading the second measure
  extras-unblind -method M join the grades with the run IDs and write results/extras.json

Calls the model, and so spends money or usage:
  preflight -online        two very small calls: is max effort capped, is the session isolated
  run -task T -effort E    one run
  sweep -plan pilot|full   every run of a plan that is not recorded yet

Tasks: bughunt, rename. Effort levels: low, medium, high, xhigh, max.
Add -dry-run to run or sweep to see what would happen without calling the model.
Run "harness <command> -h" for a command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		os.Exit(1)
	}
}

// runFlags registers the flags shared by run and sweep.
func runFlags(fs *flag.FlagSet, o *RunOptions) {
	fs.StringVar(&o.Model, "model", "claude-opus-5-5", "model to run")
	fs.StringVar(&o.Tools, "tools", "Bash,Edit,Read,Write", "built-in tools the model may use")
	fs.Float64Var(&o.BudgetUSD, "budget", 10, "most a single run may spend, in US dollars at list price")
	fs.DurationVar(&o.Timeout, "timeout", 45*time.Minute, "longest a single run may take")
	fs.BoolVar(&o.DryRun, "dry-run", false, "show what would run without calling the model")
	fs.BoolVar(&o.Keep, "keep", false, "keep each run's working directory instead of deleting it")
}

func dispatch(command string, args []string) error {
	if command == "help" || command == "-h" || command == "--help" {
		fmt.Print(usage)
		return nil
	}
	p, err := findRoot()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet(command, flag.ExitOnError)

	switch command {
	case "selfcheck":
		thorough := fs.Bool("thorough", false, "also check each planted bug's fix on its own (about two minutes)")
		fs.Parse(args)
		return selfcheck(p, *thorough)

	case "preflight":
		model := fs.String("model", "claude-opus-5-5", "model to probe with -online")
		tools := fs.String("tools", "Bash,Edit,Read,Write", "tool set to probe with -online")
		online := fs.Bool("online", false, "also make two very small model calls")
		fs.Parse(args)
		return preflight(*model, *tools, *online)

	case "score":
		task := fs.String("task", "bughunt", "bughunt or rename")
		dir := fs.String("dir", "", "service tree to score")
		fs.Parse(args)
		if *dir == "" {
			return fmt.Errorf("score needs -dir")
		}
		s, err := scoreTree(p, *task, *dir)
		if err != nil {
			return err
		}
		printScore(*task, s)
		return nil

	case "run":
		o := RunOptions{Plan: "single"}
		fs.StringVar(&o.Task, "task", "bughunt", "bughunt or rename")
		fs.StringVar(&o.Effort, "effort", "", "low, medium, high, xhigh or max")
		fs.IntVar(&o.Trial, "trial", 1, "trial number, to tell repeated runs apart")
		runFlags(fs, &o)
		fs.Parse(args)
		if !o.DryRun {
			defer keepAwake()()
		}
		rec, err := runOne(p, o)
		if err != nil || rec == nil {
			return err
		}
		fmt.Println(oneLine(rec))
		fmt.Println("record: results/runs/" + rec.ID + ".json")
		return nil

	case "sweep":
		var o RunOptions
		fs.StringVar(&o.Plan, "plan", "", "pilot (4 runs) or full (32 runs)")
		seed := fs.Int64("seed", 0, "seed for the run order (default: the clock)")
		runFlags(fs, &o)
		fs.Parse(args)
		if o.Plan == "" {
			return fmt.Errorf("sweep needs -plan pilot or -plan full")
		}
		return sweep(p, *seed, o)

	case "scorecard":
		plan := fs.String("plan", "full", "plan to report")
		fs.Parse(args)
		recs, err := loadRecords(p)
		if err != nil {
			return err
		}
		ex, err := loadExtras(p)
		if err != nil {
			return err
		}
		scorecard(os.Stdout, recs, *plan, ex)
		return nil

	case "extras-blind":
		plan := fs.String("plan", "full", "plan whose bug-hunt diffs to prepare for grading")
		fs.Parse(args)
		return blind(p, *plan)

	case "extras-unblind":
		method := fs.String("method", "", "one line saying who graded the diffs and how")
		fs.Parse(args)
		if *method == "" {
			return fmt.Errorf("extras-unblind needs -method, a line saying who graded the diffs and how")
		}
		return unblind(p, *method)

	case "show":
		fs.Parse(args)
		if fs.NArg() != 1 {
			return fmt.Errorf("show needs one run ID, or enough of one to be unique")
		}
		recs, err := loadRecords(p)
		if err != nil {
			return err
		}
		rec, err := findRecord(recs, fs.Arg(0))
		if err != nil {
			return err
		}
		show(os.Stdout, rec)
		return nil

	case "median":
		task := fs.String("task", "bughunt", "bughunt or rename")
		effort := fs.String("effort", "", "effort level")
		plan := fs.String("plan", "full", "plan to pick from")
		fs.Parse(args)
		recs, err := loadRecords(p)
		if err != nil {
			return err
		}
		rec := medianRun(completed(recs, *plan, *task)[*effort])
		if rec == nil {
			return fmt.Errorf("no completed %s runs at %s in plan %s", *task, *effort, *plan)
		}
		fmt.Println(rec.ID)
		return nil
	}
	return fmt.Errorf("unknown command %q: run the harness with no arguments for help", command)
}

// printScore prints a score for a person to read.
func printScore(task string, s *Score) {
	fmt.Printf("builds: %v, own tests pass: %v, changed %d files (+%d -%d lines)\n",
		s.BuildOK, s.OwnTestsPass, s.FilesChanged, s.LinesAdded, s.LinesRemoved)
	if task == "bughunt" {
		fmt.Printf("fixed %d of %d planted bugs\n", s.FixedCount(), s.Total)
		for _, v := range vulns {
			state := "open "
			for _, id := range s.Fixed {
				if id == v.ID {
					state = "fixed"
				}
			}
			fmt.Printf("  %s  %s  %-10s %s\n", state, v.ID, v.Depth, v.Title)
		}
	} else {
		fmt.Printf("renamed correctly: %v (old name rejected: %v)\n", s.RenameCorrect, s.OldNameRejected)
		for name, ok := range s.RenameChecks {
			fmt.Printf("  %-5v %s\n", ok, name)
		}
	}
	if len(s.LegitBroken) > 0 {
		fmt.Printf("legitimate behavior broken:%s\n", list(s.LegitBroken))
	}
	for _, note := range s.Notes {
		fmt.Println("note:", note)
	}
}
