// Package gitx is a thin wrapper around the git binary. Every call returns
// stderr in the error so a failed step never reads as an empty answer.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Repo runs git commands inside one working tree.
type Repo struct {
	Dir string
}

// Error carries the exit code and stderr of a failed git invocation.
type Error struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.Code, strings.TrimSpace(e.Stderr))
}

// Run executes git and returns trimmed stdout.
func (r Repo) Run(args ...string) (string, error) {
	return r.RunContext(context.Background(), args...)
}

// RunContext is Run with a context.
func (r Repo) RunContext(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return "", &Error{Args: args, Code: code, Stderr: errb.String()}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// Rev resolves a revision to a full sha.
func (r Repo) Rev(rev string) (string, error) {
	return r.Run("rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// BranchExists reports whether a local branch exists.
func (r Repo) BranchExists(branch string) (bool, error) {
	_, err := r.Run("show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var ge *Error
	if errors.As(err, &ge) && ge.Code == 1 {
		return false, nil
	}
	return false, err
}

// Ahead counts commits on branch that are not on base.
func (r Repo) Ahead(base, branch string) (int, error) {
	s, err := r.Run("rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// Dirty lists porcelain status lines, untracked files included.
func (r Repo) Dirty() ([]string, error) {
	s, err := r.Run("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// LastCommitTime returns the committer time of rev.
func (r Repo) LastCommitTime(rev string) (time.Time, error) {
	s, err := r.Run("log", "-1", "--format=%ct", rev)
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse commit time %q: %w", s, err)
	}
	return time.Unix(sec, 0), nil
}

// ChangedFiles lists files that differ between base and branch (three-dot).
func (r Repo) ChangedFiles(base, branch string) ([]string, error) {
	s, err := r.Run("diff", "--name-only", base+"..."+branch)
	if err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// CurrentBranch returns the checked-out branch, or "" when HEAD is detached.
func (r Repo) CurrentBranch() (string, error) {
	s, err := r.Run("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && ge.Code == 1 {
			return "", nil
		}
		return "", err
	}
	return s, nil
}
