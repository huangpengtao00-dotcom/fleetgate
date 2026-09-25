// Package ledger derives each worker's state from its sources: git, the pid
// file and the log. Nothing here is stored; the ledger is a projection that
// can be rebuilt at any time, by any session.
package ledger

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gitx"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/liveness"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

// State is one worker's derived status.
type State string

const (
	Running     State = "running"
	Stale       State = "stale"       // alive, clean tree, no commit for stale_after_min
	Harvestable State = "harvestable" // exited, ahead, named done signal, real report
	DeadWork    State = "dead-work"   // exited with commits or a dirty tree but not done
	DeadEmpty   State = "dead-empty"  // exited with nothing to show
	Harvested   State = "harvested"
	Missing     State = "missing" // pid file without branch
)

// Row is one line of the ledger.
type Row struct {
	Branch     string
	State      State
	Alive      bool
	Ahead      int
	Dirty      int
	LastCommit time.Time
	Done       bool
	Reports    int
	Reason     string
}

// Build computes the ledger for every worker in the run directory.
func Build(c *config.Config, now time.Time) ([]Row, error) {
	branches, err := workspace.Workers(c)
	if err != nil {
		return nil, err
	}
	sort.Strings(branches)
	rows := make([]Row, 0, len(branches))
	for _, b := range branches {
		r, err := Derive(c, b, now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", b, err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// Derive computes the state of one worker.
func Derive(c *config.Config, branch string, now time.Time) (Row, error) {
	p := workspace.For(c, branch)
	row := Row{Branch: branch}
	if _, err := os.Stat(p.Harvested); err == nil {
		row.State = Harvested
		return row, nil
	}
	rec, err := liveness.Read(p.PID)
	if err != nil {
		return row, err
	}
	if row.Alive, err = liveness.Alive(rec); err != nil {
		return row, err
	}
	repo := gitx.Repo{Dir: c.Root}
	ok, err := repo.BranchExists(branch)
	if err != nil {
		return row, err
	}
	if !ok {
		row.State, row.Reason = Missing, "pid file without a branch"
		return row, nil
	}
	if row.Ahead, err = repo.Ahead(c.Mainline, branch); err != nil {
		return row, err
	}
	if row.LastCommit, err = repo.LastCommitTime(branch); err != nil {
		return row, err
	}
	if _, err := os.Stat(p.Worktree); err == nil {
		d, err := gitx.Repo{Dir: p.Worktree}.Dirty()
		if err != nil {
			return row, err
		}
		row.Dirty = len(d)
	}

	if row.Alive {
		row.State = Running
		quiet := now.Sub(row.LastCommit)
		if row.Dirty == 0 && quiet > time.Duration(c.StaleAfterMin)*time.Minute {
			row.State = Stale
			row.Reason = fmt.Sprintf("no commit for %s and a clean tree", quiet.Round(time.Minute))
		}
		return row, nil
	}

	if row.Done, err = NamedDone(p.Log, c.DoneMarker, branch); err != nil {
		return row, err
	}
	if row.Reports, row.Reason, err = realReports(c, repo, branch); err != nil {
		return row, err
	}
	switch {
	case row.Ahead > 0 && row.Done && row.Reports > 0 && row.Dirty == 0:
		row.State, row.Reason = Harvestable, ""
	case row.Ahead > 0 || row.Dirty > 0:
		row.State = DeadWork
		if row.Reason == "" {
			row.Reason = notDoneReason(row)
		}
	default:
		row.State = DeadEmpty
	}
	return row, nil
}

func notDoneReason(r Row) string {
	switch {
	case r.Dirty > 0:
		return "uncommitted changes; rescue before relaunch or harvest"
	case !r.Done:
		return "no named done signal as the last line of the log"
	case r.Reports == 0:
		return "no report"
	}
	return ""
}

// NamedDone reports whether the log's last non-empty line is exactly
// "<marker> <branch>". A marker that appears anywhere else — an echoed prompt,
// a quoted instruction — does not count.
func NamedDone(logPath, marker, branch string) (bool, error) {
	f, err := os.Open(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			last = t
		}
	}
	if err := sc.Err(); err != nil {
		return false, err
	}
	return last == marker+" "+branch, nil
}

// realReports counts report files the branch adds or changes, and rejects any
// that still contain a placeholder: a skeleton report is not a report.
func realReports(c *config.Config, repo gitx.Repo, branch string) (int, string, error) {
	files, err := repo.ChangedFiles(c.Mainline, branch)
	if err != nil {
		return 0, "", err
	}
	prefix := strings.TrimSuffix(c.ReportDir, "/") + "/"
	n := 0
	for _, f := range files {
		if !strings.HasPrefix(f, prefix) {
			continue
		}
		body, err := repo.Run("show", branch+":"+f)
		if err != nil {
			continue // deleted on the branch
		}
		for _, pat := range c.PlaceholderPatterns {
			if pat != "" && strings.Contains(body, pat) {
				return 0, fmt.Sprintf("report %s still contains %q", filepath.ToSlash(f), pat), nil
			}
		}
		n++
	}
	return n, "", nil
}
