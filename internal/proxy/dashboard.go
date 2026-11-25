package proxy

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/ports"
	"github.com/jackall3n/run-stack/internal/runner"
	"github.com/jackall3n/run-stack/internal/state"
)

//go:embed dashboard.html
var dashboardPage []byte

type portView struct {
	Name      string `json:"name"`
	Port      int    `json:"port"`
	URL       string `json:"url,omitempty"`
	Listening bool   `json:"listening"`
	// Declared is false for a port the stack listens on that its config never mentioned.
	Declared bool   `json:"declared"`
	Command  string `json:"command,omitempty"`
}

type stackView struct {
	ID            string     `json:"id"`
	Dir           string     `json:"dir"`
	URL           string     `json:"url"`
	Status        string     `json:"status"`
	StartedAt     string     `json:"startedAt,omitempty"`
	FolderMissing bool       `json:"folderMissing"`
	Ports         []portView `json:"ports"`
}

// stackViews is every stack as the dashboard shows it: running ones first, with each port's URL
// and whether anything is listening on it.
func stackViews(httpsPort int) []stackView {
	views := []stackView{}

	for _, entry := range state.Read().Stacks {
		view := stackView{ID: entry.ID, Dir: entry.Dir, URL: config.Origin(entry.Host, httpsPort), Status: entry.Status(), StartedAt: entry.StartedAt, Ports: []portView{}}

		if _, err := os.Stat(entry.Dir); err != nil {
			view.FolderMissing = true
		}

		up := view.Status != state.Stopped
		routed := map[string]bool{}

		for _, route := range entry.Routes {
			routed[route.Name] = true
			view.Ports = append(view.Ports, portView{Name: route.Name, Port: route.Port, URL: config.Origin(route.Host, httpsPort), Declared: true, Listening: up && ports.Listening(route.Port)})
		}

		internal := []portView{}

		for name, port := range entry.Ports {
			if !routed[name] {
				internal = append(internal, portView{Name: name, Port: port, Declared: true, Listening: up && ports.Listening(port)})
			}
		}

		sort.Slice(internal, func(a, b int) bool { return internal[a].Name < internal[b].Name })
		view.Ports = append(view.Ports, internal...)

		if up && entry.Group != 0 {
			declared := map[int]bool{}

			for _, port := range entry.Ports {
				declared[port] = true
			}

			for _, listener := range ports.GroupListeners(entry.Group) {
				if !declared[listener.Port] {
					view.Ports = append(view.Ports, portView{Name: "undeclared", Port: listener.Port, Listening: true, Command: listener.Command})
				}
			}
		}

		views = append(views, view)
	}

	sort.Slice(views, func(a, b int) bool {
		if (views[a].Status == state.Stopped) != (views[b].Status == state.Stopped) {
			return views[b].Status == state.Stopped
		}

		return views[a].ID < views[b].ID
	})

	return views
}

func (router *router) dashboard(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/":
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Write(dashboardPage)
	case "/api/stacks":
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(stackViews(router.httpsPort))
	case "/api/logs":
		id := request.URL.Query().Get("id")

		// Only a known stack's log: the id becomes a file name.
		if state.Read().Stacks[id] == nil {
			http.Error(writer, "no stack called "+id, http.StatusNotFound)

			return
		}

		file, err := os.Open(home.LogFile(id))
		if err != nil {
			http.Error(writer, "no log yet", http.StatusNotFound)

			return
		}
		defer file.Close()

		// The tail is enough to see what a stack is doing; the whole log can be large.
		const tail = 64 << 10

		if info, err := file.Stat(); err == nil && info.Size() > tail {
			file.Seek(-tail, io.SeekEnd)
		}

		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.Copy(writer, file)
	case "/api/stop", "/api/start":
		// These change things, so only the dashboard's own page may call them: another site open
		// in the browser could otherwise post here.
		source, err := url.Parse(request.Header.Get("Origin"))

		if request.Method != http.MethodPost || err != nil || source.Host != request.Host {
			http.Error(writer, "forbidden", http.StatusForbidden)

			return
		}

		id := request.URL.Query().Get("id")
		entry := state.Read().Stacks[id]

		if entry == nil {
			http.Error(writer, "no stack called "+id, http.StatusNotFound)

			return
		}

		if request.URL.Path == "/api/stop" {
			go runner.Stop(id)
		} else if entry.Status() == state.Stopped {
			cmd, err := runner.StartDetached(entry.Dir)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)

				return
			}

			go cmd.Wait()
		}

		writer.WriteHeader(http.StatusAccepted)
	default:
		http.NotFound(writer, request)
	}
}
