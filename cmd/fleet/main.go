// Command fleet runs many coding agents against one git repository and lets
// their work reach the mainline only through a merged-tree gate.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gate"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/gitx"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/launch"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/ledger"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/liveness"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/workspace"
)

const usage = `fleet — run coding agents in parallel; merge only through the gate

  fleet init                          write an example fleet.json
  fleet launch <branch> <prompt-file> start one worker in its own worktree
  fleet ledger [--json]               derive every worker's state
  fleet rescue <branch>               commit a dead worker's leftovers to its branch
  fleet harvest <branch>              merge one branch through the gate
  fleet batch [branch...]             merge many (default: all harvestable), bisecting reds
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	code, err := run(os.Args[1], os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fleet:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func root() (string, error) {
	// The common git dir is shared by every worktree, so this resolves to the
	// main checkout even when run from inside a worker's worktree.
	s, err := gitx.Repo{}.Run("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Dir(s), nil
}

func run(cmd string, args []string) (int, error) {
	r, err := root()
	if err != nil {
		return 1, err
	}
	if cmd == "init" {
		path := filepath.Join(r, config.FileName)
		if _, err := os.Stat(path); err == nil {
			return 1, fmt.Errorf("%s already exists", path)
		}
		return 0, os.WriteFile(path, []byte(config.Example), 0o644)
	}
	c, err := config.Load(r)
	if err != nil {
		return 1, err
	}
	switch cmd {
	case "launch":
		fs := flag.NewFlagSet("launch", flag.ContinueOnError)
		grace := fs.Duration("grace", 3*time.Second, "confirm the worker is alive after this long")
		if err := fs.Parse(args); err != nil {
			return 2, err
		}
		if fs.NArg() != 2 {
			return 2, errors.New("usage: fleet launch <branch> <prompt-file>")
		}
		prompt, err := os.ReadFile(fs.Arg(1))
		if err != nil {
			return 1, err
		}
		running, err := countRunning(c)
		if err != nil {
			return 1, err
		}
		w, err := launch.Launch(c, fs.Arg(0), string(prompt), launch.Options{Grace: *grace, Running: running})
		if err != nil {
			return 1, err
		}
		fmt.Printf("launched %s pid=%d worktree=%s\n", fs.Arg(0), w.Record.PID, workspace.RelWorktree(c, fs.Arg(0)))
		return 0, nil

	case "ledger":
		fs := flag.NewFlagSet("ledger", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "machine-readable output")
		if err := fs.Parse(args); err != nil {
			return 2, err
		}
		rows, err := ledger.Build(c, time.Now())
		if err != nil {
			return 1, err
		}
		if *asJSON {
			return 0, json.NewEncoder(os.Stdout).Encode(rows)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "BRANCH\tSTATE\tAHEAD\tDIRTY\tLAST COMMIT\tNOTE")
		for _, row := range rows {
			last := "-"
			if !row.LastCommit.IsZero() {
				last = time.Since(row.LastCommit).Round(time.Minute).String() + " ago"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", row.Branch, row.State, row.Ahead, row.Dirty, last, row.Reason)
		}
		return 0, tw.Flush()

	case "rescue":
		if len(args) != 1 {
			return 2, errors.New("usage: fleet rescue <branch>")
		}
		rec, err := liveness.Read(workspace.For(c, args[0]).PID)
		if err != nil {
			return 1, err
		}
		if alive, err := liveness.Alive(rec); err != nil {
			return 1, err
		} else if alive {
			return gate.CodeWorkerAlive, fmt.Errorf("%s is still running; never rescue a live worker", args[0])
		}
		did, err := workspace.Rescue(c, args[0])
		if err != nil {
			return 1, err
		}
		fmt.Println(map[bool]string{true: "rescued", false: "nothing to rescue"}[did])
		return 0, nil

	case "harvest":
		if len(args) != 1 {
			return 2, errors.New("usage: fleet harvest <branch>")
		}
		res := gate.Harvest(c, args[0])
		printChecks(res.Checks)
		fmt.Printf("%s: %s\n", label(res.Code), res.Message)
		return res.Code, nil

	case "batch":
		branches := args
		if len(branches) == 0 {
			rows, err := ledger.Build(c, time.Now())
			if err != nil {
				return 1, err
			}
			for _, row := range rows {
				if row.State == ledger.Harvestable {
					branches = append(branches, row.Branch)
				}
			}
		}
		if len(branches) == 0 {
			fmt.Println("nothing harvestable")
			return 0, nil
		}
		br := gate.Batch(c, branches)
		for _, b := range br.Merged {
			fmt.Println("merged   ", b)
		}
		for b, why := range br.Rejected {
			fmt.Println("rejected ", b, "—", why)
		}
		if len(br.Reconciled) > 0 {
			fmt.Println("reconciled baselines:", strings.Join(br.Reconciled, ", "))
		}
		fmt.Printf("%s: %s (%d preview runs)\n", label(br.Code), br.Message, br.Previews)
		return br.Code, nil
	}
	fmt.Fprint(os.Stderr, usage)
	return 2, fmt.Errorf("unknown command %q", cmd)
}

func countRunning(c *config.Config) (int, error) {
	branches, err := workspace.Workers(c)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range branches {
		rec, err := liveness.Read(workspace.For(c, b).PID)
		if err != nil {
			continue
		}
		if ok, _ := liveness.Alive(rec); ok {
			n++
		}
	}
	return n, nil
}

func printChecks(rs []gate.CheckResult) {
	for _, r := range rs {
		fmt.Printf("  %-8s %-6s %s", r.Stage, r.Outcome, r.Name)
		if r.Attempts > 1 {
			fmt.Printf(" (%d attempts)", r.Attempts)
		}
		fmt.Println()
		if r.Outcome == gate.Fail && r.Tail != "" {
			for _, line := range strings.Split(r.Tail, "\n") {
				fmt.Println("         |", line)
			}
		}
	}
}

func label(code int) string {
	switch code {
	case gate.CodePass:
		return "PASS"
	case gate.CodePostMergeRed:
		return "WARN"
	default:
		return "FAIL"
	}
}
