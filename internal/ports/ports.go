// Package ports gives stacks their port numbers and finds out what they really listen on.
package ports

import (
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/jackall3n/run-stack/internal/state"
)

// Listening reports whether something accepts connections on the port.
func Listening(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}

	conn.Close()

	return true
}

// Bindable reports whether the proxy could listen on the port. Only the wildcard address is
// tried: macOS lets an ordinary user bind ports below 1024 there, and nowhere else.
func Bindable(port int) bool {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}

	listener.Close()

	return !Listening(port)
}

// Free reports whether nothing holds the port on any address a dev server might bind.
func Free(port int) bool {
	// macOS lets a second process bind a specific address under someone else's wildcard listener,
	// so a successful bind alone doesn't prove the port is unused.
	if Listening(port) {
		return false
	}

	for _, host := range []string{"0.0.0.0", "127.0.0.1", "::1"} {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))

		if err != nil {
			// A machine without IPv6 can't bind ::1; that doesn't make the port taken.
			if errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT) {
				continue
			}

			return false
		}

		listener.Close()
	}

	return true
}

// Allocate gives each named port a number. A stack keeps the numbers it had, unless another
// process has taken one since. New numbers start from a hash of the stack and port name, so they
// are stable, and never match a port recorded for another stack, running or not.
func Allocate(current *state.State, id string, names []string, portRange [2]int) (ports map[string]int, moved []string, err error) {
	taken := map[int]bool{}

	for _, entry := range current.Stacks {
		if entry.ID != id {
			for _, port := range entry.Ports {
				taken[port] = true
			}
		}
	}

	var previous map[string]int

	if entry := current.Stacks[id]; entry != nil {
		previous = entry.Ports
	}

	low, size := portRange[0], portRange[1]-portRange[0]+1
	ports = map[string]int{}

	for _, name := range names {
		kept := previous[name]

		if kept != 0 && !taken[kept] && Free(kept) {
			ports[name] = kept
			taken[kept] = true

			continue
		}

		hash := fnv.New32a()
		hash.Write([]byte(id + "/" + name))

		start, found := int(hash.Sum32()%uint32(size)), 0

		for offset := 0; offset < size && found == 0; offset++ {
			if candidate := low + (start+offset)%size; !taken[candidate] && Free(candidate) {
				found = candidate
			}
		}

		if found == 0 {
			return nil, nil, fmt.Errorf("no free port left between %d and %d", portRange[0], portRange[1])
		}

		if kept != 0 {
			moved = append(moved, fmt.Sprintf("%s: %d was taken by another process, now %d", name, kept, found))
		}

		ports[name] = found
		taken[found] = true
	}

	return ports, moved, nil
}
