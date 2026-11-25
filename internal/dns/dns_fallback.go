//go:build !(darwin && cgo)

package dns

import (
	"os/exec"
	"runtime"
	"sync"
)

// On macOS without cgo the dns-sd tool does the registering, one process per host. It can only
// register an IPv4 address, which makes the odd lookup fail; release builds use cgo instead.
const supported = runtime.GOOS == "darwin"

type processes struct {
	mu      sync.Mutex
	running map[string]*exec.Cmd
}

// New returns an announcer that runs dns-sd on macOS, and one that does nothing elsewhere.
func New() Announcer {
	if !supported {
		return nothing{}
	}

	return &processes{running: map[string]*exec.Cmd{}}
}

func (announcer *processes) Set(hosts []string) {
	announcer.mu.Lock()
	defer announcer.mu.Unlock()

	wanted := local(hosts)

	for host, cmd := range announcer.running {
		if !wanted[host] {
			cmd.Process.Kill()
			cmd.Wait()
			delete(announcer.running, host)
		}
	}

	for host := range wanted {
		if _, done := announcer.running[host]; done {
			continue
		}

		cmd := exec.Command("dns-sd", "-P", host, "_run-stack._tcp", "local", "443", host, "127.0.0.1")

		if cmd.Start() == nil {
			announcer.running[host] = cmd
		}
	}
}

func (announcer *processes) Close() {
	announcer.Set(nil)
}
