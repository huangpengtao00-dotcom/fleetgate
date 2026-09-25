package gate_test

import (
	"strings"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gate"
	tu "github.com/huangpengtao00-dotcom/fleetgate/internal/testutil"
)

// One bad branch among four: the batch merges the other three and rejects
// only the bad one, bisecting instead of re-checking every branch alone.
func TestBatchBisectsToTheBadBranch(t *testing.T) {
	c := tu.Repo(t, strict("no-bad", `[ ! -f BAD ]`))
	for _, b := range []string{"a", "b", "c", "d"} {
		files := map[string]string{b + ".txt": b}
		if b == "c" {
			files["BAD"] = ""
		}
		tu.Branch(t, c, b, files)
	}
	br := gate.Batch(c, []string{"a", "b", "c", "d"})
	if len(br.Merged) != 3 || br.Rejected["c"] == "" || len(br.Rejected) != 1 {
		t.Fatalf("merged=%v rejected=%v msg=%s", br.Merged, br.Rejected, br.Message)
	}
	if br.Previews > 5 {
		t.Fatalf("took %d preview runs for 4 branches", br.Previews)
	}
}

func TestBatchAllGreenIsOneRun(t *testing.T) {
	c := tu.Repo(t)
	for _, b := range []string{"a", "b", "c"} {
		tu.Branch(t, c, b, map[string]string{b + ".txt": b})
	}
	br := gate.Batch(c, []string{"a", "b", "c"})
	if br.Code != gate.CodePass || len(br.Merged) != 3 || br.Previews != 1 {
		t.Fatalf("code=%d merged=%v previews=%d %s", br.Code, br.Merged, br.Previews, br.Message)
	}
}

// Invariant 7. A ledger check counts TODO files against a recorded baseline
// of one. Each branch adds one TODO and stays within the baseline alone;
// the pair exceeds it. That is accumulation, not a bad branch: the batch is
// merged and the baseline re-recorded, instead of bisected into rejecting
// work that was fine.
const todoCount = `ls *.todo 2>/dev/null | wc -l | tr -d ' '`

func todoRepo(t *testing.T, kind string) *config.Config {
	ch := config.Check{
		Name:       "todo-baseline",
		Run:        `[ "$(` + todoCount + `)" -le "$(cat baseline)" ]`,
		Kind:       kind,
		TimeoutSec: 30,
	}
	if kind == "ledger" {
		ch.Reconcile = todoCount + ` > baseline`
	}
	c := tu.Repo(t, ch)
	tu.WriteFile(t, c.Root, "baseline", "1\n")
	tu.Git(t, c.Root, "add", "-A")
	tu.Git(t, c.Root, "commit", "-q", "-m", "baseline")
	tu.Branch(t, c, "a", map[string]string{"a.todo": ""})
	tu.Branch(t, c, "b", map[string]string{"b.todo": ""})
	return c
}

func TestLedgerCheckAccumulationIsReconciled(t *testing.T) {
	c := todoRepo(t, "ledger")
	br := gate.Batch(c, []string{"a", "b"})
	if len(br.Merged) != 2 || len(br.Reconciled) != 1 {
		t.Fatalf("merged=%v reconciled=%v rejected=%v %s", br.Merged, br.Reconciled, br.Rejected, br.Message)
	}
	if got := tu.Git(t, c.Root, "show", "HEAD:baseline"); got != "2" {
		t.Fatalf("baseline not re-recorded: %q", got)
	}
	if msg := tu.Git(t, c.Root, "log", "-1", "--format=%s"); !strings.HasPrefix(msg, "fleet: reconcile todo-baseline") {
		t.Fatalf("reconcile not committed: %q", msg)
	}
}

// The same pair under a strict check is never reconciled: the first branch
// goes in, the second is rejected.
func TestStrictCheckIsNeverReconciled(t *testing.T) {
	c := todoRepo(t, "strict")
	br := gate.Batch(c, []string{"a", "b"})
	if len(br.Reconciled) != 0 || len(br.Merged) != 1 || len(br.Rejected) != 1 {
		t.Fatalf("merged=%v reconciled=%v rejected=%v", br.Merged, br.Reconciled, br.Rejected)
	}
}
