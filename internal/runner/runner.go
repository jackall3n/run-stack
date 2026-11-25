// Package runner starts a stack's command, stays with it while it runs and stops it again,
// together with every process the command started.
package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/ports"
	"github.com/jackall3n/run-stack/internal/state"
)

// SuperviseFlag is the hidden "up" flag that makes a run-stack process the stack's supervisor.
const SuperviseFlag = "--supervise"

func routes(stack *config.Stack, assigned map[string]int) []state.Route {
	list := []state.Route{}

	for _, name := range stack.Config.PortNames {
		if spec := stack.Config.Ports[name]; spec.Route != nil {
			list = append(list, state.Route{
				Host:       stack.RouteHost(*spec.Route),
				Port:       assigned[name],
				HostHeader: spec.HostHeader,
				Stack:      stack.ID,
				Name:       name,
			})
		}
	}

	return list
}

// Reserve records the stack and gives it its ports, without starting anything. A running stack
// is returned as it is.
func Reserve(stack *config.Stack) (state.Entry, error) {
	entry, _, err := reserve(stack, false)

	return entry, err
}

// reserve does what Reserve does. With claim set it also marks the stack as run by this process,
// and fails if another process already runs it.
func reserve(stack *config.Stack, claim bool) (entry state.Entry, moved []string, err error) {
	err = state.Update(func(current *state.State) error {
		existing := current.Stacks[stack.ID]

		if existing.Running() {
			if claim {
				return fmt.Errorf(`%s is already running (pid %d). Stop it with "run-stack down"`, stack.ID, existing.PID)
			}

			entry = *existing

			return nil
		}

		assigned, changed, err := ports.Allocate(current, stack.ID, stack.Config.PortNames, home.LoadSettings().PortRange)
		if err != nil {
			return err
		}

		created := &state.Entry{ID: stack.ID, Dir: stack.Dir, Host: stack.Host, Ports: assigned, Routes: routes(stack, assigned)}

		if claim {
			created.PID = os.Getpid()
			created.StartedAt = time.Now().Format(time.RFC3339)
		}

		current.Stacks[stack.ID] = created
		entry, moved = *created, changed

		return nil
	})

	return entry, moved, err
}

// Environ is the full environment for a stack's processes: this process's, with the stack's on top.
func Environ(stack *config.Stack, assigned map[string]int) ([]string, error) {
	extra, err := stack.Env(assigned, home.LoadSettings().HTTPSPort)
	if err != nil {
		return nil, err
	}

	env := os.Environ()

	// Lets Node programs inside the stack call other stacks over HTTPS.
	if os.Getenv("NODE_EXTRA_CA_CERTS") == "" {
		if _, err := os.Stat(home.CACert()); err == nil {
			env = append(env, "NODE_EXTRA_CA_CERTS="+home.CACert())
		}
	}

	for key, value := range extra {
		env = append(env, key+"="+value)
	}

	return env, nil
}

