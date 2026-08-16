// Package config loads afe configuration using viper from
// ~/.afe/config.yaml, AFE_* environment variables, and CLI flags.
//
// It also manages the afe home directory:
//
//	~/.afe/            home directory
//	~/.afe/config.yaml user configuration (optional, created on first run)
//	~/.afe/afe/        canonical working copy of the afe source tree,
//	                   used as the build directory when installing
//	                   remote plugins
//	~/go/bin/afe       where the rebuilt afe binary is installed
package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// RepoURL is the canonical afe source repository, cloned into
// ~/.afe/afe on first run so remote plugin installs always build
// against a known, clean checkout. It uses the SSH form; if the
// repository is made public, an HTTPS URL also works (set AFE_REPO_URL
// or edit the config to override).
const RepoURL = "git@github.com:AgentForgeEngine/afe.git"

// Config holds application settings. Precedence (highest wins):
// explicit overrides (CLI flags) > environment (AFE_*) > config file
// (~/.afe/config.yaml) > defaults.
type Config struct {
	LLM    LLMConfig    `mapstructure:"llm"`
	Plugin PluginConfig `mapstructure:"plugin"`
}

// LLMConfig holds connection settings for the local OpenAI-compatible
// LLM endpoint.
type LLMConfig struct {
	URL   string `mapstructure:"url"`
	Model string `mapstructure:"model"`
}

// PluginConfig holds settings for remote plugin management.
type PluginConfig struct {
	// URLs are git URLs, local paths, or "owner/repo" shorthands for
	// external afe plugins to install.
	URLs []string `mapstructure:"urls"`
	// Dir is where downloaded plugin sources are placed, relative to
	// the working copy in ~/.afe/afe (or absolute).
	Dir string `mapstructure:"dir"`
	// Host is the default git host for "owner/repo" shorthand sources.
	Host string `mapstructure:"host"`
}

// DefaultPluginDir is the default plugin source directory inside the
// working copy.
const DefaultPluginDir = "plugins-remote"

// DefaultHost is the default git host for shorthand sources.
const DefaultHost = "github.com"

// HomeDir returns the afe home directory (~/.afe), or the path named
// by the AFE_HOME environment variable when set.
func HomeDir() (string, error) {
	if p := os.Getenv("AFE_HOME"); p != "" {
		return filepath.Abs(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".afe"), nil
}

// RepoDir returns the canonical working copy location, ~/.afe/afe.
func RepoDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "afe"), nil
}

// BinPath returns the install location of the afe binary, ~/go/bin/afe.
func BinPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "go", "bin", "afe"), nil
}

// Load reads configuration. The config file is optional; a missing
// file is not an error. AFE_CONFIG can point viper at an explicit
// config file path.
func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if home, err := HomeDir(); err == nil {
		v.AddConfigPath(home)
	}
	if p := os.Getenv("AFE_CONFIG"); p != "" {
		v.SetConfigFile(p)
	}

	v.SetEnvPrefix("AFE")
	// Map "llm.url" to AFE_LLM_URL (dots become underscores, uppercased
	// by viper) so nested config keys work with env vars.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("llm.url", "http://localhost:8080")
	v.SetDefault("llm.model", "")
	v.SetDefault("plugin.dir", DefaultPluginDir)
	v.SetDefault("plugin.host", DefaultHost)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Plugin.Dir == "" {
		cfg.Plugin.Dir = DefaultPluginDir
	}
	if cfg.Plugin.Host == "" {
		cfg.Plugin.Host = DefaultHost
	}
	return &cfg, nil
}

// repoURL returns the clone URL for the afe source repository, honoring
// the AFE_REPO_URL override.
func repoURL() string {
	if p := os.Getenv("AFE_REPO_URL"); p != "" {
		return p
	}
	return RepoURL
}

// EnsureHome prepares the afe home directory on first use: it creates
// ~/.afe, writes a template config.yaml if missing, and clones the afe
// repository into ~/.afe/afe if that checkout does not exist yet.
//
// It returns the list of human-readable actions performed (for
// display) and is safe to call repeatedly. Cloning requires git on
// PATH.
func EnsureHome(ctx context.Context) ([]string, error) {
	home, err := HomeDir()
	if err != nil {
		return nil, err
	}
	var actions []string

	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", home, err)
	}

	path := filepath.Join(home, "config.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeTemplate(path); err != nil {
			return nil, err
		}
		actions = append(actions, "wrote "+path)
	}

	repoDir := filepath.Join(home, "afe")
	if !dirExists(repoDir) || !exists(filepath.Join(repoDir, ".git")) {
		if _, err := exec.LookPath("git"); err != nil {
			return nil, fmt.Errorf("git is required to clone the afe repository into %s, but git was not found on PATH", repoDir)
		}
		url := repoURL()
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, repoDir)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("clone %s into %s: %w", url, repoDir, err)
		}
		actions = append(actions, "cloned "+url+" into "+repoDir)
	}

	return actions, nil
}

// PullRepo updates the working copy at dir with a fast-forward pull.
// A missing or dirty checkout is left alone.
func PullRepo(ctx context.Context, dir string) error {
	if !exists(filepath.Join(dir, ".git")) {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "pull", "--ff-only", "origin", "master")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Non-fatal: the working copy may have local changes (installed
		// plugins) that prevent a fast-forward.
		fmt.Fprintf(os.Stderr, "note: could not fast-forward %s (%v); using existing checkout\n", dir, err)
	}
	return nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// exists reports whether the path exists (file or directory).
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeTemplate(path string) error {
	content := `# afe configuration file (~/.afe/config.yaml)
#
# Precedence: CLI flags > AFE_* environment variables > this file > defaults.
# Environment variable examples:
#   AFE_LLM_URL="http://localhost:8080"
#   AFE_LLM_MODEL="llama3"
#   AFE_PLUGIN_URLS='["audstanley/afe-plugin-foo"]'

llm:
  # Base URL of the local OpenAI-compatible LLM endpoint
  # (llama.cpp server, Ollama with OpenAI compat, LM Studio, ...).
  url: "http://localhost:8080"
  # Model name sent in the completion request.
  model: "llama3"

plugin:
  # External afe plugins to install. Entries may be:
  #   owner/repo            shorthand, resolved against "host" below
  #   https://.../repo.git  full git URL (or git@host:owner/repo.git)
  #   /local/path           existing local checkout
  urls: []
  # Default git host for owner/repo shorthand entries.
  host: "github.com"
  # Where downloaded plugin sources live, relative to ~/.afe/afe.
  dir: "plugins-remote"
`
	return os.WriteFile(path, []byte(content), 0o644)
}
