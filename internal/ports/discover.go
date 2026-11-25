package ports

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Listener is one TCP port a process is listening on.
type Listener struct {
	Port    int
	PID     int
	Command string
}

// GroupListeners lists every TCP port that a process group, and anything descended from it, is
// listening on. That is how sidecars nobody declared get found.
func GroupListeners(group int) []Listener {
	table, _ := exec.Command("ps", "-axo", "pid=,ppid=,pgid=").Output()
	parents := map[int]int{}
	members := map[int]bool{}

	for _, line := range strings.Split(string(table), "\n") {
		fields := strings.Fields(line)

		if len(fields) != 3 {
			continue
		}

		pid, _ := strconv.Atoi(fields[0])
		parent, _ := strconv.Atoi(fields[1])
		pgid, _ := strconv.Atoi(fields[2])

		parents[pid] = parent

		if pgid == group {
			members[pid] = true
		}
	}

	// A sidecar that started its own process group is still ours if an ancestor is in the group.
	for pid := range parents {
		for current, depth := pid, 0; current > 1 && depth < 64; current, depth = parents[current], depth+1 {
			if members[current] {
				members[pid] = true

				break
			}
		}
	}

	// lsof exits 1 when some process has nothing to show; what it printed still counts.
	output, _ := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	found := map[int]Listener{}
	pid, command := 0, ""

	for _, line := range strings.Split(string(output), "\n") {
		if line == "" {
			continue
		}

		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			command = line[1:]
		case 'n':
			port, _ := strconv.Atoi(line[strings.LastIndex(line, ":")+1:])

			if members[pid] && port != 0 {
				found[port] = Listener{Port: port, PID: pid, Command: command}
			}
		}
	}

	listeners := make([]Listener, 0, len(found))

	for _, listener := range found {
		listeners = append(listeners, listener)
	}

	sort.Slice(listeners, func(a, b int) bool { return listeners[a].Port < listeners[b].Port })

	return listeners
}
