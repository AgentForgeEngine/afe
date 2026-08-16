package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withHome(t *testing.T, fn func(home string)) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AFE_HOME", home)
	t.Setenv("AFE_CONFIG", "")
	t.Setenv("HOME", t.TempDir())
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
		if cfg.Plugin.Dir != DefaultPluginDir {
			t.Errorf("default plugin dir = %q", cfg.Plugin.Dir)
		}
		if cfg.Plugin.Host != DefaultHost {
			t.Errorf("default plugin host = %q", cfg.Plugin.Host)
		}
	})
}

func TestLoadFromConfigFile(t *testing.T) {
	withHome(t, func(home string) {
		writeFile(t, filepath.Join(home, "config.yaml"), `
llm:
  url: "http://127.0.0.1:11434"
  model: "qwen3"
plugin:
  urls:
    - "audstanley/mytool"
  dir: "my-plugins"
  host: "gitlab.com"
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
		if len(cfg.Plugin.URLs) != 1 || cfg.Plugin.URLs[0] != "audstanley/mytool" {
			t.Errorf("urls = %v", cfg.Plugin.URLs)
		}
		if cfg.Plugin.Dir != "my-plugins" {
			t.Errorf("dir = %q", cfg.Plugin.Dir)
		}
		if cfg.Plugin.Host != "gitlab.com" {
			t.Errorf("host = %q", cfg.Plugin.Host)
		}
	})
}

func TestEnvOverridesFile(t *testing.T) {
	withHome(t, func(home string) {
		writeFile(t, filepath.Join(home, "config.yaml"), `
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

func TestEnsureHome(t *testing.T) {
	// Use a temp home; git may or may not be available, so skip the
	// clone assertion when it is not.
	home := t.TempDir()
	t.Setenv("AFE_HOME", home)
	t.Setenv("AFE_CONFIG", "")
	t.Setenv("HOME", t.TempDir())

	_, err := EnsureHome(t.Context())
	if err != nil {
		t.Skipf("EnsureHome requires git/network: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Errorf("config.yaml not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "afe", "go.mod")); err != nil {
		t.Errorf("afe repo not cloned: %v", err)
	}

	// Idempotent: second run makes no changes and reports no actions.
	actions, err := EnsureHome(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Errorf("second EnsureHome reported actions: %v", actions)
	}
}
