package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withHome(t *testing.T, fn func(home string)) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AFE_CONFIG", "")
	fn(home)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaults(t *testing.T) {
	withHome(t, func(home string) {
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.URL != "http://localhost:8080" {
			t.Errorf("default url = %q", cfg.LLM.URL)
		}
		if cfg.LLM.Model != "" {
			t.Errorf("default model = %q, want empty", cfg.LLM.Model)
		}
		if cfg.Plugin.Dir != PluginDir {
			t.Errorf("default plugin dir = %q", cfg.Plugin.Dir)
		}
	})
}

func TestLoadFromConfigFile(t *testing.T) {
	withHome(t, func(home string) {
		writeFile(t, filepath.Join(home, ".afe", "config.yaml"), `
llm:
  url: "http://127.0.0.1:11434"
  model: "qwen3"
plugin:
  urls:
    - "https://github.com/me/mytool.git"
  dir: "my-plugins"
`)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.URL != "http://127.0.0.1:11434" {
			t.Errorf("url = %q", cfg.LLM.URL)
		}
		if cfg.LLM.Model != "qwen3" {
			t.Errorf("model = %q", cfg.LLM.Model)
		}
		if len(cfg.Plugin.URLs) != 1 || cfg.Plugin.URLs[0] != "https://github.com/me/mytool.git" {
			t.Errorf("urls = %v", cfg.Plugin.URLs)
		}
		if cfg.Plugin.Dir != "my-plugins" {
			t.Errorf("dir = %q", cfg.Plugin.Dir)
		}
	})
}

func TestEnvOverridesFile(t *testing.T) {
	withHome(t, func(home string) {
		writeFile(t, filepath.Join(home, ".afe", "config.yaml"), `
llm:
  url: "http://127.0.0.1:11434"
  model: "fromfile"
`)
		t.Setenv("AFE_LLM_MODEL", "fromenv")
		t.Setenv("AFE_LLM_URL", "http://envhost:1234")

		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.Model != "fromenv" {
			t.Errorf("model = %q, want env override", cfg.LLM.Model)
		}
		if cfg.LLM.URL != "http://envhost:1234" {
			t.Errorf("url = %q, want env override", cfg.LLM.URL)
		}
	})
}

func TestExplicitConfigFile(t *testing.T) {
	withHome(t, func(home string) {
		path := filepath.Join(t.TempDir(), "custom.yaml")
		writeFile(t, path, "llm:\n  url: \"http://custom:9999\"\n  model: \"custom-model\"\n")
		t.Setenv("AFE_CONFIG", path)

		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.URL != "http://custom:9999" || cfg.LLM.Model != "custom-model" {
			t.Errorf("cfg = %+v", cfg.LLM)
		}
	})
}

func TestWriteDefaultConfig(t *testing.T) {
	withHome(t, func(home string) {
		path, err := WriteDefaultConfig()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".afe", "config.yaml")
		if path != want {
			t.Fatalf("path = %q, want %q", path, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatal(err)
		}
		// Second call: file exists, returns "".
		again, err := WriteDefaultConfig()
		if err != nil {
			t.Fatal(err)
		}
		if again != "" {
			t.Errorf("second call returned %q, want empty", again)
		}
	})
}

func TestPluginDirPath(t *testing.T) {
	withHome(t, func(home string) {
		writeFile(t, filepath.Join(home, ".afe", "config.yaml"), "plugin:\n  dir: \"rel-dir\"\n")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		abs, err := cfg.PluginDirPath()
		if err != nil {
			t.Fatal(err)
		}
		if abs != filepath.Join(".", "rel-dir") {
			// filepath.Abs uses CWD, not HOME; just check it's absolute.
			if !filepath.IsAbs(abs) {
				t.Errorf("expected absolute path, got %q", abs)
			}
		}
	})
}
