// Package liveness decides whether a worker process is alive.
//
// A worker is alive only if the pid in its pid file exists AND that process
// started at the time recorded when it was launched. A bare "kill -0" is
// fooled by pid reuse, and matching on command lines (pgrep -f) hits any
// process whose arguments merely mention the branch name.
package liveness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Record is the content of a pid file.
type Record struct {
	PID        int       `json:"pid"`
	Start      string    `json:"start"`
	Branch     string    `json:"branch"`
	LaunchedAt time.Time `json:"launched_at"`
}

// StartTime returns the start timestamp the OS reports for pid, as an opaque
// string suitable for equality comparison.
func StartTime(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", nil // no such process
		}
		return "", fmt.Errorf("ps: %w", err)
	}
	return strings.Join(strings.Fields(string(out)), " "), nil
}

// Write stores a record for a freshly started process.
func Write(path string, branch string, pid int) (Record, error) {
	start, err := StartTime(pid)
	if err != nil {
		return Record{}, err
	}
	if start == "" {
		return Record{}, fmt.Errorf("process %d exited before its start time could be recorded", pid)
	}
	rec := Record{PID: pid, Start: start, Branch: branch, LaunchedAt: time.Now().UTC()}
	b, err := json.Marshal(rec)
	if err != nil {
		return Record{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return Record{}, err
	}
	return rec, os.Rename(tmp, path)
}

// Read loads a pid file.
func Read(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, fmt.Errorf("%s: %w", path, err)
	}
	return rec, nil
}

// Alive reports whether the recorded process is still the one we started.
func Alive(rec Record) (bool, error) {
	if rec.PID <= 0 {
		return false, nil
	}
	if err := syscall.Kill(rec.PID, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false, nil
	}
	start, err := StartTime(rec.PID)
	if err != nil {
		return false, err
	}
	if start == "" || start != rec.Start {
		return false, nil // gone, or the pid now belongs to someone else
	}
	return !isZombie(rec.PID), nil
}

func isZombie(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
