#!/bin/sh
# Proves each invariant test can fire: remove the mechanism, expect red.
# A test that stays green with its mechanism deleted protects nothing.
set -u
cd "$(dirname "$0")/.."
if [ -n "$(git status --porcelain)" ]; then echo "mutate.sh needs a clean tree" >&2; exit 2; fi
fail=0
m() { # desc file from to test pkg
  python3 - "$2" "$3" "$4" <<'PY' || { echo "stale mutation: $1" >&2; fail=1; return; }
import sys; f, a, b = sys.argv[1:]; s = open(f).read()
if a not in s: sys.exit(1)
open(f, "w").write(s.replace(a, b, 1))
PY
  if ! go build ./... >/dev/null 2>&1; then echo "does not compile: $1" >&2; fail=1
  elif go test -count=1 -run "$5" "$6" >/dev/null 2>&1; then echo "SURVIVED  $1"; fail=1
  else echo "killed    $1"; fi
  git checkout -q -- .
}
m "liveness ignores start time"        internal/liveness/liveness.go 'start == "" || start != rec.Start' 'start == ""' TestReusedPidIsNotAlive ./internal/liveness
m "done signal matched as substring"   internal/ledger/ledger.go 'return last == marker+" "+branch, nil' 'return strings.Contains(last, marker), nil' TestEchoedMarkerIsNotDone ./internal/ledger
m "skeleton reports accepted"          internal/ledger/ledger.go 'if pat != "" && strings.Contains(body, pat)' 'if pat == "never-matches" && strings.Contains(body, pat)' TestSkeletonReportIsNotHarvestable ./internal/ledger
m "dirty mainline accepted"            internal/gate/gate.go 'if len(d) > 0 {
		return CodeMainDirty' 'if false {
		return CodeMainDirty' TestDirtyMainlineIsRefused ./internal/gate
m "branch judged alone"                internal/gate/gate.go 'pv, err := preview(c, base, []string{branch})' 'tipx, _ := root.Rev(branch); pv, err := preview(c, tipx, []string{branch})' TestCrossRedIsCaughtBeforeTheMainline ./internal/gate
m "no second run after merge"          internal/gate/gate.go 'post := runAll("mainline", c.Checks, c.Root)' 'post := []CheckResult{}' TestRedAfterTheMergeKeepsTheWorktree ./internal/gate
m "no retry, flaky blocks"             internal/gate/checks.go 'attempt <= ch.RetryOnFail' 'attempt <= 0' TestFlakyCheckIsMergedAndReported ./internal/gate
m "timeout blamed on branch"           internal/gate/checks.go 'if timedOut || isInfra' 'if (timedOut && false) || isInfra' TestTimeoutIsInfrastructure ./internal/gate
m "ledger accumulation bisected"       internal/gate/batch.go 'if s.onlyLedger {' 'if false {' TestLedgerCheckAccumulationIsReconciled ./internal/gate
m "missing config fields defaulted"    internal/config/config.go 'if len(missing) > 0 {' 'if false {' TestMissingFieldsAreNamed ./internal/config
m "worktree cut from HEAD"             internal/workspace/workspace.go 'p.Worktree, c.Mainline)' 'p.Worktree, "HEAD")' TestWorktreeStartsFromTheMainline ./internal/launch
exit $fail
