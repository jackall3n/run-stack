// Package cli is the run-stack command line: it parses arguments and prints, and leaves the work
// to the other packages.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/state"
)

const usage = `run-stack runs a checkout as its own local stack: every port it needs gets a free
number, and the ones you route get a URL such as https://<worktree>.<name>.local.

Run from inside the checkout (the main one or any git worktree):

  run-stack init              write a starter run-stack.toml
  run-stack up [-d]           start the stack; -d returns once it is started
  run-stack wait [seconds]    wait until every port answers (default 300)
  run-stack down [id|--all]   stop this checkout's stack, or the one named
  run-stack urls              this checkout's URLs and ports, without starting anything
  run-stack logs [-f] [id]    the stack's output
  run-stack ports             what the stack is really listening on, declared or not
  run-stack env               the variables the stack's processes are given
  run-stack exec -- <cmd>     run a command with those variables

From anywhere:

  run-stack ls                every stack on this machine
  run-stack dash              the dashboard: every stack, its ports and its logs
  run-stack forget <id>       drop a stopped stack and release its ports
  run-stack trust             trust the local certificate authority (once)
  run-stack doctor            check the machine is set up
  run-stack proxy [start|stop|restart|status]
  run-stack version
`

// command is one subcommand. It returns the process's exit code.
type command func(args []string) (int, error)

// Run executes the command line and returns the exit code.
func Run(args []string, version string) int {
	commands := map[string]command{
		"init":    initConfig,
		"up":      up,
		"wait":    wait,
		"down":    down,
		"stop":    down,
		"ls":      list,
		"list":    list,
		"urls":    urls,
		"env":     env,
		"exec":    execute,
		"logs":    logs,
		"ports":   listPorts,
		"dash":    dash,
		"forget":  forget,
		"trust":   trust,
		"doctor":  doctor,
		"proxy":   proxyCommand,
		"version": func([]string) (int, error) { fmt.Println(version); return 0, nil },
		"help":    func([]string) (int, error) { fmt.Print(usage); return 0, nil },
	}

	name := "help"

	if len(args) > 0 {
		name, args = strings.TrimLeft(args[0], "-"), args[1:]
	}

	run, known := commands[name]

	if !known {
		fmt.Fprintf(os.Stderr, "run-stack: unknown command %q\n\n%s", name, usage)

		return 2
	}

	code, err := run(args)

	if err != nil {
		fmt.Fprintln(os.Stderr, "run-stack:", err)

		if code == 0 {
			code = 1
		}
	}

	return code
}

func has(args []string, flags ...string) bool {
	for _, arg := range args {
		for _, flag := range flags {
			if arg == flag {
				return true
			}
		}
	}

	return false
}

// here is the stack of the current folder.
func here() (*config.Stack, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	return config.Load(cwd)
}

// stackID is the id an argument names, or else the stack of the current folder. It works for a
// folder whose config is gone, as long as a stack was started from it.
func stackID(args []string) (string, error) {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg, nil
		}
	}

	if stack, err := here(); err == nil {
		return stack.ID, nil
	}

	cwd, _ := os.Getwd()

	if dir, _, _, err := config.Locate(cwd); err == nil {
		for _, entry := range state.Read().Stacks {
			if entry.Dir == dir {
				return entry.ID, nil
			}
		}
	}

	return "", errors.New(`no stack here. Name one from "run-stack ls"`)
}
