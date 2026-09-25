// Package workspace owns worker worktrees and the run directory layout.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gitx"
)

// Paths are the files that belong to one worker.
type Paths struct {
	Worktree  string
	PID       string
	Log       string
	Prompt    string
	Harvested string
}

// SafeName maps a branch name to a file name.
func SafeName(branch string) string {
	return strings.ReplaceAll(branch, "/", "__")
}

// BranchFromSafe reverses SafeName.
func BranchFromSafe(name string) string {
	return strings.ReplaceAll(name, "__", "/")
}

// For returns the paths for a branch.
func For(c *config.Config, branch string) Paths {
	s := SafeName(branch)
	return Paths{
		Worktree:  c.RunPath("worktrees", s),
		PID:       c.RunPath(s + ".pid"),
		Log:       c.RunPath(s + ".log"),
		Prompt:    c.RunPath(s + ".prompt"),
		Harvested: c.RunPath(s + ".harvested"),
	}
}

// EnsureRunDir creates the run directory and makes git ignore it, so runtime
// state never leaks into commits or into other workers' checkouts.
func EnsureRunDir(c *config.Config) error {
	if err := os.MkdirAll(c.RunPath("worktrees"), 0o755); err != nil {
		return err
	}
	gi := c.RunPath(".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	return nil
}

// ErrExists means the branch or its worktree is already there.
var ErrExists = errors.New("worker already exists")

// Create makes a new branch and worktree from the mainline ref — never from
// whatever happens to be checked out, which may be dirty or behind.
func Create(c *config.Config, branch string) (Paths, error) {
	if err := EnsureRunDir(c); err != nil {
		return Paths{}, err
	}
	p := For(c, branch)
	repo := gitx.Repo{Dir: c.Root}
	exists, err := repo.BranchExists(branch)
	if err != nil {
		return Paths{}, err
	}
	_, statErr := os.Stat(p.Worktree)
	dirExists := statErr == nil
	switch {
	case exists && !dirExists:
		return Paths{}, fmt.Errorf("%w: branch %s exists but its worktree is missing; reattach or delete the branch", ErrExists, branch)
	case !exists && dirExists:
		return Paths{}, fmt.Errorf("%w: %s exists without a branch; remove the leftover directory", ErrExists, p.Worktree)
	case exists && dirExists:
		return Paths{}, fmt.Errorf("%w: %s", ErrExists, branch)
	}
	if _, err := repo.Run("worktree", "add", "-q", "-b", branch, p.Worktree, c.Mainline); err != nil {
		return Paths{}, err
	}
	return p, nil
}

// Rescue commits a dead worker's uncommitted changes onto its own branch so a
// relaunch can continue from them. It is not a harvest: nothing reaches the
// mainline here.
func Rescue(c *config.Config, branch string) (bool, error) {
	p := For(c, branch)
	wt := gitx.Repo{Dir: p.Worktree}
	dirty, err := wt.Dirty()
	if err != nil {
		return false, err
	}
	if len(dirty) == 0 {
		return false, nil
	}
	if _, err := wt.Run("add", "-A"); err != nil {
		return false, err
	}
	if _, err := wt.Run("commit", "-q", "--no-verify", "-m", "fleet: rescue uncommitted work of "+branch); err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes the worktree and the branch after a harvest.
func Remove(c *config.Config, branch string) error {
	p := For(c, branch)
	repo := gitx.Repo{Dir: c.Root}
	if _, err := os.Stat(p.Worktree); err == nil {
		if _, err := repo.Run("worktree", "remove", p.Worktree); err != nil {
			return err
		}
	}
	if _, err := repo.Run("branch", "-d", branch); err != nil {
		return err
	}
	return nil
}

// Workers lists branches that have a pid file in the run directory.
func Workers(c *config.Config) ([]string, error) {
	entries, err := os.ReadDir(c.RunPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pid") {
			out = append(out, BranchFromSafe(strings.TrimSuffix(e.Name(), ".pid")))
		}
	}
	return out, nil
}

// RelWorktree is the worktree path relative to the root, for display.
func RelWorktree(c *config.Config, branch string) string {
	rel, err := filepath.Rel(c.Root, For(c, branch).Worktree)
	if err != nil {
		return For(c, branch).Worktree
	}
	return rel
}
