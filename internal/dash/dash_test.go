package dash

import (
	"net"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/state"
)

func TestLoadListsRunningStacksFirstWithTheirPorts(t *testing.T) {
	t.Setenv("RUN_STACK_HOME", t.TempDir())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	err = state.Update(func(current *state.State) error {
		current.Stacks["old"] = &state.Entry{ID: "old", Dir: "/nowhere", Host: "old.local", Ports: map[string]int{"PORT": 1}}
		current.Stacks["shop"] = &state.Entry{
			ID:     "shop",
			Dir:    t.TempDir(),
			Host:   "shop.local",
			PID:    os.Getpid(),
			Ports:  map[string]int{"WEB_PORT": port, "JOBS_PORT": 1},
			Routes: []state.Route{{Host: "shop.local", Port: port, Stack: "shop", Name: "WEB_PORT"}},
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	stacks := Load(443)

	if len(stacks) != 2 || stacks[0].ID != "shop" || stacks[0].Status != state.Running || stacks[1].Status != state.Stopped {
		t.Fatalf("stacks: %+v", stacks)
	}

	web, jobs := stacks[0].Ports[0], stacks[0].Ports[1]

	if web.Name != "WEB_PORT" || web.URL != "https://shop.local" || !web.Listening || jobs.Name != "JOBS_PORT" || jobs.URL != "" || jobs.Listening {
		t.Errorf("ports: %+v", stacks[0].Ports)
	}

	if !stacks[1].FolderMissing || stacks[1].Ports[0].Listening {
		t.Errorf("stopped stack: %+v", stacks[1])
	}
}

func TestTailKeepsColoursAndDropsTerminalControl(t *testing.T) {
	t.Setenv("RUN_STACK_HOME", t.TempDir())
	os.MkdirAll(home.LogDir(), 0o755)
	os.WriteFile(home.LogFile("shop"), []byte("\x1b[2J\x1b[Hready\n10%\r50%\r100%\r\n\x1b[32mok\x1b[0m\tdone\n"), 0o644)

	want := []string{"ready", "100%", "\x1b[32mok\x1b[0m    done"}

	if got := Tail("shop"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("tail = %q, want %q", got, want)
	}

	if got := Tail("missing"); got != nil {
		t.Errorf("tail of no log = %q", got)
	}
}

func press(t *testing.T, m model, keys ...string) model {
	t.Helper()

	for _, key := range keys {
		next, _ := m.key(key)
		m = next.(model)
	}

	return m
}

func TestDashboardDrawsWithinTheWindowAndFollowsKeys(t *testing.T) {
	logs := make([]string, 200)

	for index := range logs {
		logs[index] = strings.Repeat("line ", 30)
	}

	var m tea.Model = newModel(443)

	for _, msg := range []tea.Msg{
		tea.WindowSizeMsg{Width: 60, Height: 20},
		loadedMsg{
			proxyUp: true,
			stacks: []Stack{
				{ID: "shop", Dir: "/code/shop", URL: "https://shop.local", Status: state.Running, Ports: []Port{
					{Name: "WEB_PORT", Port: 41000, URL: "https://shop.local", Declared: true, Listening: true},
					{Name: "undeclared", Port: 5555, Listening: true, Command: "node"},
				}},
				{ID: "old", Dir: "/code/old", URL: "https://old.local", Status: state.Stopped},
			},
			logs: logsMsg{id: "shop", lines: logs},
		},
	} {
		m, _ = m.Update(msg)
	}

	dash := m.(model)

	draw := func(m model) string {
		t.Helper()

		content := m.View().Content
		lines := strings.Split(content, "\n")

		if len(lines) != 20 {
			t.Errorf("drew %d lines in a window of 20", len(lines))
		}

		for _, line := range lines {
			if ansi.StringWidth(line) > 60 {
				t.Errorf("line wider than the window: %q", ansi.Strip(line))
			}
		}

		return ansi.Strip(content)
	}

	for _, want := range []string{"1 running, 1 stopped", "› ● shop", "WEB_PORT", "not in run-stack.toml: node", "s stop"} {
		if drawn := draw(dash); !strings.Contains(drawn, want) {
			t.Errorf("no %q in:\n%s", want, drawn)
		}
	}

	if scrolled := press(t, dash, "pgup"); scrolled.scroll == 0 || !strings.Contains(draw(scrolled), "lines below") {
		t.Errorf("pgup did not scroll: %d", scrolled.scroll)
	}

	if back := press(t, dash, "pgup", "pgup", "end"); back.scroll != 0 {
		t.Errorf("end did not return to the tail: %d", back.scroll)
	}

	zoomed := press(t, dash, "l")

	if strings.Contains(draw(zoomed), "WEB_PORT") {
		t.Error("zoomed logs still show the ports")
	}

	if up := press(t, zoomed, "up", "up"); up.scroll != 2 || up.selected != "shop" {
		t.Errorf("arrows in zoomed logs: scroll %d, selected %q", up.scroll, up.selected)
	}

	wheeled, _ := dash.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})

	if got := wheeled.(model).scroll; got != wheel {
		t.Errorf("wheel up scrolled %d lines, want %d", got, wheel)
	}

	if wheeled, _ = wheeled.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown}); wheeled.(model).scroll != 0 {
		t.Errorf("wheel down did not return to the tail")
	}

	if refused := press(t, dash, "x"); refused.forgetting != "" || !strings.Contains(draw(refused), "stop it before forgetting it") {
		t.Errorf("a running stack was offered for forgetting")
	}

	old := press(t, dash, "down")

	if old.selected != "old" || !strings.Contains(draw(old), "s start") {
		t.Errorf("down selected %q", old.selected)
	}

	if asked := press(t, old, "x"); asked.forgetting != "old" || !strings.Contains(draw(asked), "forget old?") {
		t.Errorf("x did not ask before forgetting")
	} else if kept := press(t, asked, "n"); kept.forgetting != "" {
		t.Errorf("n did not cancel")
	}
}
