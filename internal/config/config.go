// Package config reads a repo's run-stack.toml and works out which stack a folder is.
package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the config file, at the root of a checkout or in a folder inside it.
const FileName = "run-stack.toml"

// DashboardHosts are served by the proxy itself, so no stack may use them.
var DashboardHosts = map[string]bool{"stack.local": true, "stack.localhost": true}

type PortSpec struct {
	// Route is the subdomain this port is served at: "" is the stack's own host, "jobs" is
	// jobs.<host>. Nil means the port gets a number but no URL.
	Route *string `toml:"route"`
	// HostHeader is sent upstream instead of the public host, for dev servers that only answer to "localhost".
	HostHeader string `toml:"host_header"`
	// Optional ports are not waited for by "run-stack wait".
	Optional bool `toml:"optional"`
}

type Config struct {
	Name    string              `toml:"name"`
	Command string              `toml:"command"`
	Domain  string              `toml:"domain"`
	Copy    []string            `toml:"copy"`
	Ports   map[string]PortSpec `toml:"ports"`
	Env     map[string]string   `toml:"env"`
	Hooks   struct {
		PreUp    []string `toml:"pre_up"`
		PostDown []string `toml:"post_down"`
	} `toml:"hooks"`

	// PortNames holds the ports in the order the file declares them.
	PortNames []string `toml:"-"`
}

type Stack struct {
	Config Config
	// Dir is the folder being run: the one holding the config.
	Dir string
	// Main is the same folder in the main checkout, which worktrees copy untracked files from.
	Main string
	// Worktree is "" for the main checkout, otherwise a slug of the worktree's folder name.
	Worktree string
	// ID is unique across the machine: "<name>" or "<name>-<worktree>".
	ID   string
	Host string
}

