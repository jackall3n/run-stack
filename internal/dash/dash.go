package dash

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jackall3n/run-stack/internal/config"
	"github.com/jackall3n/run-stack/internal/home"
	"github.com/jackall3n/run-stack/internal/proxy"
	"github.com/jackall3n/run-stack/internal/runner"
	"github.com/jackall3n/run-stack/internal/state"
)

// The terminal's own sixteen colours, so the dashboard follows whatever theme it is in.
var (
	bold   = lipgloss.NewStyle().Bold(true)
	faint  = lipgloss.NewStyle().Faint(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

const (
	refresh = time.Second
	// wheel is how many lines one notch of the mouse wheel scrolls the log.
	wheel = 3
)

type (
	tickMsg   struct{}
	loadedMsg struct {
		stacks  []Stack
		proxyUp bool
		logs    logsMsg
	}
	logsMsg struct {
		id    string
		lines []string
	}
	// doneMsg is the end of a start, stop, restart or forget.
	doneMsg struct {
		id   string
		note string
		err  error
	}
)

type model struct {
	httpsPort     int
	width, height int

	loaded   bool
	stacks   []Stack
	proxyUp  bool
	selected string

	// logs are the selected stack's, wrapped to the window's width.
	logs    []string
	logsFor string
	// scroll is how many lines up from the end the log is showing: 0 follows it.
	scroll int
	// zoomed gives the whole window to the log.
	zoomed bool

	// busy is what is being done to a stack right now, such as "stopping".
	busy map[string]string
	// forgetting is the stack whose removal is waiting for a "y".
	forgetting string
	note       string
	failed     bool
}

// Run shows the dashboard until it is quit.
func Run() error {
	_, err := tea.NewProgram(newModel(home.LoadSettings().HTTPSPort)).Run()

	return err
}

func newModel(httpsPort int) model {
	return model{httpsPort: httpsPort, width: 80, height: 24, busy: map[string]string{}}
}

func (m model) Init() tea.Cmd {
	return m.load()
}

func (m model) load() tea.Cmd {
	httpsPort, selected := m.httpsPort, m.selected

	return func() tea.Msg {
		loaded := loadedMsg{stacks: Load(httpsPort), proxyUp: proxy.PID() != 0}

		if selected == "" && len(loaded.stacks) > 0 {
			selected = loaded.stacks[0].ID
		}

		loaded.logs = logsMsg{id: selected, lines: Tail(selected)}

		return loaded
	}
}

func readLogs(id string) tea.Cmd {
	return func() tea.Msg { return logsMsg{id: id, lines: Tail(id)} }
}

func (m model) current() *Stack {
	for index := range m.stacks {
		if m.stacks[index].ID == m.selected {
			return &m.stacks[index]
		}
	}

	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		return m, readLogs(m.selected)

	case tickMsg:
		return m, m.load()

	case loadedMsg:
		m.loaded, m.stacks, m.proxyUp = true, msg.stacks, msg.proxyUp

		// The selected stack may have been forgotten, here or from another terminal.
		if m.current() == nil {
			m.selected = ""

			if len(m.stacks) > 0 {
				m.selected = m.stacks[0].ID
			}
		}

		m.setLogs(msg.logs)

		return m, tea.Tick(refresh, func(time.Time) tea.Msg { return tickMsg{} })

	case logsMsg:
		m.setLogs(msg)

		return m, nil

	case doneMsg:
		delete(m.busy, msg.id)
		m.note, m.failed = msg.note, false

		if msg.err != nil {
			m.note, m.failed = msg.err.Error(), true
		}

		return m, m.load()

	case tea.KeyPressMsg:
		return m.key(msg.String())

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scrollBy(wheel)
		case tea.MouseWheelDown:
			m.scrollBy(-wheel)
		}

		return m, nil
	}

	return m, nil
}

func (m *model) setLogs(logs logsMsg) {
	if logs.id != m.selected {
		return
	}

	if m.logsFor != logs.id {
		m.scroll = 0
	}

	wrapped := make([]string, 0, len(logs.lines))

	for _, line := range logs.lines {
		for _, part := range strings.Split(lipgloss.Wrap(line, max(m.width-2, 10), ""), "\n") {
			// A colour a line leaves open must not run into the lines after it.
			wrapped = append(wrapped, part+ansi.ResetStyle)
		}
	}

	// Scrolled back, the view stays on the same lines while new ones arrive below.
	if m.scroll > 0 && m.logsFor == logs.id && len(wrapped) > len(m.logs) {
		m.scroll += len(wrapped) - len(m.logs)
	}

	m.logs, m.logsFor = wrapped, logs.id
}