// Describe lists the stack's ports, with the URL of each routed one.
func Describe(stack *config.Stack, entry state.Entry) string {
	httpsPort := home.LoadSettings().HTTPSPort
	width := 0

	for _, name := range stack.Config.PortNames {
		width = max(width, len(name))
	}

	var lines []string

	for _, name := range stack.Config.PortNames {
		line := fmt.Sprintf("%-*s  localhost:%d", width, name, entry.Ports[name])

		if route := stack.Config.Ports[name].Route; route != nil {
			line = fmt.Sprintf("%-*s  %s  (localhost:%d)", width, name, config.Origin(stack.RouteHost(*route), httpsPort), entry.Ports[name])
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func stopGroup(group int) {
	if group <= 0 {
		return
	}

	syscall.Kill(-group, syscall.SIGTERM)

	for waited := 0; waited < 80; waited++ {
		if !state.GroupAlive(group) {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	syscall.Kill(-group, syscall.SIGKILL)
}

func release(id string) {
	state.Update(func(current *state.State) error {
		if entry := current.Stacks[id]; entry != nil && (entry.PID == os.Getpid() || !state.Alive(entry.PID)) {
			entry.PID, entry.Group, entry.StartedAt = 0, 0, ""
		}

		return nil
	})
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (locked *lockedWriter) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()

	return locked.writer.Write(data)
}

// Supervise runs the stack and stays with it until the command exits or this process is told to
// stop. Everything printed goes to the stack's log file, and to echo too when it is not nil.
func Supervise(stack *config.Stack, echo io.Writer) (code int, err error) {
	entry, moved, err := reserve(stack, true)
	if err != nil {
		return 1, err
	}

	defer release(stack.ID)

	logHandle, err := os.Create(home.LogFile(stack.ID))
	if err != nil {
		return 1, err
	}
	defer logHandle.Close()

	var out io.Writer = &lockedWriter{writer: logHandle}

	if echo != nil {
		out = &lockedWriter{writer: io.MultiWriter(logHandle, echo)}
	}

	say := func(format string, args ...any) {
		fmt.Fprintf(out, "[run-stack] "+format+"\n", args...)
	}

	// From here on a failure belongs in the log: a detached stack has nowhere else to say it.
	defer func() {
		if err != nil {
			say("%v", err)
		}
	}()

	for _, file := range stack.Config.Copy {
		from, to := filepath.Join(stack.Main, file), filepath.Join(stack.Dir, file)

		if _, statErr := os.Stat(to); statErr == nil {
			continue
		}

		if data, readErr := os.ReadFile(from); readErr == nil {
			os.MkdirAll(filepath.Dir(to), 0o755)

			if os.WriteFile(to, data, 0o600) == nil {
				say("copied %s from %s", file, stack.Main)
			}
		}
	}

	for _, line := range moved {
		say("%s", line)
	}

	fmt.Fprintf(out, "%s\n\n", Describe(stack, entry))

	env, err := Environ(stack, entry.Ports)
	if err != nil {
		return 1, err
	}

	shell := func(command string) *exec.Cmd {
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = stack.Dir, env, out, out

		return cmd
	}

	defer func() {
		for _, hook := range stack.Config.Hooks.PostDown {
			say("post_down: %s", hook)
			shell(hook).Run()
		}
	}()

	for _, hook := range stack.Config.Hooks.PreUp {
		say("pre_up: %s", hook)

		if hookErr := shell(hook).Run(); hookErr != nil {
			return 1, fmt.Errorf("pre_up hook failed (%v), not starting: %s", hookErr, hook)
		}
	}

	cmd := shell(stack.Config.Command)
	// Its own process group, so stopping the stack takes every sidecar the command started with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		return 1, err
	}

	group := cmd.Process.Pid

	state.Update(func(current *state.State) error {
		if running := current.Stacks[stack.ID]; running != nil {
			running.Group = group
		}

		return nil
	})

	exited := make(chan error, 1)

	go func() { exited <- cmd.Wait() }()

	select {
	case <-signals:
		say("stopping %s", stack.ID)
		stopGroup(group)
		<-exited
		say("%s stopped", stack.ID)

		return 0, nil
	case <-exited:
		// The command is gone; anything it left running in its group goes too.
		stopGroup(group)

		code = cmd.ProcessState.ExitCode()
		say("%s exited (code %d)", stack.ID, code)

		return code, nil
	}
}

// StartDetached starts a stack's supervisor in the background, in its own session so it outlives
// the terminal. dir is the stack's folder.
func StartDetached(dir string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(self, "up", SuperviseFlag)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	return cmd, cmd.Start()
}

// Stop ends a stack by id, whichever folder it was started from. It reports whether anything was running.
func Stop(id string) (bool, error) {
	entry := state.Read().Stacks[id]

	if entry == nil {
		return false, fmt.Errorf("no stack called %s", id)
	}

	if entry.Status() == state.Stopped {
		return false, nil
	}

	if state.Alive(entry.PID) {
		syscall.Kill(entry.PID, syscall.SIGTERM)

		for waited := 0; waited < 150 && state.Alive(entry.PID); waited++ {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// The supervisor normally cleans up after itself; this covers one that was killed outright.
	if state.Alive(entry.PID) {
		syscall.Kill(entry.PID, syscall.SIGKILL)
		time.Sleep(100 * time.Millisecond)
	}

	stopGroup(entry.Group)
	release(id)

	return true, nil
}
