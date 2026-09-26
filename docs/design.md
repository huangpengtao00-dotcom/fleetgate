# Design

fleetgate comes from a production codebase maintained by one person with a
fleet of coding agents. Over thirty days, 1,289 branches were merged into its
mainline, and the maintainer's error log gained 57 entries, each written down
the day it happened.
This repository is a clean reimplementation of the parts that turned out to
matter. None of that codebase's code or business logic is here.

Each rule below names the failure it prevents and the test that replays it.
`scripts/mutate.sh` deletes the rule and confirms the test fails.

## 1. The mainline has one entrance

**Failure.** On one day, three red checks reached the mainline through
hand-typed `git merge && git push` chains. Exit codes were lost in pipes; the
merge "succeeded".

**Rule.** Workers never merge. `fleet harvest` and `fleet batch` are the only
code that merges into the mainline, and they refuse a dirty root worktree.

**Tests.** `TestDirtyMainlineIsRefused`, `TestConcurrentHarvestIsRefused`.

## 2. The merged tree is what gets checked

**Failure.** Two branches changed different files. Each passed its checks. The
combination failed, and it was only noticed after both were merged.

**Rule.** The gate merges `mainline + branch` in a detached worktree and runs
the checks there before touching the mainline. When that fails, it checks the
branch alone and reports `self` (red alone) or `cross` (red only in
combination). The checks run again on the mainline after the real merge.

**Tests.** `TestCrossRedIsCaughtBeforeTheMainline`,
`TestRedOnItsOwnIsAttributedToTheBranch`, `TestRedAfterTheMergeKeepsTheWorktree`.

## 3. Liveness is a pid plus a start time, never a name

**Failure.** Killing workers with `pkill -f <branch>` also matched other
workers whose prompts mentioned that branch; three were killed. Separately, a
bare `kill -0` reported a long-dead worker as alive because its pid had been
reused.

**Rule.** The pid file stores the pid and the start time the OS reported at
launch. A worker is alive only if both still match. Command lines are never
consulted.

A pid file that cannot be read is an unknown worker, not an absent one. The
capacity count stops with an error instead of counting it as idle, which
would let `launch` exceed `max_workers`.

**Tests.** `TestReusedPidIsNotAlive`,
`TestLookAlikeProcessDoesNotKeepAWorkerAlive`,
`TestUnreadablePidFileIsNotCountedAsIdle`.

## 4. Done means four things at once

**Failure.** The done marker appeared in a log because the agent echoed its
prompt, which contained the marker. Another time a report file existed but was
still a skeleton, and it was merged five minutes before it would have been
written.

**Rule.** A worker is harvestable only if its process has exited, its branch is
ahead of the mainline, the last non-empty log line is exactly
`<done_marker> <branch>`, and it changed at least one report that contains none
of the placeholder patterns.

**Tests.** `TestEchoedMarkerIsNotDone`, `TestSkeletonReportIsNotHarvestable`.

## 5. State is derived, not stored

**Failure.** The scheduler's state lived in a session's scratch directory. When
the session ended, so did the knowledge of which workers existed and what they
had done.

**Rule.** The ledger is recomputed from git, pid files and logs on every call.
There is no database and no state file to fall out of date.

**Test.** `TestLedgerIsRebuiltFromSources`.

## 6. Three kinds of failure; only one is the branch's fault

**Failure.** An order-dependent test failed on its first run and passed alone.
Checks killed by a loaded machine were read as red. Both caused good branches
to be rejected or bisected.

**Rule.** A failure that passes on retry is flaky: allowed, and reported. A
timeout or a declared infrastructure signature stops the harvest without
judging the branch. Only a failure that reproduces counts against it.

**Tests.** `TestFlakyCheckIsMergedAndReported`, `TestTimeoutIsInfrastructure`,
`TestInfrastructureFailureIsNotBlamedOnTheBranch`.

## 7. Accumulation is not a bad branch

**Failure.** A check counted hard-coded font sizes against a baseline. Twelve
branches each added one or two and stayed under it; the batch crossed it. The
batch was bisected down to single branches, each of which passed and was merged
alone, raising the baseline one step at a time. Many full check runs were spent
to reach the same result.

**Rule.** Checks declare a kind. For a `ledger` check that is red only on the
combined set, while every branch is green on it alone, the set is merged and the
baseline re-recorded in a separate commit. `strict` checks are never treated
this way.

**Tests.** `TestLedgerCheckAccumulationIsReconciled`,
`TestStrictCheckIsNeverReconciled`, `TestBatchBisectsToTheBadBranch`.

## 8. Thresholds live in configuration and are never defaulted

**Reason.** The original system kept its limits in one registry file and
refused to run without it, so that a limit could not be changed by editing one
script and forgetting another. This is the one rule here that is a precaution
rather than a replay.

**Rule.** Every threshold is a required field in `fleet.json`. Missing or
unknown fields are errors.

**Tests.** `TestMissingConfigIsAnError`, `TestMissingFieldsAreNamed`,
`TestUnknownFieldsAreRejected`.

## 9. Worktrees start from the mainline

**Failure.** A worktree created with a bare `git worktree add` started from
whatever was checked out, which was a stale branch with uncommitted changes.

**Rule.** New worktrees are always cut from the configured mainline ref.

**Test.** `TestWorktreeStartsFromTheMainline`.

## 10. The documentation is checked against the code

**Failure.** Found in this repository. The README's exit-code table had
drifted from the constants: codes 1 and 11 existed but were undocumented, the
`missing` worker state was not listed, and usage errors exited 2, which the
table defines as "root worktree dirty". A script branching on the exit status
would have read a typo as a dirty checkout.

**Rule.** A fact the README states about the code has one source in the code
(`gate.Codes`, `ledger.States`, the CLI usage text) and a test that fails when
the README disagrees, in either direction. Every `Code*` constant must be
published. Every test this document cites must exist, and every test
`mutate.sh` relies on must be cited here. Usage errors exit 64, a code with no
other meaning.

**Tests.** `TestReadmeExitCodesMatchTheCode`,
`TestEveryExitCodeConstantIsPublished`, `TestReadmeWorkerStatesMatchTheCode`,
`TestDesignCitesOnlyRealTests`, `TestMutationsNameRealTests`,
`TestReadmeCommandsMatchTheCLI`.

## What is left out

Remote worker hosts, deployment after the merge, and a UI were all part of the
original system. They depend on the environment more than on the rules above
and are not included.
