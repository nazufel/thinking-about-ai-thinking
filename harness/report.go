package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// oneLine summarizes a run in a single line.
func oneLine(r *Record) string {
	var what string
	switch {
	case r.Score == nil:
		what = "not scored"
	case r.Task == "bughunt":
		what = fmt.Sprintf("fixed %d of %d", r.Score.FixedCount(), r.Score.Total)
	case r.Score.RenameCorrect:
		what = "renamed correctly"
	default:
		what = "rename incomplete"
	}
	line := fmt.Sprintf("%s at %s: %s, %.1f min, $%.2f at list price", r.Task, r.Effort, what, r.Minutes, r.CostUSD)
	if r.PermissionDenials > 0 {
		line += fmt.Sprintf(" (%d tool call(s) denied)", r.PermissionDenials)
	}
	if r.Outcome != outcomeCompleted {
		line += fmt.Sprintf(" [%s: %s]", r.Outcome, r.Cause)
	}
	return line
}

// deniedRuns returns how many of the runs had a tool call refused, as "k/n".
func deniedRuns(runs []*Record) string {
	n := 0
	for _, r := range runs {
		if r.PermissionDenials > 0 {
			n++
		}
	}
	return fmt.Sprintf("%d/%d", n, len(runs))
}

// points returns the number a run is ranked by: bugs fixed, or 1 for a
// correct rename.
func points(r *Record) int {
	if r.Score == nil {
		return 0
	}
	if r.Task == "bughunt" {
		return r.Score.FixedCount()
	}
	if r.Score.RenameCorrect {
		return 1
	}
	return 0
}

// completed returns the plan's completed runs of a task, grouped by effort.
func completed(recs []*Record, plan, task string) map[string][]*Record {
	out := map[string][]*Record{}
	for _, r := range recs {
		if r.Plan == plan && r.Task == task && r.Outcome == outcomeCompleted && r.Score != nil {
			out[r.Effort] = append(out[r.Effort], r)
		}
	}
	return out
}

// medianRun returns the run in the middle when the runs are ordered by
// score and then by duration. With an even count it is the lower middle.
// The rule is fixed here so that nobody picks which run to show.
func medianRun(runs []*Record) *Record {
	if len(runs) == 0 {
		return nil
	}
	sorted := append([]*Record(nil), runs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if points(sorted[i]) != points(sorted[j]) {
			return points(sorted[i]) < points(sorted[j])
		}
		return sorted[i].Minutes < sorted[j].Minutes
	})
	return sorted[(len(sorted)-1)/2]
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func collect(runs []*Record, f func(*Record) float64) []float64 {
	out := make([]float64, len(runs))
	for i, r := range runs {
		out[i] = f(r)
	}
	return out
}

