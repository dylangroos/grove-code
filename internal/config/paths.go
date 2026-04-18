// Package config resolves XDG paths and loads/saves grove-code config + state.
package config

import (
	"os"
	"path/filepath"
)

const appName = "grove-code"

func xdg(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func ConfigDir() string { return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), appName) }
func DataDir() string   { return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), appName) }
func StateDir() string  { return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), appName) }

func ConfigFile() string { return filepath.Join(ConfigDir(), "config.yaml") }
func StateFile() string  { return filepath.Join(StateDir(), "state.json") }

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// ExpandHome replaces a leading "~/" with the user's home directory.
func ExpandHome(p string) string {
	if len(p) < 2 || p[:2] != "~/" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
