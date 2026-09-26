# fleetgate

Run many coding agents against one git repository. Each agent works in its own
worktree; its branch reaches `main` only through a gate that runs your checks on
the merged tree.

```
go install github.com/huangpengtao00-dotcom/fleetgate/cmd/fleet@latest
```

Single binary, no dependencies outside the Go standard library and `git`.
macOS and Linux.

## The problem

Starting ten agents in ten worktrees is easy. What breaks is everything after:

- Two branches are each green and red together. Checking branches one at a time
  merges both.
- A worker looks alive because some other process mentions its branch name, or
  because its pid was reused.
- The log contains the "done" marker because the prompt was echoed back, and a
  half-written report is merged.
- A check times out on a loaded machine and the branch is blamed.
- Scheduler state lives in a terminal session and is gone the next morning.

These are the failures this code was written against. Each one is described in
[docs/design.md](docs/design.md) with the rule that prevents it and the test that
proves the rule holds.

## Usage

```
fleet init                          # write fleet.json
fleet launch <branch> <prompt-file> # new worktree from main, start the agent
fleet ledger                        # state of every worker
fleet rescue <branch>               # commit a dead worker's leftovers to its branch
fleet harvest <branch>              # merge one branch through the gate
fleet batch                         # merge every harvestable branch, bisecting reds
```

```
$ fleet ledger
BRANCH       STATE        AHEAD  DIRTY  LAST COMMIT  NOTE
add-admin    harvestable  1      0      2m ago
add-metrics  harvestable  1      0      3m ago

$ fleet harvest add-metrics
  preview  pass   port-unique
  mainline pass   port-unique
PASS: merged as d3e41d7b8

$ fleet harvest add-admin
  preview  fail   port-unique
FAIL: red on mainline+add-admin: port-unique (cross); not merged
```

`cross` means the branch passes on its own and fails only on top of the current
`main`. `./examples/demo.sh` reproduces this output in a scratch repository.

## The gate

`fleet harvest <branch>`:

1. Takes the merge lock. A second harvest exits with code 7.
2. Refuses if the worker is still running, has uncommitted changes, or if the
   root worktree is dirty or not on the mainline.
3. Merges `main + branch` in a detached temporary worktree and runs every check
   there. A conflict or a red check stops here; `main` has not moved.
4. Merges for real and runs the checks again on `main`. A red result here keeps
   the worktree and exits 5 so nothing gets pushed by accident.
5. Removes the worktree and the branch.

`fleet batch` merges a whole set into one preview and checks it once. A red set
is split in half and each half retried. One bad branch among n costs about
log2(n) extra runs.

Check failures are sorted into three kinds. A failure that passes on retry is
**flaky**: merged and reported. A timeout or a declared exit code or output
pattern is **infra**: not merged, not blamed on the branch. Everything else is a
**failure**. Only failures are bisected.

| Exit | Meaning |
|---|---|
| 0 | merged, checks green, worktree removed |
| 1 | unexpected error; nothing merged |
| 2 | root worktree dirty or not on the mainline |
| 3 | merge conflict in the preview |
| 5 | merged, then red on `main`; worktree kept |
| 6 | red on the merged preview; not merged |
| 7 | another harvest holds the lock |
| 8 | worker still running |
| 9 | checks could not run (infra); not merged |
| 10 | worker left uncommitted changes |
| 11 | no such branch |
| 64 | bad command line; nothing ran |

This table and the worker-state table below are tested against the constants
in the code (`cmd/fleet/docs_test.go`), so they cannot drift apart.

## Worker states

`fleet ledger` stores nothing. It derives each state from git, the pid file and
the log every time it runs, so any session can pick up where another left off.

| State | Condition |
|---|---|
| running | pid file names a live process with the recorded start time |
| stale | running, clean tree, no commit for `stale_after_min` |
| harvestable | exited, ahead of main, last log line is `<done_marker> <branch>`, at least one report without placeholders |
| dead-work | exited with commits or uncommitted changes, not done |
| dead-empty | exited with nothing |
| harvested | merged by the gate |
| missing | pid file whose branch no longer exists |

## Configuration

`fleet.json` at the repository root. Every field is required; a missing field
or an unknown one is an error.

```json
{
  "mainline": "main",
  "run_dir": ".fleet",
  "agent": ["sh", "-c", "claude -p \"$(cat \"$FLEET_PROMPT\")\" --dangerously-skip-permissions"],
  "checks": [
    {"name": "tests", "run": "go test ./...", "kind": "strict",
     "timeout_sec": 900, "retry_on_fail": 1, "infra_exit_codes": [124]}
  ],
  "max_workers": 8,
  "stale_after_min": 30,
  "done_marker": "WORKER-DONE",
  "report_dir": "reports",
  "placeholder_patterns": ["TODO: fill in"]
}
```

A check of kind `ledger` compares a count against a baseline stored in the
repository and must declare a `reconcile` command. When a batch is red only on
ledger checks and every branch is green on them alone, the branches each stayed
under the baseline and crossed it together. The batch is merged and `reconcile`
re-records the baseline in its own commit. Strict checks are never reconciled.

`run_dir` holds pid files, logs, prompts and worktrees. It gets its own
`.gitignore`, so none of it can be committed.

## Tests

```
go test ./...          # incident replays: temp repos, a scripted fake agent, offline
./scripts/mutate.sh    # deletes each rule in turn and expects its test to fail
```

`mutate.sh` removes each listed mechanism one at a time and checks that the
matching test goes red. A mutation that does not compile counts as a failure of the
script, not as a pass. CI runs both on Linux and macOS.

## Limits

- One machine. Workers on other hosts are not supported.
- Liveness uses `ps -o lstart` and `flock`, so no Windows.
- The gate is only as good as your checks.

## License

MIT