// scorecard prints the results of a plan: one row per effort level, with
// every run's score listed so the spread is visible next to the median. ex
// holds the hand grades for the second measure and may be nil.
func scorecard(w io.Writer, recs []*Record, plan string, ex *ExtrasFile) {
	var model string
	total := 0
	for _, r := range recs {
		if r.Plan == plan {
			model = r.Model
			total++
		}
	}
	if total == 0 {
		fmt.Fprintf(w, "no runs recorded for plan %q yet\n", plan)
		return
	}
	fmt.Fprintf(w, "Plan %s, model %s, %d runs. Cost is Claude Code's estimate at list prices.\n", plan, model, total)

	hunt := completed(recs, plan, "bughunt")
	if len(hunt) > 0 {
		fmt.Fprintf(w, "\nBUG HUNT: planted bugs fixed, of %d\n", len(vulns))
		fmt.Fprintf(w, "%-7s %-5s %-16s %-7s %-6s %-8s %-8s %-11s %-14s %-10s %-7s %s\n",
			"level", "runs", "each run", "median", "range", "minutes", "cost", "tool calls", "lines changed", "ran tests", "denied", "legit broken")
		for _, level := range efforts {
			runs := hunt[level]
			if len(runs) == 0 {
				continue
			}
			scores := collect(runs, func(r *Record) float64 { return float64(points(r)) })
			sort.Float64s(scores)
			each := make([]string, len(scores))
			for i, s := range scores {
				each[i] = fmt.Sprintf("%.0f", s)
			}
			ranTests, broken := 0, 0
			for _, r := range runs {
				if r.RanTests {
					ranTests++
				}
				broken += len(r.Score.LegitBroken)
			}
			fmt.Fprintf(w, "%-7s %-5d %-16s %-7s %-6s %-8.1f %-8s %-11.0f %-14.0f %-10s %-7s %d\n",
				level, len(runs), strings.Join(each, " "),
				trimFloat(median(scores)),
				fmt.Sprintf("%.0f-%.0f", scores[0], scores[len(scores)-1]),
				median(collect(runs, func(r *Record) float64 { return r.Minutes })),
				fmt.Sprintf("$%.2f", median(collect(runs, func(r *Record) float64 { return r.CostUSD }))),
				median(collect(runs, func(r *Record) float64 { return float64(r.ToolCalls) })),
				median(collect(runs, func(r *Record) float64 { return float64(r.Score.LinesChanged()) })),
				fmt.Sprintf("%d/%d", ranTests, len(runs)),
				deniedRuns(runs),
				broken)
		}

		fmt.Fprintf(w, "\nFixed in how many runs, by bug:\n")
		fmt.Fprintf(w, "%-4s %-11s", "bug", "depth")
		var levels []string
		for _, level := range efforts {
			if len(hunt[level]) > 0 {
				levels = append(levels, level)
				fmt.Fprintf(w, " %-7s", level)
			}
		}
		fmt.Fprintln(w, " what")
		for _, v := range vulns {
			fmt.Fprintf(w, "%-4s %-11s", v.ID, v.Depth)
			for _, level := range levels {
				n := 0
				for _, r := range hunt[level] {
					for _, id := range r.Score.Fixed {
						if id == v.ID {
							n++
						}
					}
				}
				fmt.Fprintf(w, " %-7s", fmt.Sprintf("%d/%d", n, len(hunt[level])))
			}
			fmt.Fprintf(w, " %s\n", v.Title)
		}

		secondMeasure(w, hunt, levels, ex)
	}

	rename := completed(recs, plan, "rename")
	if len(rename) > 0 {
		fmt.Fprintf(w, "\nRENAME (control): renamed correctly\n")
		fmt.Fprintf(w, "%-7s %-5s %-8s %-8s %-8s %-11s %-14s %s\n", "level", "runs", "correct", "minutes", "cost", "tool calls", "lines changed", "denied")
		for _, level := range efforts {
			runs := rename[level]
			if len(runs) == 0 {
				continue
			}
			correct := 0
			for _, r := range runs {
				correct += points(r)
			}
			fmt.Fprintf(w, "%-7s %-5d %-8s %-8.1f %-8s %-11.0f %-14.0f %s\n",
				level, len(runs), fmt.Sprintf("%d/%d", correct, len(runs)),
				median(collect(runs, func(r *Record) float64 { return r.Minutes })),
				fmt.Sprintf("$%.2f", median(collect(runs, func(r *Record) float64 { return r.CostUSD }))),
				median(collect(runs, func(r *Record) float64 { return float64(r.ToolCalls) })),
				median(collect(runs, func(r *Record) float64 { return float64(r.Score.LinesChanged()) })),
				deniedRuns(runs))
		}
	}

	var stopped []*Record
	for _, r := range recs {
		if r.Plan == plan && r.Outcome != outcomeCompleted {
			stopped = append(stopped, r)
		}
	}
	if len(stopped) > 0 {
		fmt.Fprintf(w, "\nNOT COUNTED ABOVE: %d run(s) that did not complete. They are not scored as failures.\n", len(stopped))
		for _, r := range stopped {
			fmt.Fprintf(w, "  %s at %s, trial %d: %s (%s)\n", r.Task, r.Effort, r.Trial, r.Cause, r.ID)
		}
	}
	fmt.Fprintf(w, "\nMinutes, cost, tool calls and lines changed are medians. With this few runs, read a gap\nbetween levels as real only when it is wider than the spread in the \"each run\" column.\n")
	fmt.Fprintf(w, "\"denied\" counts the runs in which the permission system refused at least one tool call.\nThose runs went on and are scored; each record lists what was refused.\n")
}

