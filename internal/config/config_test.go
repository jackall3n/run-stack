package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
name = "Shop"
command = "bun run dev"
domain = "shop.local"

[ports]
WEB_PORT = { route = "" }
JOBS_PORT = { route = "jobs" }
INSPECTOR_PORT = { optional = true }

[env]
APP_URL = "{url}"
JOBS_PUBLIC = "{url:JOBS_PORT}"
JOBS_URL = "http://localhost:{JOBS_PORT}"
DATABASE_URL = "postgresql://localhost:5432/{db}"
`

func TestParseKeepsPortOrderAndDefaults(t *testing.T) {
	config, err := Parse(sample, "fallback")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(config.PortNames, ","); got != "WEB_PORT,JOBS_PORT,INSPECTOR_PORT" {
		t.Errorf("port order = %s", got)
	}

	if config.Name != "shop" {
		t.Errorf("name = %q", config.Name)
	}

	if config.Ports["INSPECTOR_PORT"].Route != nil || !config.Ports["INSPECTOR_PORT"].Optional {
		t.Error("INSPECTOR_PORT should be unrouted and optional")
	}

	unnamed, err := Parse(`command = "x"`, "My Repo")
	if err != nil {
		t.Fatal(err)
	}

	if unnamed.Name != "my-repo" || unnamed.Domain != "my-repo."+DefaultTLD() {
		t.Errorf("fallback name %q, domain %q", unnamed.Name, unnamed.Domain)
	}
}

func TestParseRejectsMistakes(t *testing.T) {
	for name, source := range map[string]string{
		"unknown key":      "command = \"x\"\ncomand = \"y\"",
		"missing command":  "name = \"x\"",
		"shared route":     "command = \"x\"\n[ports]\nA = { route = \"\" }\nB = { route = \"\" }",
		"bad port name":    "command = \"x\"\n[ports]\nweb-port = {}",
		"bad route":        "command = \"x\"\n[ports]\nA = { route = \"Not A Host\" }",
		"dashboard's host": "command = \"x\"\ndomain = \"stack.local\"",
	} {
		if _, err := Parse(source, "fallback"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestEnvForAWorktree(t *testing.T) {
	config, err := Parse(sample, "fallback")
	if err != nil {
		t.Fatal(err)
	}

	stack := New(config, "/work/api-worker", "/work/shop", "api-worker")

	env, err := stack.Env(map[string]int{"WEB_PORT": 41001, "JOBS_PORT": 41002, "INSPECTOR_PORT": 41003}, 443)
	if err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]string{
		"WEB_PORT":       "41001",
		"APP_URL":        "https://api-worker.shop.local",
		"JOBS_PUBLIC":    "https://jobs.api-worker.shop.local",
		"JOBS_URL":       "http://localhost:41002",
		"DATABASE_URL":   "postgresql://localhost:5432/shop_api_worker",
		"RUN_STACK_HOST": "api-worker.shop.local",
		"RUN_STACK_ID":   "shop-api-worker",
	} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}

	stack.Config.Env["BROKEN"] = "{nope}"

	if _, err := stack.Env(map[string]int{}, 443); err == nil {
		t.Error("an unknown placeholder should be an error")
	}
}

func TestLocateMainCheckoutWorktreeAndNestedConfig(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "shop")
	nested := filepath.Join(repo, "apps", "web")

	run := func(dir string, args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	os.MkdirAll(nested, 0o755)
	os.WriteFile(filepath.Join(nested, FileName), []byte(`command = "x"`), 0o644)
	run(repo, "init", "-q")
	run(repo, "add", "-A")
	run(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init")
	run(repo, "worktree", "add", "-q", filepath.Join(root, "Feature One"), "-b", "feature")

	dir, main, worktree, err := Locate(filepath.Join(nested))
	if err != nil {
		t.Fatal(err)
	}

	if dir != real(nested) || main != real(nested) || worktree != "" {
		t.Errorf("main checkout: dir %s, main %s, worktree %q", dir, main, worktree)
	}

	inWorktree := filepath.Join(root, "Feature One", "apps", "web")

	dir, main, worktree, err = Locate(inWorktree)
	if err != nil {
		t.Fatal(err)
	}

	if dir != real(inWorktree) || main != real(nested) || worktree != "feature-one" {
		t.Errorf("worktree: dir %s, main %s, worktree %q", dir, main, worktree)
	}
}
