// Package dns makes .local names resolve to this machine, in every program and without sudo, by
// registering them with the system's mDNS responder. Names under .localhost need none of this:
// browsers resolve them by themselves.
package dns

import "strings"

// Announcer keeps a set of host names resolving to this machine for as long as the process lives.
type Announcer interface {
	// Set makes exactly these hosts resolve here, adding and withdrawing as needed. Hosts that
	// are not under .local are ignored.
	Set(hosts []string)
	Close()
}

// Supported reports whether New returns an announcer that does anything on this system.
func Supported() bool {
	return supported
}

func local(hosts []string) map[string]bool {
	wanted := map[string]bool{}

	for _, host := range hosts {
		if strings.HasSuffix(host, ".local") {
			wanted[host] = true
		}
	}

	return wanted
}

type nothing struct{}

func (nothing) Set([]string) {}
func (nothing) Close()       {}
