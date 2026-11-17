# Thinking About AI Thinking

Companion code for the lightning talk of the same name.

Claude Code has an effort level: `low`, `medium`, `high`, `xhigh` or `max`. It
sets how much the model thinks, how far it digs and how hard it checks its own
work. This repo lets you see what that dial does. It runs the same task, on the
same code, with the same model, and changes only the effort level. Hidden
tests keep score.

> **`app/` is deliberately vulnerable.** It is a teaching fixture with nine
> planted security bugs. Do not deploy it or copy code from it.

## What you need

- Claude Code, signed in. Tested with 2.1.325; `preflight` tells you if your
  version lacks a flag the harness needs.
- Go 1.22 or later
- `git`, `tar` and `sh`
- macOS or Linux

Nothing else. The service and the harness use only the Go standard library.

## Try it

These two commands call no model and cost nothing.

```sh
go run ./harness selfcheck    # prove the scoring works
go run ./harness preflight    # check this machine can run the demo
```

These call the model, so they spend money or usage. Higher levels take longer
and cost more. Every run has a spend cap (`-budget`, default $10 at list
price) and a time limit (`-timeout`, default 45 minutes).

```sh
go run ./harness preflight -online            # two tiny calls: is max capped, is the session isolated
go run ./harness run -task rename  -effort low
go run ./harness run -task bughunt -effort low
go run ./harness run -task bughunt -effort max
go run ./harness scorecard -plan single
```

Add `-dry-run` to `run` or `sweep` to see the exact command without calling
the model.

To reproduce the talk's numbers, run the pilot first and then the full sweep:

```sh
go run ./harness sweep -plan pilot    # 4 runs: bug hunt at low and max, twice each
go run ./harness sweep -plan full     # 32 runs: 5 bug hunts and 3 renames at each of low, medium, high, max
go run ./harness scorecard            # the results table
go run ./harness show "$(go run ./harness median -task bughunt -effort max)"
```

A sweep skips runs that already have a record, so you can stop and resume it.
Stopping it with Ctrl-C also stops the run in flight, which leaves no record.

The full sweep takes about three hours. On macOS the harness keeps the
machine awake while it runs, but closing the lid still puts it to sleep. A
run the machine sleeps through is discarded and repeated, so leave the lid
open and the power connected.

## The two tasks

The prompts are in [`tasks/`](tasks/) and are identical at every level.

- **Bug hunt.** "Review this service for security vulnerabilities and fix
  every one you find." Nine bugs are planted at three depths: three you can
  see in one line, three that take reading a function and its caller, and
  three that only show when you follow a request across several files.
- **Rename.** "Rename the `registry` setting to `registry-url` everywhere it
  appears." This is the control. The work is finished when the tests pass, so
  more effort has nothing to buy.

Both are the Claude Code docs' own examples: "a small change like a rename"
for `low`, and "finding security vulnerabilities" for `max`.

## How a run is scored

Each run happens in a fresh copy of `app/`, in a temporary directory outside
this repo. When Claude exits, the harness builds the service and runs the
hidden tests in [`scoring/hidden/`](scoring/hidden/) against the binary, over
HTTP.

- A bug counts as **fixed** only when its attack is blocked *and* the
  legitimate requests around it still work. Switching a feature off is not a
  fix.
- The tests use only the service's flags, configuration file and HTTP API, so
  a run is free to rearrange the code.
- A run that stops short is recorded as **did-not-complete** with its cause:
  a timeout, the spend cap, a refusal, or no changes made. It is never
  counted as a low score.
- A refused tool call does not stop a run. Claude Code's permission system
  can refuse a command; the model is told and carries on. Such a run is
  scored, its record lists what was refused, and the scorecard shows how many
  runs at each level had a refusal.
- The model never sees the hidden tests. They carry a canary string, and the
  harness voids any run whose transcript contains it.

`selfcheck` proves all of this before you spend anything. The untouched
service must score 0 of 9 with nothing broken. The reference fix must score 9
of 9 with nothing broken. With `-thorough`, each bug's fix applied on its own
must be credited with exactly that bug.

### The second measure

In the pilot every run fixed all nine planted bugs, at `low` and at `max`
alike. The `max` runs then went on to fix weaknesses nobody had planted. They
are real, but the hidden tests cannot see them.