func (m model) key(key string) (tea.Model, tea.Cmd) {
	m.note, m.failed = "", false

	if m.forgetting != "" {
		id := m.forgetting
		m.forgetting = ""

		if key != "y" {
			return m, nil
		}

		return m, func() tea.Msg {
			return doneMsg{id: id, note: "forgot " + id, err: runner.Forget(id)}
		}
	}

	page := max(m.logRows()-1, 1)

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.zoomed = false
	case "l", "enter":
		m.zoomed = !m.zoomed
	case "up", "k":
		// With the log filling the window, the arrows move through it instead of the stacks.
		if !m.zoomed {
			return m.move(-1)
		}

		m.scroll++
	case "down", "j":
		if !m.zoomed {
			return m.move(1)
		}

		m.scroll--
	case "pgup", "ctrl+u", "b":
		m.scroll += page
	case "pgdown", "ctrl+d", "space", "f":
		m.scroll -= page
	case "home", "g":
		m.scroll = len(m.logs)
	case "end", "G":
		m.scroll = 0
	case "s", "r", "o", "x":
		return m.act(key)
	}

	m.scrollBy(0)

	return m, nil
}

// scrollBy moves the log up by a number of lines (down if negative), stopping at either end.
func (m *model) scrollBy(lines int) {
	m.scroll = max(0, min(m.scroll+lines, len(m.logs)-m.logRows()))
}

func (m model) move(by int) (tea.Model, tea.Cmd) {
	for index := range m.stacks {
		if m.stacks[index].ID == m.selected {
			next := max(0, min(index+by, len(m.stacks)-1))

			if next == index {
				return m, nil
			}

			m.selected, m.logs, m.scroll = m.stacks[next].ID, nil, 0

			return m, readLogs(m.selected)
		}
	}

	return m, nil
}

func (m model) act(key string) (tea.Model, tea.Cmd) {
	stack := m.current()

	if stack == nil {
		return m, nil
	}

	id, dir, stopped := stack.ID, stack.Dir, stack.Status == state.Stopped

	if m.busy[id] != "" {
		m.note = fmt.Sprintf("%s is %s", id, m.busy[id])

		return m, nil
	}

	switch {
	case key == "o" && stopped:
		m.note = id + " is stopped: s starts it"
	case key == "o":
		return m, func() tea.Msg { return doneMsg{id: id, err: browse(stack.URL)} }
	case key == "x" && !stopped:
		m.note = id + " is " + stack.Status + ": stop it before forgetting it"
	case key == "x":
		m.forgetting = id
	case key == "s" && stopped:
		m.busy[id] = "starting"

		return m, func() tea.Msg { return doneMsg{id: id, note: "started " + id, err: start(id, dir)} }
	case key == "s":
		m.busy[id] = "stopping"

		return m, func() tea.Msg {
			_, err := runner.Stop(id)

			return doneMsg{id: id, note: "stopped " + id, err: err}
		}
	case key == "r":
		m.busy[id] = "restarting"

		return m, func() tea.Msg {
			if _, err := runner.Stop(id); err != nil {
				return doneMsg{id: id, err: err}
			}

			return doneMsg{id: id, note: "restarted " + id, err: start(id, dir)}
		}
	}

	return m, nil
}

