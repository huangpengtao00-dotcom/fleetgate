// Package testutil builds throwaway repositories for the incident-replay
// tests. The fake agent executes its prompt file as a shell script, so each
// test scripts exactly what its worker does.
package testutil

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/liveness"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Repo creates a repository with one commit on main and a fleet.json that
// uses the given checks.
func Repo(t testing.TB, checks ...config.Check) *config.Config {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "commit.gpgsign", "false")
	if len(checks) == 0 {
		checks = []config.Check{{Name: "true", Run: "true", Kind: "strict", TimeoutSec: 30}}
	}
	c := config.Config{
		Mainline:            "main",
		RunDir:              ".fleet",
		Agent:               []string{"sh", "-c", `sh "$FLEET_PROMPT"`},
		Checks:              checks,
		MaxWorkers:          4,
		StaleAfterMin:       30,
		DoneMarker:          "WORKER-DONE",
		ReportDir:           "reports",
		PlaceholderPatterns: []string{"TODO: fill in"},
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	WriteFile(t, dir, config.FileName, string(b))
	WriteFile(t, dir, "README.md", "fixture\n")
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "-m", "init")
	loaded, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// WriteFile writes rel under dir, creating parents.
func WriteFile(t testing.TB, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Branch creates a finished worker by hand: a worktree with the given files
// committed, a done line in its log, and a pid file pointing at a process
// that no longer exists.
func Branch(t testing.TB, c *config.Config, branch string, files map[string]string) {
	t.Helper()
	p, err := workspace.Create(c, branch)
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		WriteFile(t, p.Worktree, rel, body)
	}
	if len(files) > 0 {
		Git(t, p.Worktree, "add", "-A")
		Git(t, p.Worktree, "commit", "-q", "-m", "work on "+branch)
	}
	WriteFile(t, filepath.Dir(p.Log), filepath.Base(p.Log), "working\n"+c.DoneMarker+" "+branch+"\n")
	DeadPID(t, p.PID, branch)
}

// DeadPID writes a pid file for a process that has exited.
func DeadPID(t testing.TB, path, branch string) {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	rec := liveness.Record{PID: cmd.Process.Pid, Start: "Thu Jan  1 00:00:00 1970", Branch: branch}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Head returns the mainline sha.
func Head(t testing.TB, c *config.Config) string {
	t.Helper()
	return Git(t, c.Root, "rev-parse", "HEAD")
}
