package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mycli/pkg/config"
	"mycli/pkg/llm"
	"mycli/pkg/plugin"
	"mycli/pkg/pluginmgr"

	_ "mycli/plugins"
)

var (
	cfg    *config.Config
	loaded bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "afe",
		Short: "Single-shot LLM CLI with flag-enabled tool plugins",
		Long: `afe sends a prompt to a local OpenAI-compatible LLM endpoint and runs a
tool-calling loop until the model produces a final answer.

Tool capabilities are flag-enabled plugins. Only plugins enabled via their
flags are sent to the LLM. LLM settings come from ~/.afe/config.yaml
(create it with 'afe config init'), AFE_* environment variables, or the
--llm-url/--model flags (highest precedence).`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE:       ensureHome,
		RunE:          runPrompt,
	}

	var (
		prompt  string
		llmURL  string
		model   string
		timeout time.Duration
	)
	rootCmd.Flags().StringVarP(&prompt, "prompt", "p", "", "the user prompt to send to the LLM (required)")
	rootCmd.Flags().StringVar(&llmURL, "llm-url", "", "LLM endpoint URL (overrides config)")
	rootCmd.Flags().StringVar(&model, "model", "", "model name (overrides config)")
	rootCmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "overall context timeout for the run")

	installCmd := newInstallCmd()
	configCmd := &cobra.Command{Use: "config", Short: "Manage the afe configuration"}
	configCmd.AddCommand(newConfigInitCmd())

	rootCmd.AddCommand(installCmd, configCmd)

	plugin.BindFlags(rootCmd.Flags())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ensureHome loads the configuration and prepares ~/.afe (template
// config plus the canonical repo checkout) on first use.
func ensureHome(cmd *cobra.Command, args []string) error {
	if loaded {
		return nil
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	cfg = c
	loaded = true

	home, err := config.HomeDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	actions, err := config.EnsureHome(ctx)
	if err != nil {
		return err
	}
	for _, a := range actions {
		fmt.Fprintf(os.Stderr, "afe: %s (home: %s)\n", a, home)
	}
	return nil
}

func loadConfig() *config.Config {
	if cfg == nil {
		c, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		cfg = c
	}
	return cfg
}

func runPrompt(cmd *cobra.Command, args []string) error {
	c := loadConfig()

	prompt, _ := cmd.Flags().GetString("prompt")
	if prompt == "" {
		return fmt.Errorf("--prompt/-p is required (or run 'afe --help')")
	}

	url, _ := cmd.Flags().GetString("llm-url")
	model, _ := cmd.Flags().GetString("model")
	if url == "" {
		url = c.LLM.URL
	}
	if model == "" {
		model = c.LLM.Model
	}
	if url == "" || model == "" {
		return fmt.Errorf("LLM url and model must be set via --llm-url/--model, AFE_LLM_URL/AFE_LLM_MODEL, or ~/.afe/config.yaml")
	}

	if err := offerInstallMissing(); err != nil {
		return err
	}

	active, err := plugin.ResolveActivePlugins()
	if err != nil {
		return err
	}

	timeout, _ := cmd.Flags().GetDuration("timeout")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := llm.NewClient(url, model)
	answer, err := client.Run(ctx, prompt, active)
	if err != nil {
		return err
	}
	fmt.Println(answer)
	return nil
}

func newInstallCmd() *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "install [source ...]",
		Short: "Download, build, and install external afe plugins",
		Long: `Download, build, and install external afe plugins.

Each source may be:
  owner/repo            shorthand, resolved against --host (default: plugin.host
                        from config, i.e. github.com) -> https://<host>/owner/repo.git
  https://.../repo.git  full git URL (or git@host:owner/repo.git)
  /local/path           existing local checkout

With no arguments, all plugin.urls entries from the config are installed.

Sources are placed under <plugin.dir> (default plugins-remote) inside the
canonical working copy at ~/.afe/afe, compiled into the afe module, the
binary is rebuilt there, and the new binary is installed to ~/go/bin/afe.
The current process then exits; run afe again to use the new plugins.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := loadConfig()

			sources := args
			if len(sources) == 0 {
				sources = c.Plugin.URLs
			}
			if len(sources) == 0 {
				fmt.Fprintln(os.Stderr, "No plugin sources given and plugin.urls is empty in config; nothing to install.")
				return nil
			}

			if err := pluginmgr.CheckGo(); err != nil {
				return err
			}

			repoRoot, err := config.RepoDir()
			if err != nil {
				return err
			}
			binPath, err := config.BinPath()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			// Keep the canonical checkout current when possible.
			config.PullRepo(ctx, repoRoot)

			h := host
			if h == "" {
				h = c.Plugin.Host
			}

			for _, src := range sources {
				resolved, err := pluginmgr.ResolveSource(src, h)
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Installing %s (%s) ...\n", src, resolved)
				pkgPath, err := pluginmgr.Install(ctx, resolved, repoRoot, c.Plugin.Dir, binPath)
				if err != nil {
					return fmt.Errorf("install %q: %w", src, err)
				}
				fmt.Fprintf(os.Stderr, "Installed %s (package %s).\n", src, pkgPath)
			}

			fmt.Fprintf(os.Stderr, "\nNew binary installed to %s.\n", binPath)
			if !pathInPATH(binPath) {
				fmt.Fprintln(os.Stderr, "Note: the directory containing the new binary is not in your PATH; add it or invoke it directly.")
			}
			fmt.Fprintln(os.Stderr, "Re-run afe to use the new plugin(s). Exiting.")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "git host for owner/repo shorthand sources (default: plugin.host from config)")
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create ~/.afe/config.yaml and clone the afe working copy",
		Long: `Create the afe home directory:
  ~/.afe/config.yaml  commented config template (only if missing)
  ~/.afe/afe/         canonical afe source checkout, used as the build
                      directory for plugin installs

Existing files are never overwritten. Requires git on PATH.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := config.HomeDir()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			actions, err := config.EnsureHome(ctx)
			if err != nil {
				return err
			}
			if len(actions) == 0 {
				fmt.Fprintf(os.Stderr, "%s is already set up; nothing written.\n", home)
				return nil
			}
			for _, a := range actions {
				fmt.Fprintf(os.Stderr, "afe: %s\n", a)
			}
			return nil
		},
	}
}

