// Package gate is the only way worker branches reach the mainline.
//
// Every harvest first materialises "mainline + branch" in a throwaway
// worktree and runs the checks there. Two branches can each be green and only
// go red once combined; checking the branch alone cannot see that, so the
// merged tree is what gets judged. Only a green preview is merged for real,
// and the checks run once more on the mainline afterwards.
package gate

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gitx"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/liveness"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/lock"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// Exit codes. They are the CLI's exit status too.
const (
	CodePass         = 0  // merged, checks green, worktree and branch removed
	CodeError        = 1  // unexpected failure; nothing merged
	CodeMainDirty    = 2  // mainline tree dirty or not on the mainline branch
	CodeConflict     = 3  // merge conflict in the preview; mainline untouched
	CodePostMergeRed = 5  // merged, then red on the mainline; NOT cleaned up
	CodePreviewRed   = 6  // red on the merged preview; not merged
	CodeBusy         = 7  // another harvest holds the merge lock
	CodeWorkerAlive  = 8  // the worker is still running
	CodeInfra        = 9  // checks could not run meaningfully; not merged, not the branch's fault
	CodeWorkerDirty  = 10 // the worker left uncommitted changes; rescue first
	CodeNotBranch    = 11 // no such branch
)

// Result describes one harvest.
type Result struct {
	Branch      string
	Code        int
	Message     string
	Checks      []CheckResult
	Attribution string // for preview reds: "self" or "cross"
	MergeSHA    string
}

// Harvest merges one branch through the gate.
func Harvest(c *config.Config, branch string) Result {
	res := Result{Branch: branch}
	if err := workspace.EnsureRunDir(c); err != nil {
		return fail(res, CodeError, err)
	}
	l, err := lock.Acquire(c.RunPath("merge.lock"))
	if errors.Is(err, lock.ErrBusy) {
		return fail(res, CodeBusy, err)
	}
	if err != nil {
		return fail(res, CodeError, err)
	}
	defer l.Release()

	if code, err := precheckWorker(c, branch); err != nil {
		return fail(res, code, err)
	}
	root := gitx.Repo{Dir: c.Root}
	if code, err := precheckMain(c, root); err != nil {
		return fail(res, code, err)
	}
	base, err := root.Rev("HEAD")
	if err != nil {
		return fail(res, CodeError, err)
	}

	pv, err := preview(c, base, []string{branch})
	if err != nil {
		return fail(res, CodeError, err)
	}
	res.Checks = pv.checks
	if len(pv.conflicts) > 0 {
		res.Code = CodeConflict
		res.Message = "merge conflict in " + strings.Join(pv.conflicts[branch], ", ") + "; mainline untouched"
		return res
	}
	s := summarize(pv.checks)
	if len(s.infra) > 0 && len(s.failed) == 0 {
		res.Code = CodeInfra
		res.Message = "checks could not run (" + names(s.infra) + "); says nothing about the branch; not merged"
		return res
	}
	if len(s.failed) > 0 {
		res.Code = CodePreviewRed
		res.Attribution, err = attribute(c, branch, subset(c.Checks, s.failed))
		if err != nil {
			return fail(res, CodeError, err)
		}
		res.Message = fmt.Sprintf("red on mainline+%s: %s (%s); not merged", branch, names(s.failed), res.Attribution)
		return res
	}

	now, err := root.Rev("HEAD")
	if err != nil {
		return fail(res, CodeError, err)
	}
	if now != base {
		return fail(res, CodeError, fmt.Errorf("mainline moved during the harvest (%s -> %s)", short(base), short(now)))
	}
	sha, err := mergeInto(root, branch)
	if err != nil {
		return fail(res, CodeError, err)
	}
	res.MergeSHA = sha
	post := runAll("mainline", c.Checks, c.Root)
	res.Checks = append(res.Checks, post...)
	if ps := summarize(post); !ps.green() {
		res.Code = CodePostMergeRed
		res.Message = "merged as " + short(sha) + " but red on the mainline afterwards: " + names(append(ps.failed, ps.infra...)) + "; worktree kept, investigate before pushing"
		return res
	}
	if err := finish(c, branch, sha); err != nil {
		return fail(res, CodeError, err)
	}
	res.Code = CodePass
	res.Message = "merged as " + short(sha)
	if len(s.flaky) > 0 {
		res.Message += "; flaky on first run: " + names(s.flaky)
	}
	return res
}