So the sweep has a second measure: how many of ten listed, unplanted
weaknesses a run fixed. The list is in
[`scoring/EXTRAS.md`](scoring/EXTRAS.md). It was written after the pilot and
frozen before the sweep, and it is graded by reading each run's diff, with
the effort level hidden from the grader. That is weaker evidence than the
hidden tests, so the scorecard prints it separately and says so.

```sh
go run ./harness extras-blind      # copy the bug-hunt diffs under random names
# grade them against scoring/EXTRAS.md, then:
go run ./harness extras-unblind -method "who graded, and how"
```

## What keeps the runs comparable

- One model, one prompt, one starting tree. Only `--effort` changes.
- `--safe-mode`: your own CLAUDE.md, skills, plugins, hooks and MCP servers
  are not loaded, so your results match your colleague's. Sign-in and
  admin-managed settings still apply.
- File tools and a shell only. No subagents, no web.
- Go stays off the network (`GOPROXY=off`, `GOTOOLCHAIN=local`).
- The harness refuses to run while `CLAUDE_CODE_EFFORT_LEVEL` is set, because
  it overrides `--effort`.
- A sweep runs its cells in a random order, so time of day and cache state
  do not line up with a level.
- If you start the harness from inside a Claude Code session, it removes that
  session's variables from every run, so the run matches one started in a
  plain terminal.

## Reading the scorecard

The scorecard lists every run's score next to the median, so you can see the
spread. With five runs per level, treat a gap between levels as real only
when it is wider than that spread.

Cost is Claude Code's own estimate at list prices. It is not what a
subscription seat is billed, but it compares levels fairly.

To pick the run shown for a level, `median` orders that level's runs by score
and then by duration and takes the middle one. The rule is fixed so that
nobody chooses a flattering run.

## What changed after the first runs

Two things were changed after some runs had been seen. Neither changed a
result that is reported here.

- **The second measure was added after the pilot**, as described above.
- **Runs with a refused tool call are now scored.** The first rule left out
  any run in which the permission system refused a command. The first three
  runs of the sweep showed why that was wrong: a rename at `max` finished
  correctly, but an extra verification command it had tried on its own was
  refused. Dropping such runs would have hidden correct work. Those three
  runs were discarded and the sweep was started again under the new rule.

One run was also discarded for a reason that has nothing to do with the
model: the laptop went to sleep in the middle of it. The harness now keeps
the machine awake, and throws away and repeats any run the machine slept
through.

## Spoilers

[`scoring/ANSWERS.md`](scoring/ANSWERS.md) lists the planted bugs, and
[`scoring/EXTRAS.md`](scoring/EXTRAS.md) lists the unplanted ones. Read them
after you have run the demo.

Do not start Claude Code in this repo and ask it to do the bug hunt: it could
read the answers. Use the harness, which gives the model a copy of `app/` and
nothing else.

## Layout

| Path | Holds |
| --- | --- |
| `app/` | gatekeeper, the service handed to Claude. It has its own README and tests. |
| `tasks/` | The two prompts. |
| `scoring/hidden/` | The hidden tests, one package per planted bug, plus regression and rename checks. |
| `scoring/reference/` | Reference fixes as patches, and the script that regenerates them. |
| `scoring/ANSWERS.md` | The answer key for the planted bugs. |
| `scoring/EXTRAS.md` | The list of unplanted weaknesses for the second measure. |
| `harness/` | The program that runs, scores and reports. |
| `results/runs/` | One JSON record per run. Records hold no local paths. |
| `results/extras.json` | The hand grades for the second measure, with a reason for each credit. |
| `results/raw/` | Raw transcripts and diffs. Not committed. |

## Limits

- gatekeeper is a toy, and its bugs were planted to have depth. A real
  codebase is larger and its bugs are not arranged in tiers.
- Runs vary. The same level can score differently twice. That is why the
  sweep repeats each cell and the scorecard shows every run.
- Results belong to one model on one version of Claude Code. The effort scale
  is calibrated per model, so `medium` on one model is not `medium` on
  another.
- A run works in its own temporary directory, which the harness deletes. The
  model may still leave scratch files elsewhere in the system temp folder; in
  the pilot one run kept a copy of the original code there to diff against.

## Changing the fixture

After any change to `app/`, regenerate the reference patches and check them:

```sh
python3 scoring/reference/make_patches.py
go run ./harness selfcheck -thorough
```
