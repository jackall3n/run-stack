package cli

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackall3n/run-stack/internal/certs"
	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/ports"
	"github.com/jackall3n/run-stack/internal/proxy"
	"github.com/jackall3n/run-stack/internal/runner"
	"github.com/jackall3n/run-stack/internal/state"
)

//go:embed starter.toml
var starter string

func trustHint() {
	if !certs.Trusted() {
		fmt.Fprintln(os.Stderr, `Browsers will warn about the certificate until you run "run-stack trust" (once).`)
	}
}

func initConfig([]string) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 1, err
	}

	dir, main := cwd, cwd

	// In a git checkout the config goes where you are, named after the repo.
	if _, mainDir, _, err := config.Locate(cwd); err == nil {
		main = mainDir
	}

	path := filepath.Join(dir, config.FileName)

	if _, err := os.Stat(path); err == nil {
		return 1, fmt.Errorf("%s already exists", path)
	}

	content := strings.NewReplacer("{{name}}", config.Slug(filepath.Base(main)), "{{tld}}", config.DefaultTLD()).Replace(starter)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return 1, err
	}

	fmt.Println("wrote", path)

	return 0, nil
}

func up(args []string) (int, error) {
	stack, err := here()
	if err != nil {
		return 1, err
	}

	if err := proxy.Ensure(); err != nil {
		return 1, err
	}

	// This process is the detached supervisor: run the stack and say nothing.
	if has(args, runner.SuperviseFlag) {
		code, _ := runner.Supervise(stack, nil)

		return code, nil
	}

	// A supervisor that was killed outright leaves its processes behind, holding the stack's ports.
	if entry := state.Read().Stacks[stack.ID]; entry.Status() == state.Orphaned {
		runner.Stop(stack.ID)
	}

	if !has(args, "-d", "--detach") {
		trustHint()

		return runner.Supervise(stack, os.Stdout)
	}

	if entry := state.Read().Stacks[stack.ID]; entry.Running() {
		fmt.Printf("%s is already running\n%s\n", stack.ID, runner.Describe(stack, *entry))

		return 0, nil
	}

	cmd, err := runner.StartDetached(stack.Dir)
	if err != nil {
		return 1, err
	}

	exited := make(chan struct{})

	go func() {
		cmd.Wait()
		close(exited)
	}()

	// Started means the command is running: pre_up hooks have passed by then.
	for {
		if entry := state.Read().Stacks[stack.ID]; entry != nil && entry.PID == cmd.Process.Pid && entry.Group != 0 {
			fmt.Println(runner.Describe(stack, *entry))
			fmt.Printf("\n%s started. Logs: run-stack logs -f %s\n", stack.ID, stack.ID)
			trustHint()

			return 0, nil
		}

		select {
		case <-exited:
			log, _ := os.ReadFile(home.LogFile(stack.ID))
			fmt.Fprint(os.Stderr, string(log))

			return 1, fmt.Errorf("%s did not start", stack.ID)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func wait(args []string) (int, error) {
	stack, err := here()
	if err != nil {
		return 1, err
	}

	timeout := 300 * time.Second

	if len(args) > 0 {
		if seconds, err := strconv.Atoi(args[0]); err == nil {
			timeout = time.Duration(seconds) * time.Second
		}
	}

	for deadline := time.Now().Add(timeout); ; time.Sleep(500 * time.Millisecond) {
		entry := state.Read().Stacks[stack.ID]

		if !entry.Running() {
			return 1, fmt.Errorf("%s is not running. See: run-stack logs %s", stack.ID, stack.ID)
		}

		var pending []string

		for _, name := range stack.Config.PortNames {
			if !stack.Config.Ports[name].Optional && !ports.Listening(entry.Ports[name]) {
				pending = append(pending, fmt.Sprintf("%s (localhost:%d)", name, entry.Ports[name]))
			}
		}

		if len(pending) == 0 {
			fmt.Printf("%s is ready\n%s\n", stack.ID, runner.Describe(stack, *entry))

			return 0, nil
		}

		if time.Now().After(deadline) {
			return 1, fmt.Errorf("still nothing listening on %s after %s. See: run-stack logs %s", strings.Join(pending, ", "), timeout, stack.ID)
		}
	}
}

func down(args []string) (int, error) {
	var ids []string

	if has(args, "--all") {
		for id, entry := range state.Read().Stacks {
			if entry.Status() != state.Stopped {
				ids = append(ids, id)
			}
		}

		sort.Strings(ids)
	} else {
		id, err := stackID(args)
		if err != nil {
			return 1, err
		}

		ids = []string{id}
	}

	for _, id := range ids {
		stopped, err := runner.Stop(id)
		if err != nil {
			return 1, err
		}

		if stopped {
			fmt.Println("stopped", id)
		} else {
			fmt.Println(id, "was not running")
		}
	}

	return 0, nil
}

func urls([]string) (int, error) {
	stack, err := here()
	if err != nil {
		return 1, err
	}

	entry, err := runner.Reserve(stack)
	if err != nil {
		return 1, err
	}

	fmt.Println(runner.Describe(stack, entry))

	return 0, nil
}

func env([]string) (int, error) {
	stack, err := here()
	if err != nil {
		return 1, err
	}

	entry, err := runner.Reserve(stack)
	if err != nil {
		return 1, err
	}

	values, err := stack.Env(entry.Ports, home.LoadSettings().HTTPSPort)
	if err != nil {
		return 1, err
	}

	keys := make([]string, 0, len(values))

	for key := range values {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		fmt.Printf("%s='%s'\n", key, strings.ReplaceAll(values[key], "'", `'\''`))
	}

	return 0, nil
}

func execute(args []string) (int, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	if len(args) == 0 {
		return 2, errors.New("usage: run-stack exec -- <command>")
	}

	stack, err := here()
	if err != nil {
		return 1, err
	}

	entry, err := runner.Reserve(stack)
	if err != nil {
		return 1, err
	}

	environ, err := runner.Environ(stack, entry.Ports)
	if err != nil {
		return 1, err
	}

	binary, err := exec.LookPath(args[0])
	if err != nil {
		return 1, err
	}

	// Replaces this process, so the command gets the terminal and its exit code is ours.
	return 1, syscall.Exec(binary, args, environ)
}

func logs(args []string) (int, error) {
	id, err := stackID(args)
	if err != nil {
		return 1, err
	}

	tail := []string{"tail", "-n", "200"}

	if has(args, "-f") {
		tail = append(tail, "-f")
	}

	binary, err := exec.LookPath("tail")
	if err != nil {
		return 1, err
	}

	return 1, syscall.Exec(binary, append(tail, home.LogFile(id)), os.Environ())
}

func listPorts([]string) (int, error) {
	stack, err := here()
	if err != nil {
		return 1, err
	}

	entry := state.Read().Stacks[stack.ID]

	if !entry.Running() || entry.Group == 0 {
		return 1, fmt.Errorf("%s is not running", stack.ID)
	}

	declared := map[int]string{}

	for name, port := range entry.Ports {
		declared[port] = name
	}

	open := map[int]bool{}
	undeclared := 0

	for _, listener := range ports.GroupListeners(entry.Group) {
		open[listener.Port] = true
		name := declared[listener.Port]

		if name == "" {
			name = "UNDECLARED"
			undeclared++
		}

		fmt.Printf("%-6d %-24s %s (pid %d)\n", listener.Port, name, listener.Command, listener.PID)
	}

	for _, name := range stack.Config.PortNames {
		if !open[entry.Ports[name]] {
			fmt.Printf("%-6d %-24s nothing is listening\n", entry.Ports[name], name)
		}
	}

	if undeclared > 0 {
		fmt.Printf("\n%d undeclared: another stack of this repo would fight over a fixed port. Add each to [ports] in %s and make its process listen on that variable.\n", undeclared, config.FileName)
	}

	return 0, nil
}
