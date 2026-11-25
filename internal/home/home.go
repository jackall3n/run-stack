// Package home knows where run-stack keeps its files (~/.run-stack) and the machine-wide settings.
package home

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Dir is ~/.run-stack, or $RUN_STACK_HOME.
func Dir() string {
	if dir := os.Getenv("RUN_STACK_HOME"); dir != "" {
		return dir
	}

	dir, _ := os.UserHomeDir()

	return filepath.Join(dir, ".run-stack")
}

func StateFile() string { return filepath.Join(Dir(), "state.json") }
func LockFile() string  { return filepath.Join(Dir(), "state.lock") }
func LogDir() string    { return filepath.Join(Dir(), "logs") }
func ProxyLog() string  { return filepath.Join(LogDir(), "proxy.log") }
func ProxyPID() string  { return filepath.Join(Dir(), "proxy.pid") }
func CACert() string    { return filepath.Join(Dir(), "ca", "rootCA.pem") }
func CAKey() string     { return filepath.Join(Dir(), "ca", "rootCA-key.pem") }

// LogFile is where a stack's output goes.
func LogFile(id string) string { return filepath.Join(LogDir(), id+".log") }

// Settings are machine-wide, read from ~/.run-stack/config.json. Every key is optional.
type Settings struct {
	HTTPSPort int    `json:"https_port"`
	HTTPPort  int    `json:"http_port"`
	PortRange [2]int `json:"port_range"`
}

func LoadSettings() Settings {
	// The range sits below macOS's ephemeral ports (49152+), so the OS never hands these out itself.
	settings := Settings{HTTPSPort: 443, HTTPPort: 80, PortRange: [2]int{41000, 48999}}

	if data, err := os.ReadFile(filepath.Join(Dir(), "config.json")); err == nil {
		_ = json.Unmarshal(data, &settings)
	}

	return settings
}
