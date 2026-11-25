// Package state is the record of every stack on the machine: its ports, its routes and whether
// it is running. It lives in one JSON file that the CLI writes and the proxy reads.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"syscall"

	"github.com/jackall3n/run-stack/internal/home"
)

type Route struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	HostHeader string `json:"host_header,omitempty"`
	// Stack and Name say which port this is, for the proxy's error pages.
	Stack string `json:"stack"`
	Name  string `json:"name"`
}

type Entry struct {
	ID   string `json:"id"`
	Dir  string `json:"dir"`
	Host string `json:"host"`
	// Ports are sticky: a stack keeps its numbers across restarts.
	Ports  map[string]int `json:"ports"`
	Routes []Route        `json:"routes"`
	// PID is the supervising run-stack process, while the stack is up.
	PID int `json:"pid,omitempty"`
	// Group is the command's process group, while the stack is up.
	Group     int    `json:"group,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type State struct {
	Stacks map[string]*Entry `json:"stacks"`
}

const (
	Running = "running"
	Stopped = "stopped"
	// Orphaned: the supervisor died without stopping the command. "run-stack down" clears it up.
	Orphaned = "orphaned"
)

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
}

// GroupAlive reports whether any process is left in a process group.
func GroupAlive(group int) bool {
	return group > 0 && !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
}

func (entry *Entry) Running() bool {
	return entry != nil && Alive(entry.PID)
}

func (entry *Entry) Status() string {
	switch {
	case entry.Running():
		return Running
	case entry != nil && GroupAlive(entry.Group):
		return Orphaned
	default:
		return Stopped
	}
}

// Read returns the state as it is now. It never fails: a missing or broken file is an empty state.
func Read() *State {
	state := &State{}

	if data, err := os.ReadFile(home.StateFile()); err == nil {
		_ = json.Unmarshal(data, state)
	}

	if state.Stacks == nil {
		state.Stacks = map[string]*Entry{}
	}

	return state
}

// Update reads, changes and writes the state under a lock, so two stacks starting at the same
// moment can't be given the same port.
func Update(change func(*State) error) error {
	if err := os.MkdirAll(home.LogDir(), 0o755); err != nil {
		return err
	}

	lock, err := os.OpenFile(home.LockFile(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()

	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}

	state := Read()

	if err := change(state); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	// Written beside the file and renamed over it, so the proxy never reads half a state.
	temp := home.StateFile() + ".tmp"

	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}

	return os.Rename(temp, home.StateFile())
}
