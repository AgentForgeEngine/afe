// Package config loads afe configuration using viper from
// ~/.afe/config.yaml, AFE_* environment variables, and CLI flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

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
	URLs []string `mapstructure:"urls"`
	Dir  string   `mapstructure:"dir"`
}

// PluginDir is the default location for downloaded remote plugin
// sources.
const PluginDir = "plugins-remote"

// Load reads configuration. The config file is optional; a missing
// file is not an error. AFE_CONFIG can point viper at an explicit
// config file path.
func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if home, err := os.UserHomeDir(); err == nil {
		v.AddConfigPath(filepath.Join(home, ".afe"))
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
	v.SetDefault("plugin.dir", PluginDir)

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
		cfg.Plugin.Dir = PluginDir
	}
	return &cfg, nil
}

// PluginDirPath returns the absolute path of the plugin download
// directory, resolving a relative dir against the working directory.
func (c *Config) PluginDirPath() (string, error) {
	abs, err := filepath.Abs(c.Plugin.Dir)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// WriteDefaultConfig creates ~/.afe/config.yaml with a commented
// template if it does not already exist. It returns the path written,
// or "" if the file already existed.
func WriteDefaultConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".afe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return "", nil
	}

	content := `# afe configuration file (~/.afe/config.yaml)
#
# Precedence: CLI flags > AFE_* environment variables > this file > defaults.
# Environment variable examples:
#   AFE_LLM_URL="http://localhost:8080"
#   AFE_LLM_MODEL="llama3"
#   AFE_PLUGIN_URLS='["https://github.com/me/afe-plugin-foo.git"]'

llm:
  # Base URL of the local OpenAI-compatible LLM endpoint
  # (llama.cpp server, Ollama with OpenAI compat, LM Studio, ...).
  url: "http://localhost:8080"
  # Model name sent in the completion request.
  model: "llama3"

plugin:
  # Git URLs (or local paths) of external afe plugins to download and
  # install when they are not already compiled into the binary.
  urls: []
  # Where downloaded plugin sources are placed (relative to the
  # working directory or absolute).
  dir: "plugins-remote"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