// start runs a stack in the background and returns once its command is running.
func start(id, dir string) error {
	// The supervisor has nowhere to report a broken config, so it is read here first.
	if _, err := config.Load(dir); err != nil {
		return err
	}

	cmd, err := runner.StartDetached(dir)
	if err != nil {
		return err
	}

	exited := make(chan struct{})

	go func() {
		cmd.Wait()
		close(exited)
	}()

	for {
		if entry := state.Read().Stacks[id]; entry != nil && entry.PID == cmd.Process.Pid && entry.Group != 0 {
			return nil
		}

		select {
		case <-exited:
			return fmt.Errorf("%s did not start: its log says why", id)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func browse(address string) error {
	opener := "xdg-open"

	if runtime.GOOS == "darwin" {
		opener = "open"
	}

	return exec.Command(opener, address).Run()
}

func (m model) View() tea.View {
	var lines []string

	if stack := m.current(); m.zoomed && stack != nil {
		lines = append(lines, fmt.Sprintf("%s %s  %s", dot(stack.Status), bold.Render(stack.ID), faint.Render(stack.URL)))
	} else {
		lines = m.overview()
	}

	rows := m.height - len(lines) - 2
	lines = append(lines, m.rule(rows))
	lines = append(lines, m.logWindow(rows)...)
	lines = append(lines, m.footer())

	for index, line := range lines {
		lines[index] = ansi.Truncate(line, m.width, "…")
	}

	view := tea.NewView(strings.Join(lines[:min(len(lines), m.height)], "\n"))
	view.AltScreen = true
	// For the wheel and the trackpad to scroll the log.
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "run-stack"

	return view
}

// logRows is how many lines of log the window has room for.
func (m model) logRows() int {
	if m.zoomed && m.current() != nil {
		return max(m.height-3, 0)
	}

	return max(m.height-len(m.overview())-2, 0)
}

// overview is everything above the log: the stacks, and the selected one's ports.
func (m model) overview() []string {
	running := 0

	for _, stack := range m.stacks {
		if stack.Status != state.Stopped {
			running++
		}
	}

	title := bold.Render("run-stack") + faint.Render(fmt.Sprintf("  %d running, %d stopped", running, len(m.stacks)-running))

	if m.loaded && !m.proxyUp {
		title += yellow.Render("  proxy not running")
	}

	lines := []string{title, ""}

	if len(m.stacks) == 0 {
		empty := "no stacks yet: run-stack up in a checkout starts one"

		if !m.loaded {
			empty = "loading"
		}

		return append(lines, faint.Render(empty))
	}

	widest := 0

	for _, stack := range m.stacks {
		widest = max(widest, len(stack.ID))
	}

	// A long list scrolls around the selected stack and leaves the log some room.
	room := max(3, m.height/3)
	first := 0

	for index, stack := range m.stacks {
		if stack.ID == m.selected {
			first = max(0, min(index-room/2, len(m.stacks)-room))
		}
	}

	for index := first; index < min(first+room, len(m.stacks)); index++ {
		stack := m.stacks[index]
		status := stack.Status

		if m.busy[stack.ID] != "" {
			status = m.busy[stack.ID]
		}

		name := fmt.Sprintf("%-*s", widest, stack.ID)
		pointer := "  "

		if stack.ID == m.selected {
			pointer, name = cyan.Render("› "), bold.Render(name)
		}

		lines = append(lines, fmt.Sprintf("%s%s %s  %s  %s", pointer, dot(stack.Status), name, statusStyle(stack.Status).Render(fmt.Sprintf("%-10s", status)), faint.Render(stack.URL)))
	}

	if hidden := len(m.stacks) - room; hidden > 0 {
		lines = append(lines, faint.Render(fmt.Sprintf("  and %d more", hidden)))
	}

	stack := m.current()

	if stack == nil {
		return lines
	}

	where := shorten(stack.Dir)

	if stack.FolderMissing {
		where += red.Render("  folder deleted")
	}

	if since, err := time.Parse(time.RFC3339, stack.StartedAt); err == nil && stack.Status == state.Running {
		where += faint.Render("  up " + age(time.Since(since)))
	}

	lines = append(lines, "", "  "+where)
	widest = 0

	for _, port := range stack.Ports {
		widest = max(widest, len(port.Name))
	}

	for _, port := range stack.Ports {
		mark, detail := faint.Render("○"), port.URL

		switch {
		case !port.Declared:
			mark, detail = yellow.Render("●"), yellow.Render("not in run-stack.toml: "+port.Command)
		case port.Listening:
			mark = green.Render("●")
		case stack.Status == state.Running:
			detail = strings.TrimSpace(detail + faint.Render("  nothing listening"))
		}

		lines = append(lines, fmt.Sprintf("  %s %-*s  %-5d  %s", mark, widest, port.Name, port.Port, detail))
	}

	return lines
}

func (m model) rule(rows int) string {
	label := " logs "

	if m.scroll > 0 {
		label = fmt.Sprintf(" logs, %d lines below ", m.scroll)
	}

	return faint.Render("──" + label + strings.Repeat("─", max(m.width-len([]rune(label))-2, 0)))
}

// logWindow is exactly rows lines: the part of the log the scroll position shows.
func (m model) logWindow(rows int) []string {
	rows = max(rows, 0)
	window := make([]string, rows)
	end := len(m.logs) - max(0, min(m.scroll, len(m.logs)-rows))
	start := max(end-rows, 0)

	for index, line := range m.logs[start:end] {
		window[index] = " " + line
	}

	if len(m.logs) == 0 && rows > 0 && m.selected != "" {
		window[0] = faint.Render(" no output yet")
	}

	return window
}

func (m model) footer() string {
	switch {
	case m.forgetting != "":
		return yellow.Render(fmt.Sprintf(" forget %s? its ports are released and its log deleted. y to confirm", m.forgetting))
	case m.failed:
		return red.Render(" " + m.note)
	case m.note != "":
		return " " + m.note
	}

	toggle := "stop"

	if stack := m.current(); stack != nil && stack.Status == state.Stopped {
		toggle = "start"
	}

	keys := []string{"↑↓ select", "s " + toggle, "r restart", "o open", "l logs", "wheel scroll", "x forget", "q quit"}

	if m.zoomed {
		keys[0], keys[4] = "↑↓ scroll", "esc back"
	}

	return faint.Render(" " + strings.Join(keys, " · "))
}

func dot(status string) string {
	return statusStyle(status).Render("●")
}

func statusStyle(status string) lipgloss.Style {
	switch status {
	case state.Running:
		return green
	case state.Orphaned:
		return yellow
	default:
		return faint
	}
}

func shorten(dir string) string {
	if user, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, user+"/") {
		return "~" + strings.TrimPrefix(dir, user)
	}

	return dir
}

func age(elapsed time.Duration) string {
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(elapsed.Hours())/24, int(elapsed.Hours())%24)
	}
}
