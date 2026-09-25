// Package launch starts one worker in its own worktree.
package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/liveness"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// ErrPrompt means the prompt would waste a worker: empty or still templated.
var ErrPrompt = errors.New("prompt rejected")

// ErrCapacity means max_workers are already running.
var ErrCapacity = errors.New("worker capacity reached")

// ErrExitedAtStart means the worker died during the startup grace period.
var ErrExitedAtStart = errors.New("worker exited during startup")

var placeholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

// ValidatePrompt rejects prompts that would burn a worker for nothing.
func ValidatePrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("%w: empty", ErrPrompt)
	}
	if m := placeholder.FindString(prompt); m != "" {
		return fmt.Errorf("%w: unfilled placeholder %s", ErrPrompt, m)
	}
	return nil
}

// Options tune a launch.
type Options struct {
	// Grace is how long to wait before confirming the worker is still alive.
	// Zero skips the check.
	Grace time.Duration
	// Running is the number of workers currently alive, for the capacity gate.
	Running int
}

// Worker is a launched process. Done closes when it exits, when launched
// from a process that stays around (tests); the CLI simply exits.
type Worker struct {
	Record liveness.Record
	Paths  workspace.Paths
	Done   <-chan struct{}
}

// Launch validates the prompt, creates the worktree and starts the agent.
func Launch(c *config.Config, branch, prompt string, opt Options) (*Worker, error) {
	if err := ValidatePrompt(prompt); err != nil {
		return nil, err
	}
	if opt.Running >= c.MaxWorkers {
		return nil, fmt.Errorf("%w: %d running, max_workers=%d", ErrCapacity, opt.Running, c.MaxWorkers)
	}
	p, err := workspace.Create(c, branch)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p.Prompt, []byte(prompt), 0o644); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()

	cmd := exec.Command(c.Agent[0], c.Agent[1:]...)
	cmd.Dir = p.Worktree
	cmd.Env = append(os.Environ(), "FLEET_PROMPT="+p.Prompt, "FLEET_BRANCH="+branch)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent: %w", err)
	}
	// Record the start time before reaping: until Wait runs, even a worker
	// that already exited stays visible to ps as a zombie.
	rec, err := liveness.Write(p.PID, branch, cmd.Process.Pid)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	if err != nil {
		return nil, err
	}
	w := &Worker{Record: rec, Paths: p, Done: done}
	if opt.Grace > 0 {
		select {
		case <-done:
			return w, fmt.Errorf("%w: see %s", ErrExitedAtStart, p.Log)
		case <-time.After(opt.Grace):
		}
	}
	return w, nil
}
