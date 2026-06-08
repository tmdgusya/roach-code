package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadForEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roach-code.toml")
	custom := `default_model = "custom"
[[providers]]
name = "custom"
kind = "openai"
base_url = "https://x"
model = "m"
api_key_env = "X_KEY"
`
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	// Existing file: its providers/default override the built-in defaults, so a
	// reconfigure preserves the user's setup.
	cfg := LoadForEdit(path)
	if cfg.DefaultModel != "custom" {
		t.Errorf("default_model = %q, want custom", cfg.DefaultModel)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "custom" {
		t.Errorf("providers = %v, want a single custom provider", cfg.Providers)
	}

	// Missing file: falls back to the built-in defaults.
	if cfg := LoadForEdit(filepath.Join(dir, "absent.toml")); cfg.DefaultModel != Default().DefaultModel {
		t.Errorf("missing-file default = %q, want %q", cfg.DefaultModel, Default().DefaultModel)
	}
}

func isolateConfigEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return home
}

func TestUserConfigPathDefaultsToDotConfigWhenXDGUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Chdir(t.TempDir())

	primary := filepath.Join(home, ".config", "roach-code", "config.toml")
	if got := UserConfigPath(); got != primary {
		t.Fatalf("UserConfigPath() = %q, want %q", got, primary)
	}
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte(`default_model = "dotconfig"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "dotconfig" {
		t.Fatalf("Load default_model = %q, want dotconfig", cfg.DefaultModel)
	}
	if got := SourcePath(); got != primary {
		t.Fatalf("SourcePath() = %q, want %q", got, primary)
	}
}

func TestLoadFallsBackToOSUserConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Chdir(t.TempDir())

	primary := UserConfigPath()
	osPath := osUserConfigPath()
	if osPath == "" || osPath == primary {
		t.Skip("OS user config path is the primary path on this platform")
	}
	if err := os.MkdirAll(filepath.Dir(osPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(osPath, []byte(`default_model = "osuser"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "osuser" {
		t.Fatalf("Load default_model = %q, want osuser", cfg.DefaultModel)
	}
	if got := SourcePath(); got != osPath {
		t.Fatalf("SourcePath() = %q, want %q", got, osPath)
	}
}

func TestLoadAndSourcePathUseLegacyHomeConfig(t *testing.T) {
	home := isolateConfigEnv(t)
	project := t.TempDir()
	t.Chdir(project)

	homePath := filepath.Join(home, "roach-code.toml")
	if err := os.WriteFile(homePath, []byte(`default_model = "home"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := SourcePath(); got != homePath {
		t.Fatalf("SourcePath() = %q, want legacy home config %q", got, homePath)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "home" {
		t.Fatalf("Load default_model = %q, want home", cfg.DefaultModel)
	}
}

func TestLoadConfigPriority(t *testing.T) {
	home := isolateConfigEnv(t)
	project := t.TempDir()
	t.Chdir(project)

	writeConfig := func(path, model string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`default_model = "`+model+`"
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHomeConfig := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`default_model = "home"
[[plugins]]
name = "legacy-home"
command = "home-plugin"
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	homePath := filepath.Join(home, "roach-code.toml")
	userPath := userConfigPath()
	projectPath := "roach-code.toml"
	writeHomeConfig(homePath)
	writeConfig(userPath, "user")
	writeConfig(projectPath, "project")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "project" {
		t.Fatalf("with all configs, default_model = %q, want project", cfg.DefaultModel)
	}
	if len(cfg.Plugins) != 0 {
		t.Fatalf("with user config present, legacy home plugins leaked: %+v", cfg.Plugins)
	}
	if got := SourcePath(); got != projectPath {
		t.Fatalf("SourcePath with project config = %q, want %q", got, projectPath)
	}

	if err := os.Remove(projectPath); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "user" {
		t.Fatalf("without project config, default_model = %q, want user", cfg.DefaultModel)
	}
	if len(cfg.Plugins) != 0 {
		t.Fatalf("with user config present, legacy home plugins leaked: %+v", cfg.Plugins)
	}
	if got := SourcePath(); got != userPath {
		t.Fatalf("SourcePath with user config = %q, want %q", got, userPath)
	}

	if err := os.Remove(userPath); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "home" {
		t.Fatalf("without project/user config, default_model = %q, want home", cfg.DefaultModel)
	}
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].Name != "legacy-home" {
		t.Fatalf("without project/user config, plugins = %+v, want legacy home plugin", cfg.Plugins)
	}
	if got := SourcePath(); got != homePath {
		t.Fatalf("SourcePath with home config = %q, want %q", got, homePath)
	}
}
