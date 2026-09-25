package gate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
)

// Outcome classifies one check run.
type Outcome string

const (
	Pass  Outcome = "pass"
	Fail  Outcome = "fail"
	Flaky Outcome = "flaky" // failed, then passed on retry: allowed through, but reported
	Infra Outcome = "infra" // could not run meaningfully; says nothing about the branch
)

// CheckResult is the outcome of one check.
type CheckResult struct {
	Stage    string // "preview" or "mainline"
	Name     string
	Kind     string
	Outcome  Outcome
	ExitCode int
	Attempts int
	Tail     string
}

// runCheck runs one check in dir. Failures are split three ways: a timeout or
// a declared infrastructure signature is Infra; a failure that passes on a
// retry is Flaky; only a failure that reproduces is Fail.
func runCheck(ch config.Check, dir string) CheckResult {
	res := CheckResult{Name: ch.Name, Kind: ch.Kind}
	for attempt := 0; attempt <= ch.RetryOnFail; attempt++ {
		res.Attempts = attempt + 1
		code, out, timedOut := execCheck(ch, dir)
		res.ExitCode, res.Tail = code, tail(out, 20)
		if code == 0 {
			if attempt > 0 {
				res.Outcome = Flaky
			} else {
				res.Outcome = Pass
			}
			return res
		}
		if timedOut || isInfra(ch, code, out) {
			res.Outcome = Infra
			return res
		}
		res.Outcome = Fail
	}
	return res
}

func execCheck(ch config.Check, dir string) (int, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ch.TimeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", ch.Run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FLEET_TREE="+dir)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return 124, buf.String(), true
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), buf.String(), false
		}
		return 127, buf.String() + "\n" + err.Error(), false
	}
	return 0, buf.String(), false
}

func isInfra(ch config.Check, code int, out string) bool {
	for _, c := range ch.InfraExitCodes {
		if c == code {
			return true
		}
	}
	for _, p := range ch.InfraPatterns {
		if p != "" && strings.Contains(out, p) {
			return true
		}
	}
	return false
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// runAll runs every check; it does not stop at the first failure, so the
// report names every red check at once.
func runAll(stage string, checks []config.Check, dir string) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, ch := range checks {
		r := runCheck(ch, dir)
		r.Stage = stage
		out = append(out, r)
	}
	return out
}

type summary struct {
	failed     []CheckResult
	infra      []CheckResult
	flaky      []CheckResult
	onlyLedger bool
}

func summarize(rs []CheckResult) summary {
	var s summary
	s.onlyLedger = true
	for _, r := range rs {
		switch r.Outcome {
		case Fail:
			s.failed = append(s.failed, r)
			if r.Kind != "ledger" {
				s.onlyLedger = false
			}
		case Infra:
			s.infra = append(s.infra, r)
		case Flaky:
			s.flaky = append(s.flaky, r)
		}
	}
	if len(s.failed) == 0 {
		s.onlyLedger = false
	}
	return s
}

func (s summary) green() bool { return len(s.failed) == 0 && len(s.infra) == 0 }

func names(rs []CheckResult) string {
	n := make([]string, len(rs))
	for i, r := range rs {
		n[i] = r.Name
	}
	return strings.Join(n, ", ")
}

func subset(all []config.Check, rs []CheckResult) []config.Check {
	want := map[string]bool{}
	for _, r := range rs {
		want[r.Name] = true
	}
	var out []config.Check
	for _, c := range all {
		if want[c.Name] {
			out = append(out, c)
		}
	}
	return out
}