// offerInstallMissing checks the config's plugin URLs against the
// installed manifest in the canonical working copy and, for any that
// are missing, prompts to download, build, and install them. After a
// successful install it exits, since the new binary lives at
// ~/go/bin/afe and the current process cannot load it.
func offerInstallMissing() error {
	c := loadConfig()
	if len(c.Plugin.URLs) == 0 {
		return nil
	}

	repoRoot, err := config.RepoDir()
	if err != nil {
		return err
	}
	pluginDir := c.Plugin.Dir
	if !filepath.IsAbs(pluginDir) {
		pluginDir = filepath.Join(repoRoot, pluginDir)
	}

	manifest, err := pluginmgr.LoadManifest(pluginDir)
	if err != nil {
		return fmt.Errorf("read plugin manifest: %w", err)
	}

	var missing []string
	for _, u := range c.Plugin.URLs {
		if !manifest.IsInstalled(u) {
			missing = append(missing, u)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "\nafe: the following configured plugins are not installed in this binary:")
	for _, u := range missing {
		fmt.Fprintf(os.Stderr, "  - %s\n", u)
	}
	fmt.Fprint(os.Stderr, "Download, build, and install them now? [y/N] ")

	answer := pluginmgr.ReadChoice("n")
	if answer != "y" && answer != "yes" {
		return nil
	}

	binPath, err := config.BinPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	for _, u := range missing {
		fmt.Fprintf(os.Stderr, "Installing %s ...\n", u)
		pkgPath, err := pluginmgr.Install(ctx, u, repoRoot, c.Plugin.Dir, binPath)
		if err != nil {
			return fmt.Errorf("install %q: %w", u, err)
		}
		fmt.Fprintf(os.Stderr, "Installed %s (package %s).\n", u, pkgPath)
	}

	fmt.Fprintf(os.Stderr, "\nNew binary installed to %s.\n", binPath)
	fmt.Fprintln(os.Stderr, "Quit and re-run afe to use them. Exiting.")
	os.Exit(0)
	return nil
}

func pathInPATH(p string) bool {
	dir := filepath.Dir(p)
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(d) == dir {
			return true
		}
	}
	return false
}
