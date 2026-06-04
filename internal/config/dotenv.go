package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// GlobalDotEnvPath returns the absolute path to the user-global ~/.env file
// used to persist API keys across all projects. Returns "" if the home
// directory cannot be resolved.
func GlobalDotEnvPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".env")
}

func loadDotEnv() {
	if home, err := os.UserHomeDir(); err == nil {
		loadDotEnvFile(filepath.Join(home, ".env"))
	}
}

// loadDotEnvFile reads one .env file (if present) and sets any keys not already
// present in the environment. Lenient, zero-dependency parsing.
func loadDotEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
