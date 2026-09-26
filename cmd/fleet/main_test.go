package main

import (
	"os"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/testutil"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// A pid file that cannot be read belongs to a worker whose state is unknown.
// Counting it as not running would let launch start more than max_workers.
func TestUnreadablePidFileIsNotCountedAsIdle(t *testing.T) {
	c := testutil.Repo(t)
	if err := workspace.EnsureRunDir(c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace.For(c, "half-written").PID, []byte("{\"pid\": 12"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := countRunning(c)
	if err == nil {
		t.Fatalf("counted %d running workers from a corrupt pid file without an error", n)
	}
}
