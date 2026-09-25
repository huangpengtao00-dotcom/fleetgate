package ledger_test

import (
	"os"
	"testing"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/launch"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/ledger"
	tu "github.com/huangpengtao00-dotcom/fleetgate/internal/testutil"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// Invariant 4. The done marker appeared in the log only because the prompt
// was echoed back. Only the last line, carrying this branch's name, counts.
func TestEchoedMarkerIsNotDone(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x", "reports/x.md": "done"})
	log := workspace.For(c, "x").Log
	tu.WriteFile(t, "/", log, "prompt: when finished print WORKER-DONE x\nWORKER-DONE x\nstill thinking...\n")
	r, err := ledger.Derive(c, "x", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Done || r.State == ledger.Harvestable {
		t.Fatalf("echoed marker counted as done: %+v", r)
	}
	tu.WriteFile(t, "/", log, "WORKER-DONE other-branch\n")
	if r, _ := ledger.Derive(c, "x", time.Now()); r.Done {
		t.Fatal("another branch's marker counted")
	}
}

// A report that is still a skeleton is not a report.
func TestSkeletonReportIsNotHarvestable(t *testing.T) {
	c := tu.Repo(t)
	tu.Branch(t, c, "x", map[string]string{"x.txt": "x", "reports/x.md": "# Result\nTODO: fill in\n"})
	r, err := ledger.Derive(c, "x", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.State != ledger.DeadWork || r.Reports != 0 {
		t.Fatalf("skeleton report accepted: %+v", r)
	}
}

// Invariant 5. The ledger stores nothing. A real worker runs, commits, writes
// its report and signs off; a fresh load of the configuration — as a new
// session would do — derives the same harvestable state from git, the pid
// file and the log alone.
func TestLedgerIsRebuiltFromSources(t *testing.T) {
	c := tu.Repo(t)
	script := `
mkdir -p reports
echo "found it" > reports/x.md
echo fix > fix.txt
git add -A && git commit -q -m "fix"
echo "WORKER-DONE $FLEET_BRANCH"
`
	w, err := launch.Launch(c, "x", script, launch.Options{})
	if err != nil {
		t.Fatal(err)
	}
	<-w.Done
	fresh, err := config.Load(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ledger.Build(fresh, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != ledger.Harvestable {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestRunningAndStale(t *testing.T) {
	c := tu.Repo(t)
	w, err := launch.Launch(c, "x", "sleep 5", launch.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p, _ := os.FindProcess(w.Record.PID); _ = p.Kill() }()
	r, err := ledger.Derive(c, "x", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.State != ledger.Running {
		t.Fatalf("want running, got %+v", r)
	}
	r, _ = ledger.Derive(c, "x", time.Now().Add(time.Hour))
	if r.State != ledger.Stale {
		t.Fatalf("want stale an hour later with a clean tree, got %+v", r)
	}
	tu.WriteFile(t, workspace.For(c, "x").Worktree, "wip.txt", "writing")
	r, _ = ledger.Derive(c, "x", time.Now().Add(time.Hour))
	if r.State != ledger.Running {
		t.Fatalf("a dirty tree may be mid-write; want running, got %+v", r)
	}
}
