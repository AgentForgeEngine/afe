package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"mycli/pkg/config"
	"mycli/pkg/llm"
	"mycli/pkg/plugin"
	"mycli/pkg/pluginmgr"

	_ "mycli/plugins"
)

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s --prompt \"...\" [flags]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "LLM and endpoint settings default from ~/.afe/config.yaml (or AFE_* env vars).")
		fmt.Fprintln(os.Stderr, "\nBase flags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nPlugin flags are registered dynamically by plugins in plugins/.")
		fmt.Fprintln(os.Stderr, "Only plugins enabled via their flags are sent to the LLM.")
	}

	var (
		prompt    = fs.String("prompt", "", "the user prompt to send to the LLM (required)")
		llmURL    = fs.String("llm-url", "", "base URL of the local OpenAI-compatible LLM endpoint (overrides config)")
		model     = fs.String("model", "", "model name for the completion request (overrides config)")
		timeout   = fs.Duration("timeout", 10*time.Minute, "overall context timeout for the run")
		install   = fs.Bool("install", false, "install plugins from the config's plugin.urls that are missing, then exit")
		initConfig = fs.Bool("init-config", false, "write a template ~/.afe/config.yaml if missing, then exit")
	)

	plugin.BindFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *initConfig {
		path, err := config.WriteDefaultConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if path == "" {
			fmt.Fprintln(os.Stderr, "~/.afe/config.yaml already exists; nothing written")
		} else {
			fmt.Fprintln(os.Stderr, "wrote", path)
		}
		os.Exit(0)
	}

	if *install {
		runInstall(cfg)
		os.Exit(0)
	}

	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "error: --prompt is required")
		fs.Usage()
		os.Exit(2)
	}

	resolvedURL, resolvedModel := cfg.LLM.URL, cfg.LLM.Model
	if *llmURL != "" {
		resolvedURL = *llmURL
	}
	if *model != "" {
		resolvedModel = *model
	}
	if resolvedURL == "" || resolvedModel == "" {
		fmt.Fprintln(os.Stderr, "error: LLM url and model must be set via --llm-url/--model, AFE_LLM_URL/AFE_LLM_MODEL, or ~/.afe/config.yaml")
		os.Exit(2)
	}

	// Offer to install any configured plugins that are not already
	// compiled into the binary (no manifest entry).
	offerInstallMissing(cfg)

	active, err := plugin.ResolveActivePlugins()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client := llm.NewClient(resolvedURL, resolvedModel)
	answer, err := client.Run(ctx, *prompt, active)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(answer)
}

// runInstall installs every plugin URL from the config that is not yet
// in the manifest, without prompting, and exits.
func runInstall(cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	dir, err := cfg.PluginDirPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot resolve plugin dir: %v\n", err)
		os.Exit(1)
	}
	repoRoot, err := pluginmgr.FindRepoRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	manifest, err := pluginmgr.LoadManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read plugin manifest: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Plugin.URLs) == 0 {
		fmt.Fprintln(os.Stderr, "No plugin.urls configured in ~/.afe/config.yaml; nothing to install.")
		return
	}

	for _, u := range cfg.Plugin.URLs {
		if manifest.IsInstalled(u) {
			fmt.Fprintf(os.Stderr, "Already installed, skipping: %s\n", u)
			continue
		}
		fmt.Fprintf(os.Stderr, "Installing %s ...\n", u)
		pkgPath, err := pluginmgr.Install(ctx, u, dir, repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: install %q: %v\n", u, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Installed %s (package %s).\n", u, pkgPath)
	}
	fmt.Fprintln(os.Stderr, "\nDone. The afe binary was rebuilt; re-run afe to use the new plugin(s).")
}

// offerInstallMissing checks the config's plugin URLs against what is
// already registered in the binary (via the manifest) and, for any
// missing ones, prompts the user to download, build, and install them.
// After a successful install it prints a notice and exits, since the
// current binary does not yet contain the new plugin.
func offerInstallMissing(cfg *config.Config) {
	urls := cfg.Plugin.URLs
	if len(urls) == 0 {
		return
	}

	dir, err := cfg.PluginDirPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot resolve plugin dir: %v\n", err)
		return
	}

	manifest, err := pluginmgr.LoadManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot read plugin manifest: %v\n", err)
		return
	}

	var missing []string
	for _, u := range urls {
		if !manifest.IsInstalled(u) {
			missing = append(missing, u)
		}
	}
	if len(missing) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\nafe: the following configured plugins are not installed in this binary:")
	for _, u := range missing {
		fmt.Fprintf(os.Stderr, "  - %s\n", u)
	}
	fmt.Fprint(os.Stderr, "Download, build, and install them now? [y/N] ")

	answer := pluginmgr.ReadChoice("n")
	if answer != "y" && answer != "yes" {
		return
	}

	repoRoot, err := pluginmgr.FindRepoRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	for _, u := range missing {
		fmt.Fprintf(os.Stderr, "Installing %s ...\n", u)
		pkgPath, err := pluginmgr.Install(ctx, u, dir, repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: install %q: %v\n", u, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Installed %s (package %s).\n", u, pkgPath)
	}

	fmt.Fprintln(os.Stderr, "\nThe afe binary was rebuilt with the new plugin(s).")
	fmt.Fprintln(os.Stderr, "Quit and re-run afe to use them. Exiting.")
	os.Exit(0)
}