// secondMeasure prints how many of the unplanted weaknesses each level's
// runs fixed. It is kept apart from the hidden-test score because it is
// graded by reading diffs, which is weaker evidence.
func secondMeasure(w io.Writer, hunt map[string][]*Record, levels []string, ex *ExtrasFile) {
	if ex == nil {
		return
	}
	graded := map[string][]*Record{}
	ungraded := 0
	for _, level := range levels {
		for _, r := range hunt[level] {
			if _, ok := ex.extrasCount(r.ID); ok {
				graded[level] = append(graded[level], r)
			} else {
				ungraded++
			}
		}
	}
	if len(graded) == 0 {
		return
	}

	fmt.Fprintf(w, "\nSECOND MEASURE: weaknesses fixed that were not planted, of %d\n", len(extras))
	fmt.Fprintf(w, "Graded by reading each diff against scoring/EXTRAS.md. Weaker evidence than the hidden tests above.\n")
	fmt.Fprintf(w, "%-7s %-5s %-16s %-7s %-6s\n", "level", "runs", "each run", "median", "range")
	for _, level := range levels {
		runs := graded[level]
		if len(runs) == 0 {
			continue
		}
		counts := collect(runs, func(r *Record) float64 {
			n, _ := ex.extrasCount(r.ID)
			return float64(n)
		})
		sort.Float64s(counts)
		each := make([]string, len(counts))
		for i, c := range counts {
			each[i] = fmt.Sprintf("%.0f", c)
		}
		fmt.Fprintf(w, "%-7s %-5d %-16s %-7s %-6s\n", level, len(runs), strings.Join(each, " "),
			trimFloat(median(counts)), fmt.Sprintf("%.0f-%.0f", counts[0], counts[len(counts)-1]))
	}

	fmt.Fprintf(w, "\nFixed in how many runs, by weakness:\n")
	fmt.Fprintf(w, "%-4s", "id")
	for _, level := range levels {
		if len(graded[level]) > 0 {
			fmt.Fprintf(w, " %-7s", level)
		}
	}
	fmt.Fprintln(w, " what")
	for _, e := range extras {
		fmt.Fprintf(w, "%-4s", e.ID)
		for _, level := range levels {
			if len(graded[level]) == 0 {
				continue
			}
			n := 0
			for _, r := range graded[level] {
				if _, ok := ex.Runs[r.ID][e.ID]; ok {
					n++
				}
			}
			fmt.Fprintf(w, " %-7s", fmt.Sprintf("%d/%d", n, len(graded[level])))
		}
		fmt.Fprintf(w, " %s\n", e.Title)
	}
	if ungraded > 0 {
		fmt.Fprintf(w, "%d completed run(s) have no grade yet and are left out of this measure.\n", ungraded)
	}
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.1f", f)
}

// findRecord returns the record whose ID equals or contains the given text.
func findRecord(recs []*Record, text string) (*Record, error) {
	var matches []*Record
	for _, r := range recs {
		if r.ID == text {
			return r, nil
		}
		if strings.Contains(r.ID, text) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no run matches %q", text)
	case 1:
		return matches[0], nil
	}
	return nil, fmt.Errorf("%d runs match %q: give more of the ID", len(matches), text)
}

// show prints one run's closing report under a one-line caption, ready for
// a screenshot.
func show(w io.Writer, r *Record) {
	var what string
	if r.Task == "bughunt" {
		what = fmt.Sprintf("fixed %d of %d planted bugs", r.Score.FixedCount(), r.Score.Total)
	} else if r.Score.RenameCorrect {
		what = "renamed correctly"
	} else {
		what = "rename incomplete"
	}
	fmt.Fprintf(w, "effort %s  |  %s  |  %.1f min  |  $%.2f at list price  |  %d tool calls\n",
		r.Effort, what, r.Minutes, r.CostUSD, r.ToolCalls)
	if r.Outcome != outcomeCompleted {
		fmt.Fprintf(w, "%s: %s\n", r.Outcome, r.Cause)
	}
	fmt.Fprintf(w, "%s\n\n%s\n", strings.Repeat("-", 78), strings.TrimSpace(r.FinalReport))
}
