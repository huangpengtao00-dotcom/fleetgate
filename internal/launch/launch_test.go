package launch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/launch"
	tu "github.com/huangpengtao00-dotcom/fleetgate/internal/testutil"
)

func TestTemplatedOrEmptyPromptIsRejected(t *testing.T) {
	for _, p := range []string{"", "  \n", "fix {{TASK}} on {{BRANCH}}"} {
		if err := launch.ValidatePrompt(p); !errors.Is(err, launch.ErrPrompt) {
			t.Fatalf("%q: want ErrPrompt, got %v", p, err)
		}
	}
}

func TestCapacityIsEnforced(t *testing.T) {
	c := tu.Repo(t)
	_, err := launch.Launch(c, "x", "true", launch.Options{Running: c.MaxWorkers})
	if !errors.Is(err, launch.ErrCapacity) {
		t.Fatalf("got %v", err)
	}
}

func TestWorkerThatDiesAtStartIsReported(t *testing.T) {
	c := tu.Repo(t)
	_, err := launch.Launch(c, "x", "exit 3", launch.Options{Grace: 2 * time.Second})
	if !errors.Is(err, launch.ErrExitedAtStart) {
		t.Fatalf("got %v", err)
	}
}

// The worktree is cut from the mainline, not from whatever is checked out.
func TestWorktreeStartsFromTheMainline(t *testing.T) {
	c := tu.Repo(t)
	tu.Git(t, c.Root, "checkout", "-q", "-b", "elsewhere")
	tu.WriteFile(t, c.Root, "stray.txt", "x")
	tu.Git(t, c.Root, "add", "-A")
	tu.Git(t, c.Root, "commit", "-q", "-m", "stray")
	w, err := launch.Launch(c, "x", "true", launch.Options{})
	if err != nil {
		t.Fatal(err)
	}
	<-w.Done
	if got, want := tu.Git(t, w.Paths.Worktree, "rev-parse", "HEAD"), tu.Git(t, c.Root, "rev-parse", "main"); got != want {
		t.Fatal("worktree was not cut from main")
	}
}