func fail(r Result, code int, err error) Result {
	r.Code, r.Message = code, err.Error()
	return r
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func precheckWorker(c *config.Config, branch string) (int, error) {
	root := gitx.Repo{Dir: c.Root}
	ok, err := root.BranchExists(branch)
	if err != nil {
		return CodeError, err
	}
	if !ok {
		return CodeNotBranch, fmt.Errorf("no branch %s", branch)
	}
	p := workspace.For(c, branch)
	if rec, err := liveness.Read(p.PID); err == nil {
		alive, err := liveness.Alive(rec)
		if err != nil {
			return CodeError, err
		}
		if alive {
			return CodeWorkerAlive, fmt.Errorf("worker %s is still running (pid %d)", branch, rec.PID)
		}
	}
	if _, err := os.Stat(p.Worktree); err == nil {
		d, err := gitx.Repo{Dir: p.Worktree}.Dirty()
		if err != nil {
			return CodeError, err
		}
		if len(d) > 0 {
			return CodeWorkerDirty, fmt.Errorf("worker %s has %d uncommitted changes; run `fleet rescue %s` first", branch, len(d), branch)
		}
	}
	return 0, nil
}

func precheckMain(c *config.Config, root gitx.Repo) (int, error) {
	cur, err := root.CurrentBranch()
	if err != nil {
		return CodeError, err
	}
	if cur != c.Mainline {
		return CodeMainDirty, fmt.Errorf("root worktree is on %q, not %q", cur, c.Mainline)
	}
	d, err := root.Dirty()
	if err != nil {
		return CodeError, err
	}
	if len(d) > 0 {
		return CodeMainDirty, fmt.Errorf("mainline worktree is dirty (%d paths); refusing to merge", len(d))
	}
	return 0, nil
}

type previewResult struct {
	checks    []CheckResult
	conflicts map[string][]string // branch -> conflicting paths
	merged    []string
}

// preview merges branches onto base in a detached throwaway worktree and runs
// the given checks there (all configured checks when none are given).
// Conflicting branches are skipped and reported.
func preview(c *config.Config, base string, branches []string, checks ...config.Check) (previewResult, error) {
	pr := previewResult{conflicts: map[string][]string{}}
	dir := c.RunPath("preview-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	root := gitx.Repo{Dir: c.Root}
	if _, err := root.Run("worktree", "add", "-q", "--detach", dir, base); err != nil {
		return pr, err
	}
	defer func() {
		_, _ = root.Run("worktree", "remove", "--force", dir)
		_, _ = root.Run("worktree", "prune")
	}()
	tree := gitx.Repo{Dir: dir}
	for _, b := range branches {
		if _, err := tree.Run("merge", "-q", "--no-ff", "--no-edit", "-m", "fleet: preview "+b, b); err != nil {
			files, _ := tree.Run("diff", "--name-only", "--diff-filter=U")
			if files == "" {
				return pr, err
			}
			pr.conflicts[b] = strings.Split(files, "\n")
			if _, err := tree.Run("merge", "--abort"); err != nil {
				return pr, err
			}
			continue
		}
		pr.merged = append(pr.merged, b)
	}
	if len(pr.merged) == 0 {
		return pr, nil
	}
	if len(checks) == 0 {
		checks = c.Checks
	}
	pr.checks = runAll("preview", checks, dir)
	return pr, nil
}

// attribute tells a branch that is red on its own ("self") from one that is
// green alone and only red in combination with the current mainline ("cross").
func attribute(c *config.Config, branch string, failing []config.Check) (string, error) {
	tip, err := gitx.Repo{Dir: c.Root}.Rev(branch)
	if err != nil {
		return "", err
	}
	dir := c.RunPath("attr-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	root := gitx.Repo{Dir: c.Root}
	if _, err := root.Run("worktree", "add", "-q", "--detach", dir, tip); err != nil {
		return "", err
	}
	defer func() {
		_, _ = root.Run("worktree", "remove", "--force", dir)
		_, _ = root.Run("worktree", "prune")
	}()
	if s := summarize(runAll("branch-alone", failing, dir)); len(s.failed) == 0 {
		return "cross", nil
	}
	return "self", nil
}

func mergeInto(root gitx.Repo, branch string) (string, error) {
	if _, err := root.Run("merge", "-q", "--no-ff", "--no-edit", "-m", "fleet: harvest "+branch, branch); err != nil {
		_, _ = root.Run("merge", "--abort")
		return "", err
	}
	return root.Rev("HEAD")
}

func finish(c *config.Config, branch, sha string) error {
	if err := workspace.Remove(c, branch); err != nil {
		return err
	}
	return os.WriteFile(workspace.For(c, branch).Harvested, []byte(sha+"\n"), 0o644)
}
