package runner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/state"
)

func TestSuperviseRunsHooksPassesPortsAndStopsSidecars(t *testing.T) {
	t.Setenv("RUN_STACK_HOME", t.TempDir())

	dir := t.TempDir()
	parsed, err := config.Parse(`
name = "shop"
command = "echo web=$WEB_PORT url=$APP_URL > started; sleep 60 & sleep 60 & wait"

[ports]
WEB_PORT = { route = "" }

[env]
APP_URL = "{url}"

[hooks]
pre_up = ["touch before"]
post_down = ["touch after"]
`, "fallback")
	if err != nil {
		t.Fatal(err)
	}

	stack := config.New(parsed, dir, dir, "")
	done := make(chan int, 1)

	go func() {
		code, _ := Supervise(stack, nil)
		done <- code
	}()

	var entry *state.Entry

	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if entry = state.Read().Stacks["shop"]; entry != nil && entry.Group != 0 {
			if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
				break
			}
		}
	}

	if entry == nil || entry.Status() != state.Running {
		t.Fatalf("stack did not start: %+v", entry)
	}

	started, _ := os.ReadFile(filepath.Join(dir, "started"))
	want := "web=" + strconv.Itoa(entry.Ports["WEB_PORT"]) + " url=https://" + stack.Host

	if strings.TrimSpace(string(started)) != want {
		t.Errorf("command saw %q, want %q", strings.TrimSpace(string(started)), want)
	}

	if _, err := os.Stat(filepath.Join(dir, "before")); err != nil {
		t.Error("pre_up hook did not run")
	}

	// What "run-stack down" sends the supervisor, which here is this process.
	syscall.Kill(os.Getpid(), syscall.SIGTERM)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Supervise did not return after Stop")
	}

	if state.GroupAlive(entry.Group) {
		t.Error("the command's sidecars outlived the stack")
	}

	after := state.Read().Stacks["shop"]

	if after.Status() != state.Stopped || after.Ports["WEB_PORT"] != entry.Ports["WEB_PORT"] {
		t.Errorf("after stop: %+v", after)
	}

	if _, err := os.Stat(filepath.Join(dir, "after")); err != nil {
		t.Error("post_down hook did not run")
	}

	if log, _ := os.ReadFile(home.LogFile("shop")); !strings.Contains(string(log), "shop stopped") {
		t.Errorf("log = %q", log)
	}
}
