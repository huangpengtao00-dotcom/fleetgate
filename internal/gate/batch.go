package gate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gitx"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/lock"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// BatchResult describes a batch harvest.
type BatchResult struct {
	Merged     []string
	Rejected   map[string]string // branch -> reason
	Reconciled []string          // ledger checks whose baseline was re-recorded
	Previews   int               // how many merged-tree check runs it took
	Code       int
	Message    string
}

// Batch harvests many branches with as few check runs as possible: the whole
// set is merged into one preview and checked once. A green set is merged in
// one go. A red set is split in half and each half retried, so one bad branch
// costs about log2(n) extra runs instead of n.
//
// One red is not bisected: a ledger check (a count compared to a recorded
// baseline) that only goes red for the combined set, while every branch is
// green on it alone. Each branch kept under the baseline; together they
// crossed it. That is accumulation, not a bad branch, so the set is merged
// and the check's reconcile command re-records the baseline. Strict checks
// never take this path.
func Batch(c *config.Config, branches []string) BatchResult {
	br := BatchResult{Rejected: map[string]string{}}
	if err := workspace.EnsureRunDir(c); err != nil {
		br.Code, br.Message = CodeError, err.Error()
		return br
	}
	l, err := lock.Acquire(c.RunPath("merge.lock"))
	if errors.Is(err, lock.ErrBusy) {
		br.Code, br.Message = CodeBusy, err.Error()
		return br
	}
	if err != nil {
		br.Code, br.Message = CodeError, err.Error()
		return br
	}
	defer l.Release()

	root := gitx.Repo{Dir: c.Root}
	if code, err := precheckMain(c, root); err != nil {
		br.Code, br.Message = code, err.Error()
		return br
	}
	var set []string
	for _, b := range branches {
		if code, err := precheckWorker(c, b); err != nil {
			br.Rejected[b] = fmt.Sprintf("precheck (%d): %v", code, err)
			continue
		}
		set = append(set, b)
	}
	if err := br.try(c, root, set); err != nil {
		if br.Code == 0 {
			br.Code = CodeError
		}
		br.Message = err.Error()
		return br
	}
	switch {
	case br.Code != 0:
	case len(br.Rejected) > 0:
		br.Code = CodePreviewRed
		br.Message = fmt.Sprintf("merged %d, rejected %d", len(br.Merged), len(br.Rejected))
	default:
		br.Message = fmt.Sprintf("merged %d", len(br.Merged))
	}
	return br
}

func (br *BatchResult) try(c *config.Config, root gitx.Repo, set []string) error {
	if len(set) == 0 {
		return nil
	}
	base, err := root.Rev("HEAD")
	if err != nil {
		return err
	}
	pv, err := preview(c, base, set)
	if err != nil {
		return err
	}
	for b, files := range pv.conflicts {
		br.Rejected[b] = "conflict: " + strings.Join(files, ", ")
	}
	set = pv.merged
	if len(set) == 0 {
		return nil
	}
	br.Previews++
	s := summarize(pv.checks)
	if len(s.infra) > 0 {
		br.Code = CodeInfra
		return fmt.Errorf("checks could not run (%s); stopping the batch without judging any branch", names(s.infra))
	}
	if len(s.failed) == 0 {
		return br.mergeSet(c, root, set, nil)
	}
	if len(set) == 1 {
		br.Rejected[set[0]] = "red on merged preview: " + names(s.failed)
		return nil
	}
	if s.onlyLedger {
		accumulated, err := eachGreenAlone(c, base, set, subset(c.Checks, s.failed))
		if err != nil {
			return err
		}
		br.Previews += len(set)
		if accumulated {
			return br.mergeSet(c, root, set, subset(c.Checks, s.failed))
		}
	}
	mid := len(set) / 2
	if err := br.try(c, root, set[:mid]); err != nil {
		return err
	}
	return br.try(c, root, set[mid:])
}

func eachGreenAlone(c *config.Config, base string, set []string, checks []config.Check) (bool, error) {
	for _, b := range set {
		pv, err := preview(c, base, []string{b}, checks...)
		if err != nil {
			return false, err
		}
		if len(pv.conflicts) > 0 || !summarize(pv.checks).green() {
			return false, nil
		}
	}
	return true, nil
}

func (br *BatchResult) mergeSet(c *config.Config, root gitx.Repo, set []string, reconcile []config.Check) error {
	shas := map[string]string{}
	for _, b := range set {
		sha, err := mergeInto(root, b)
		if err != nil {
			return fmt.Errorf("merge %s after a green preview: %w", b, err)
		}
		shas[b] = sha
	}
	for _, ch := range reconcile {
		if code, out, _ := execCheck(config.Check{Name: ch.Name, Run: ch.Reconcile, TimeoutSec: ch.TimeoutSec}, c.Root); code != 0 {
			br.Code = CodePostMergeRed
			return fmt.Errorf("reconcile for %s failed (exit %d): %s", ch.Name, code, tail(out, 5))
		}
		if d, err := root.Dirty(); err != nil {
			return err
		} else if len(d) > 0 {
			if _, err := root.Run("add", "-A"); err != nil {
				return err
			}
			msg := fmt.Sprintf("fleet: reconcile %s baseline after batch %s", ch.Name, strings.Join(set, " "))
			if _, err := root.Run("commit", "-q", "--no-verify", "-m", msg); err != nil {
				return err
			}
		}
		br.Reconciled = append(br.Reconciled, ch.Name)
	}
	if ps := summarize(runAll("mainline", c.Checks, c.Root)); !ps.green() {
		br.Code = CodePostMergeRed
		return fmt.Errorf("merged %s but red on the mainline afterwards: %s; worktrees kept", strings.Join(set, ", "), names(append(ps.failed, ps.infra...)))
	}
	for _, b := range set {
		if err := finish(c, b, shas[b]); err != nil {
			return err
		}
		br.Merged = append(br.Merged, b)
	}
	return nil
}
