package gate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gate"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/lock"
	tu "github.com/huangpengtao00-dotcom/fleetgate/internal/testutil"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

func strict(name, run string) config.Check {
	return config.Check{Name: name, Run: run, Kind: "strict", TimeoutSec: 30}
}

// Invariant 2. Branch a and branch b are each green; together they are red.
// Judging each branch alone would let both in. The gate judges the merged
// tree, so the second harvest is refused and the mainline does not move.
func TestCrossRedIsCaughtBeforeTheMainline(t *testing.T) {
	c := tu.Repo(t, strict("not-both", `! { [ -f a.flag ] && [ -f b.flag ]; }`))
	tu.Branch(t, c, "a", map[string]string{"a.flag": "", "reports/a.md": "done"})
	tu.Branch(t, c, "b", map[string]string{"b.flag": "", "reports/b.md": "done"})

	if r := gate.Harvest(c, "a"); r.Code != gate.CodePass {
		t.Fatalf("harvest a: %d %s", r.Code, r.Message)
	}
	before := tu.Head(t, c)
	r := gate.Harvest(c, "b")
	if r.Code != gate.CodePreviewRed {
		t.Fatalf("harvest b: want preview red, got %d %s", r.Code, r.Message)
	}
	if r.Attribution != "cross" {
		t.Fatalf("attribution: want cross (green alone), got %q", r.Attribution)
	}
	if after := tu.Head(t, c); after != before {
		t.Fatal("mainline moved on a red preview")
	}
}

func TestRedOnItsOwnIsAttributedToTheBranch(t *testing.T) {
	c := tu.Repo(t, strict("no-bad", `[ ! -f BAD ]`))
	tu.Branch(t, c, "x", map[string]string{"BAD": "", "reports/x.md": "done"})
	r := gate.Harvest(c, "x")
	if r.Code != gate.CodePreviewRed || r.Attribution != "self" {
		t.Fatalf("got %d %q %s", r.Code, r.Attribution, r.Message)
	}
}

// Invariants 1 and 9: the mainline has one entrance and the gate owns it
// while running. A dirty mainline tree or a held lock means no merge.
func TestDirtyMainlineIsRefused(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	tu.WriteFile(t, c.Root, "note.txt", "someone wrote here mid-harvest")
	before := tu.Head(t, c)
	if r := gate.Harvest(c, "x"); r.Code != gate.CodeMainDirty {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
	if tu.Head(t, c) != before {
		t.Fatal("merged into a dirty mainline")
	}
}

func TestConcurrentHarvestIsRefused(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	if err := workspace.EnsureRunDir(c); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(c.RunPath("merge.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if r := gate.Harvest(c, "x"); r.Code != gate.CodeBusy {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
}

func TestConflictLeavesTheMainlineUntouched(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "a", map[string]string{"README.md": "from a\n"})
	tu.Branch(t, c, "b", map[string]string{"README.md": "from b\n"})
	if r := gate.Harvest(c, "a"); r.Code != gate.CodePass {
		t.Fatal(r.Message)
	}
	before := tu.Head(t, c)
	r := gate.Harvest(c, "b")
	if r.Code != gate.CodeConflict || !strings.Contains(r.Message, "README.md") {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
	if tu.Head(t, c) != before {
		t.Fatal("mainline moved on a conflict")
	}
}

// Invariant 6: failures split three ways. A failure that passes on retry is
// flaky — merged, and reported. A timeout or a declared infrastructure exit
// says nothing about the branch — not merged, and not blamed on it.
func TestFlakyCheckIsMergedAndReported(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "n")
	ch := strict("flaky", `n=$(cat `+counter+` 2>/dev/null || echo 0); echo $((n+1)) > `+counter+`; [ "$n" -ge 1 ]`)
	ch.RetryOnFail = 1
	c := tu.Repo(t, ch)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	r := gate.Harvest(c, "x")
	if r.Code != gate.CodePass || !strings.Contains(r.Message, "flaky") {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
}

func TestInfrastructureFailureIsNotBlamedOnTheBranch(t *testing.T) {
	ch := strict("needs-db", `echo "connection refused" >&2; exit 75`)
	ch.InfraExitCodes = []int{75}
	c := tu.Repo(t, ch)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	before := tu.Head(t, c)
	r := gate.Harvest(c, "x")
	if r.Code != gate.CodeInfra {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
	if tu.Head(t, c) != before {
		t.Fatal("merged on an infrastructure failure")
	}
}

func TestTimeoutIsInfrastructure(t *testing.T) {
	ch := strict("slow", `sleep 5`)
	ch.TimeoutSec = 1
	c := tu.Repo(t, ch)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	if r := gate.Harvest(c, "x"); r.Code != gate.CodeInfra {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
}

// The second belt: checks run again on the mainline after the real merge.
// The preview is detached, the mainline is on "main"; this check can only
// tell them apart, so it passes the preview and fails afterwards.
func TestRedAfterTheMergeKeepsTheWorktree(t *testing.T) {
	c := tu.Repo(t, strict("detached-only", `[ "$(git rev-parse --abbrev-ref HEAD)" = HEAD ]`))
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	r := gate.Harvest(c, "x")
	if r.Code != gate.CodePostMergeRed {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
	if _, err := os.Stat(workspace.For(c, "x").Worktree); err != nil {
		t.Fatal("worktree removed after a post-merge red")
	}
}

func TestDirtyWorkerIsRefused(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x"})
	tu.WriteFile(t, workspace.For(c, "x").Worktree, "leftover.txt", "not committed")
	if r := gate.Harvest(c, "x"); r.Code != gate.CodeWorkerDirty {
		t.Fatalf("got %d %s", r.Code, r.Message)
	}
}

func TestPassCleansUp(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "feat/one", map[string]string{"one.txt": "1"})
	r := gate.Harvest(c, "feat/one")
	if r.Code != gate.CodePass {
		t.Fatal(r.Message)
	}
	if _, err := os.Stat(workspace.For(c, "feat/one").Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree left behind")
	}
	if out := tu.Git(t, c.Root, "branch", "--list", "feat/one"); out != "" {
		t.Fatal("branch left behind")
	}
	if _, err := os.Stat(filepath.Join(c.Root, "one.txt")); err != nil {
		t.Fatal("work did not reach the mainline")
	}
}
