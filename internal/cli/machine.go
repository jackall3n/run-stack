package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/x/term"

	"github.com/jackall3n/run-stack/internal/certs"
	"github.com/jackall3n/run-stack/internal/config"
	tui "github.com/jackall3n/run-stack/internal/dash"
	"github.com/jackall3n/run-stack/internal/dns"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/ports"
	"github.com/jackall3n/run-stack/internal/proxy"
	"github.com/jackall3n/run-stack/internal/runner"
	"github.com/jackall3n/run-stack/internal/state"
)

func list([]string) (int, error) {
	current := state.Read()
	ids := make([]string, 0, len(current.Stacks))

	for id := range current.Stacks {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	if len(ids) == 0 {
		fmt.Println("no stacks yet")

		return 0, nil
	}

	httpsPort := home.LoadSettings().HTTPSPort
	rows := [][]string{{"STACK", "STATUS", "URL", "FOLDER"}}

	for _, id := range ids {
		entry := current.Stacks[id]
		folder := entry.Dir

		if _, err := os.Stat(entry.Dir); err != nil {
			folder += "  (folder deleted)"
		}

		rows = append(rows, []string{id, entry.Status(), config.Origin(entry.Host, httpsPort), folder})
	}

	widths := make([]int, 3)

	for _, row := range rows {
		for column := range widths {
			widths[column] = max(widths[column], len(row[column]))
		}
	}

	for _, row := range rows {
		fmt.Printf("%-*s  %-*s  %-*s  %s\n", widths[0], row[0], widths[1], row[1], widths[2], row[2], row[3])
	}

	return 0, nil
}

func dash(args []string) (int, error) {
	// Without a terminal to draw on (a pipe, a script, an agent) the list says the same thing.
	if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		return list(args)
	}

	if err := tui.Run(); err != nil {
		return 1, err
	}

	return 0, nil
}

func forget(args []string) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("usage: run-stack forget <id>")
	}

	id := args[0]

	if err := runner.Forget(id); err != nil {
		return 1, err
	}

	fmt.Println("forgot", id)

	return 0, nil
}

func trust([]string) (int, error) {
	if err := certs.Trust(); err != nil {
		return 1, err
	}

	fmt.Println("trusted", home.CACert())

	return 0, nil
}

func doctor([]string) (int, error) {
	settings := home.LoadSettings()
	problems := 0
	check := func(ok bool, good, bad string) {
		if ok {
			fmt.Println("ok   ", good)
		} else {
			fmt.Println("FIX  ", bad)
			problems++
		}
	}

	if pid := proxy.PID(); pid != 0 {
		check(true, fmt.Sprintf("proxy running (pid %d) on :%d and :%d", pid, settings.HTTPSPort, settings.HTTPPort), "")
	} else {
		check(ports.Bindable(settings.HTTPSPort) && ports.Bindable(settings.HTTPPort),
			fmt.Sprintf("proxy not running; ports %d and %d are free for it (it starts with the first stack)", settings.HTTPSPort, settings.HTTPPort),
			fmt.Sprintf("ports %d or %d are held by another program. Stop it, or set https_port and http_port in %s", settings.HTTPSPort, settings.HTTPPort, filepath.Join(home.Dir(), "config.json")))
	}

	check(certs.Trusted(), "certificate authority trusted", `certificate authority not trusted: run "run-stack trust"`)
	check(dns.Supported(), ".local names resolve to this machine while their stack runs",
		`.local names are not announced on this system: set domain = "<name>.localhost" in run-stack.toml`)

	fmt.Println("note  Firefox keeps its own certificate store: import", home.CACert(), "there if you use it.")

	if problems > 0 {
		return 1, nil
	}

	return 0, nil
}

func proxyCommand(args []string) (int, error) {
	action := "status"

	if len(args) > 0 {
		action = args[0]
	}

	switch action {
	case "run":
		return 1, proxy.Serve()
	case "stop":
		if proxy.Stop() {
			fmt.Println("proxy stopped")
		} else {
			fmt.Println("proxy was not running")
		}
	case "start", "restart":
		if action == "restart" {
			proxy.Stop()
		}

		if err := proxy.Ensure(); err != nil {
			return 1, err
		}

		fmt.Println("proxy running, pid", proxy.PID())
	case "status":
		if pid := proxy.PID(); pid != 0 {
			fmt.Println("proxy running, pid", pid)
		} else {
			fmt.Println("proxy not running")
		}
	default:
		return 2, fmt.Errorf("unknown proxy action %q: use start, stop, restart or status", action)
	}

	return 0, nil
}
