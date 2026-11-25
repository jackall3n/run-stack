// Package dash is the dashboard: a terminal view of every stack on the machine, its ports and its
// logs, with keys to start, stop and open them.
package dash

import (
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/ports"
	"github.com/jackall3n/run-stack/internal/state"
)

// Port is one port of a stack as the dashboard shows it.
type Port struct {
	Name      string
	Port      int
	URL       string
	Listening bool
	// Declared is false for a port the stack listens on that its config never mentioned.
	Declared bool
	Command  string
}

// Stack is one stack as the dashboard shows it.
type Stack struct {
	ID            string
	Dir           string
	URL           string
	Status        string
	StartedAt     string
	FolderMissing bool
	Ports         []Port
}

// Load is every stack on the machine, running ones first, with each port's URL and whether
// anything is listening on it.
func Load(httpsPort int) []Stack {
	stacks := []Stack{}

	for _, entry := range state.Read().Stacks {
		stack := Stack{ID: entry.ID, Dir: entry.Dir, URL: config.Origin(entry.Host, httpsPort), Status: entry.Status(), StartedAt: entry.StartedAt}

		if _, err := os.Stat(entry.Dir); err != nil {
			stack.FolderMissing = true
		}

		up := stack.Status != state.Stopped
		routed := map[string]bool{}

		for _, route := range entry.Routes {
			routed[route.Name] = true
			stack.Ports = append(stack.Ports, Port{Name: route.Name, Port: route.Port, URL: config.Origin(route.Host, httpsPort), Declared: true, Listening: up && ports.Listening(route.Port)})
		}

		internal := []Port{}

		for name, port := range entry.Ports {
			if !routed[name] {
				internal = append(internal, Port{Name: name, Port: port, Declared: true, Listening: up && ports.Listening(port)})
			}
		}

		sort.Slice(internal, func(a, b int) bool { return internal[a].Name < internal[b].Name })
		stack.Ports = append(stack.Ports, internal...)

		if up && entry.Group != 0 {
			declared := map[int]bool{}

			for _, port := range entry.Ports {
				declared[port] = true
			}

			for _, listener := range ports.GroupListeners(entry.Group) {
				if !declared[listener.Port] {
					stack.Ports = append(stack.Ports, Port{Name: "undeclared", Port: listener.Port, Listening: true, Command: listener.Command})
				}
			}
		}

		stacks = append(stacks, stack)
	}

	sort.Slice(stacks, func(a, b int) bool {
		if (stacks[a].Status == state.Stopped) != (stacks[b].Status == state.Stopped) {
			return stacks[b].Status == state.Stopped
		}

		return stacks[a].ID < stacks[b].ID
	})

	return stacks
}

// Colours survive; anything else a dev server prints to drive a terminal (clearing the screen,
// moving the cursor, setting the title) would wreck the dashboard's own drawing.
var control = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-ln-z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Z0-9]|[\x00-\x08\x0b\x0c\x0e-\x1a\x1c-\x1f]`)

// Tail is the end of a stack's log as lines that are safe to draw.
func Tail(id string) []string {
	file, err := os.Open(home.LogFile(id))
	if err != nil {
		return nil
	}
	defer file.Close()

	// The tail is enough to see what a stack is doing; the whole log can be large.
	const tail = 256 << 10

	cut := false

	if info, err := file.Stat(); err == nil && info.Size() > tail {
		file.Seek(-tail, io.SeekEnd)
		cut = true
	}

	data, _ := io.ReadAll(file)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	// The first line of a cut log starts mid-line.
	if cut && len(lines) > 1 {
		lines = lines[1:]
	}

	for index, line := range lines {
		// A progress line rewrites itself with carriage returns: its last state is what shows.
		line = strings.TrimRight(line, "\r")

		if at := strings.LastIndex(line, "\r"); at >= 0 {
			line = line[at+1:]
		}

		lines[index] = strings.ReplaceAll(control.ReplaceAllString(line, ""), "\t", "    ")
	}

	if len(lines) == 1 && lines[0] == "" {
		return nil
	}

	return lines
}
