package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/config"
)

// Invariant 8. Thresholds live in configuration. A missing file or field is
// an error, never a silent default.
func TestMissingConfigIsAnError(t *testing.T) {
	if _, err := config.Load(t.TempDir()); !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestMissingFieldsAreNamed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, config.FileName), []byte(`{"mainline":"main"}`), 0o644)
	_, err := config.Load(dir)
	if err == nil || !strings.Contains(err.Error(), "max_workers") || !strings.Contains(err.Error(), "checks") {
		t.Fatalf("got %v", err)
	}
}

func TestLedgerCheckNeedsReconcile(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(config.Example, `"kind": "strict"`, `"kind": "ledger"`, 1)
	os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644)
	if _, err := config.Load(dir); err == nil || !strings.Contains(err.Error(), "reconcile") {
		t.Fatalf("got %v", err)
	}
}

func TestExampleLoads(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, config.FileName), []byte(config.Example), 0o644)
	if _, err := config.Load(dir); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(config.Example, `"mainline"`, `"max_wrokers": 3, "mainline"`, 1)
	os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644)
	if _, err := config.Load(dir); err == nil {
		t.Fatal("a typo in a field name was silently ignored")
	}
}
