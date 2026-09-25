// Package config loads fleet.json. A missing file or a missing required field
// is an error: thresholds live in configuration, never as silent defaults
// buried in code.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the configuration file at the repository root.
const FileName = "fleet.json"

// Check is one command run against a merged tree.
type Check struct {
	Name string `json:"name"`
	Run  string `json:"run"`
	// Kind is "strict" (any failure blocks) or "ledger" (the check compares a
	// count against a recorded baseline, so several branches can each stay
	// under it and only exceed it together).
	Kind string `json:"kind"`
	// Reconcile re-records the baseline of a ledger check after a batch that
	// only failed it collectively. Required for kind "ledger".
	Reconcile      string   `json:"reconcile,omitempty"`
	TimeoutSec     int      `json:"timeout_sec"`
	RetryOnFail    int      `json:"retry_on_fail"`
	InfraExitCodes []int    `json:"infra_exit_codes,omitempty"`
	InfraPatterns  []string `json:"infra_patterns,omitempty"`
}

// Config is the whole fleet.json.
type Config struct {
	Mainline string `json:"mainline"`
	RunDir   string `json:"run_dir"`
	// Agent is the argv that starts one worker. $FLEET_PROMPT holds the path
	// of the prompt file and $FLEET_BRANCH the branch name.
	Agent               []string `json:"agent"`
	Checks              []Check  `json:"checks"`
	MaxWorkers          int      `json:"max_workers"`
	StaleAfterMin       int      `json:"stale_after_min"`
	DoneMarker          string   `json:"done_marker"`
	ReportDir           string   `json:"report_dir"`
	PlaceholderPatterns []string `json:"placeholder_patterns"`

	// Root is the repository root the file was loaded from (not serialized).
	Root string `json:"-"`
}

// ErrNotFound is returned when fleet.json does not exist.
var ErrNotFound = errors.New("fleet.json not found")

// Load reads and validates root/fleet.json.
func Load(root string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w in %s (run `fleet init`)", ErrNotFound, root)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	c.Root = root
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.Mainline == "" {
		missing = append(missing, "mainline")
	}
	if c.RunDir == "" {
		missing = append(missing, "run_dir")
	}
	if len(c.Agent) == 0 {
		missing = append(missing, "agent")
	}
	if len(c.Checks) == 0 {
		missing = append(missing, "checks")
	}
	if c.MaxWorkers <= 0 {
		missing = append(missing, "max_workers")
	}
	if c.StaleAfterMin <= 0 {
		missing = append(missing, "stale_after_min")
	}
	if c.DoneMarker == "" {
		missing = append(missing, "done_marker")
	}
	if c.ReportDir == "" {
		missing = append(missing, "report_dir")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	for i, ch := range c.Checks {
		if ch.Name == "" || ch.Run == "" {
			return fmt.Errorf("checks[%d]: name and run are required", i)
		}
		switch ch.Kind {
		case "strict":
		case "ledger":
			if ch.Reconcile == "" {
				return fmt.Errorf("check %q: kind ledger requires reconcile", ch.Name)
			}
		default:
			return fmt.Errorf("check %q: kind must be strict or ledger, got %q", ch.Name, ch.Kind)
		}
		if ch.TimeoutSec <= 0 {
			return fmt.Errorf("check %q: timeout_sec must be positive", ch.Name)
		}
	}
	return nil
}

// RunPath resolves a path inside the run directory.
func (c *Config) RunPath(elem ...string) string {
	return filepath.Join(append([]string{c.Root, c.RunDir}, elem...)...)
}

// Example is written by `fleet init`.
const Example = `{
  "mainline": "main",
  "run_dir": ".fleet",
  "agent": ["sh", "-c", "claude -p \"$(cat \"$FLEET_PROMPT\")\" --dangerously-skip-permissions"],
  "checks": [
    {"name": "tests", "run": "go test ./...", "kind": "strict", "timeout_sec": 900, "retry_on_fail": 1, "infra_exit_codes": [124]}
  ],
  "max_workers": 8,
  "stale_after_min": 30,
  "done_marker": "WORKER-DONE",
  "report_dir": "reports",
  "placeholder_patterns": ["TODO: fill in", "<fill>"]
}
`