var (
	notSlug     = regexp.MustCompile(`[^a-z0-9]+`)
	portName    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	routeName   = regexp.MustCompile(`^([a-z0-9-]+(\.[a-z0-9-]+)*)?$`)
	placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_:]*)\}`)
)

// DefaultTLD is ".local" on macOS, where run-stack makes those names resolve in every program.
// Elsewhere it is ".localhost", which browsers resolve by themselves.
func DefaultTLD() string {
	if runtime.GOOS == "darwin" {
		return "local"
	}

	return "localhost"
}

func Slug(value string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(value), "-"), "-")
}

func git(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.Output()

	return strings.TrimSpace(string(out)), err == nil
}

func real(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}

	return path
}

// findUp returns the nearest folder holding a config, from dir up to and including top ("" for no limit).
func findUp(dir, top string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
			return dir, true
		}

		if dir == top || dir == filepath.Dir(dir) {
			return "", false
		}

		dir = filepath.Dir(dir)
	}
}

// Locate finds the folder to run from cwd, the same folder in the main checkout, and the worktree
// name. The config may sit at the checkout root or in a folder inside it (a monorepo package).
func Locate(cwd string) (dir, main, worktree string, err error) {
	cwd = real(cwd)

	top, inGit := git(cwd, "rev-parse", "--show-toplevel")

	if !inGit {
		dir, ok := findUp(cwd, "")

		if !ok {
			return "", "", "", fmt.Errorf("no %s in %s or above it", FileName, cwd)
		}

		return dir, dir, "", nil
	}

	top = real(top)
	mainTop := top

	// The first entry of `git worktree list` is the main checkout, whichever worktree asks.
	if list, ok := git(cwd, "worktree", "list", "--porcelain"); ok {
		first, _, _ := strings.Cut(strings.TrimPrefix(list, "worktree "), "\n")
		mainTop = real(first)
	}

	if top != mainTop {
		worktree = Slug(filepath.Base(top))
	}

	dir, ok := findUp(cwd, top)

	if !ok {
		// No config yet: the checkout root is where one would go.
		return top, mainTop, worktree, nil
	}

	inside, _ := filepath.Rel(top, dir)

	return dir, filepath.Join(mainTop, inside), worktree, nil
}

// Parse reads a config. fallbackName names the stack when the file doesn't.
func Parse(source, fallbackName string) (Config, error) {
	var config Config

	meta, err := toml.Decode(source, &config)
	if err != nil {
		return config, fmt.Errorf("%s: %w", FileName, err)
	}

	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return config, fmt.Errorf("%s: unknown key %q", FileName, unknown[0].String())
	}

	if config.Name == "" {
		config.Name = fallbackName
	}

	if config.Name = Slug(config.Name); config.Name == "" {
		return config, fmt.Errorf("%s: name is empty", FileName)
	}

	if strings.TrimSpace(config.Command) == "" {
		return config, fmt.Errorf("%s: command is required", FileName)
	}

	if config.Domain == "" {
		config.Domain = config.Name + "." + DefaultTLD()
	}

	if config.Domain = strings.ToLower(config.Domain); DashboardHosts[config.Domain] {
		return config, fmt.Errorf("%s: %s is the dashboard's address. Choose another name or domain", FileName, config.Domain)
	}

	for _, key := range meta.Keys() {
		if len(key) == 2 && key[0] == "ports" {
			config.PortNames = append(config.PortNames, key[1])
		}
	}

	routes := map[string]string{}

	for _, name := range config.PortNames {
		if !portName.MatchString(name) {
			return config, fmt.Errorf("%s: port %q must be an env variable name such as WEB_PORT", FileName, name)
		}

		route := config.Ports[name].Route

		if route == nil {
			continue
		}

		if !routeName.MatchString(*route) {
			return config, fmt.Errorf(`%s: ports.%s.route must be "" or a subdomain such as "jobs"`, FileName, name)
		}

		if other, taken := routes[*route]; taken {
			return config, fmt.Errorf("%s: %s and %s have the same route", FileName, other, name)
		}

		routes[*route] = name
	}

	return config, nil
}

// Load works out the stack for cwd and reads its config.
func Load(cwd string) (*Stack, error) {
	dir, main, worktree, err := Locate(cwd)
	if err != nil {
		return nil, err
	}

	source, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(`%s has no %s. Run "run-stack init" to create one`, dir, FileName)
	} else if err != nil {
		return nil, err
	}

	config, err := Parse(string(source), filepath.Base(main))
	if err != nil {
		return nil, err
	}

	return New(config, dir, main, worktree), nil
}

// New builds the stack a config describes for one checkout.
func New(config Config, dir, main, worktree string) *Stack {
	stack := &Stack{Config: config, Dir: dir, Main: main, Worktree: worktree, ID: config.Name, Host: config.Domain}

	if worktree != "" {
		stack.ID = config.Name + "-" + worktree
		stack.Host = worktree + "." + config.Domain
	}

	return stack
}

// RouteHost is the public host of a route: the stack's own for "", a subdomain of it otherwise.
func (stack *Stack) RouteHost(route string) string {
	if route == "" {
		return stack.Host
	}

	return route + "." + stack.Host
}

// Origin is the https URL of a host on the proxy.
func Origin(host string, httpsPort int) string {
	if httpsPort == 443 {
		return "https://" + host
	}

	return "https://" + host + ":" + strconv.Itoa(httpsPort)
}

// Env is what a stack's processes get on top of their own environment: one variable per port,
// RUN_STACK_* describing the stack, and the config's [env] with its {placeholders} filled in.
//
// Placeholders: {host}, {url}, {id}, {name}, {worktree}, {db} (the id with underscores, usable as
// a database name), {dir}, {main}, any port name, and {url:PORT_NAME} for a routed port's URL.
func (stack *Stack) Env(ports map[string]int, httpsPort int) (map[string]string, error) {
	values := map[string]string{
		"host":     stack.Host,
		"url":      Origin(stack.Host, httpsPort),
		"id":       stack.ID,
		"name":     stack.Config.Name,
		"worktree": stack.Worktree,
		"db":       strings.ReplaceAll(stack.ID, "-", "_"),
		"dir":      stack.Dir,
		"main":     stack.Main,
	}
	env := map[string]string{
		"RUN_STACK_ID":       stack.ID,
		"RUN_STACK_HOST":     stack.Host,
		"RUN_STACK_URL":      values["url"],
		"RUN_STACK_WORKTREE": stack.Worktree,
	}

	for _, name := range stack.Config.PortNames {
		values[name] = strconv.Itoa(ports[name])
		env[name] = values[name]

		if route := stack.Config.Ports[name].Route; route != nil {
			values["url:"+name] = Origin(stack.RouteHost(*route), httpsPort)
		}
	}

	for key, template := range stack.Config.Env {
		var unknown string

		env[key] = placeholder.ReplaceAllStringFunc(template, func(match string) string {
			value, ok := values[match[1:len(match)-1]]

			if !ok {
				unknown = match
			}

			return value
		})

		if unknown != "" {
			return nil, fmt.Errorf("%s: env.%s uses unknown placeholder %s", FileName, key, unknown)
		}
	}

	return env, nil
}
