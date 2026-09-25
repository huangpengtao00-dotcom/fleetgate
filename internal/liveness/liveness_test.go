package liveness_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/liveness"
)

// Invariant 3. A pid is not an identity: the recorded pid is alive, but the
// start time says it is not the process we launched.
func TestReusedPidIsNotAlive(t *testing.T) {
	start, err := liveness.StartTime(os.Getpid())
	if err != nil || start == "" {
		t.Fatalf("start time: %q %v", start, err)
	}
	same := liveness.Record{PID: os.Getpid(), Start: start}
	if ok, err := liveness.Alive(same); err != nil || !ok {
		t.Fatalf("our own process should be alive: %v %v", ok, err)
	}
	reused := liveness.Record{PID: os.Getpid(), Start: "Thu Jan  1 00:00:00 1970"}
	if ok, _ := liveness.Alive(reused); ok {
		t.Fatal("a reused pid was taken for the original worker")
	}
}

// A process whose command line mentions the branch is not the worker. The
// check never looks at names, so a look-alike cannot keep a dead worker alive.
func TestLookAlikeProcessDoesNotKeepAWorkerAlive(t *testing.T) {
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	look := exec.Command("sh", "-c", "sleep 3 # feature-x worker")
	if err := look.Start(); err != nil {
		t.Fatal(err)
	}
	defer look.Process.Kill()
	path := filepath.Join(t.TempDir(), "feature-x.pid")
	if err := os.WriteFile(path, []byte(`{"pid":`+itoa(dead.Process.Pid)+`,"start":"x","branch":"feature-x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := liveness.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := liveness.Alive(rec); ok {
		t.Fatal("dead worker reported alive")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
