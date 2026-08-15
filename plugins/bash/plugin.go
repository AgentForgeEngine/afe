// Package bash implements the bash tool plugin: executes shell commands
// natively in-process.
package bash

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mycli/pkg/plugin"
)

const maxOutput = 30000

type pluginArgs struct {
	Command   string `json:"command"`
	Directory string `json:"working_dir"`
}

type bashPlugin struct {
	enabled   bool
	allowDirs allowDirList
	allowMap  map[string]bool
}

var instance = &bashPlugin{}

func init() {
	plugin.Register(instance)
}

func (p *bashPlugin) Name() string { return "bash" }

func (p *bashPlugin) IsEnabled() bool { return p.enabled }

func (p *bashPlugin) RegisterFlags(fs *flag.FlagSet) {
	fs.BoolVar(&p.enabled, "bash", false, "enable the bash tool (execute shell commands)")
	fs.Var(&p.allowDirs, "bash-allow-dir", "restrict the bash tool to this working directory (repeatable)")
}

func (p *bashPlugin) Description() string {
	return "Execute a shell command (Bash-compatible) and return its combined output and exit code. " +
		"Use forward slashes in paths. Commands run with a hard timeout. " +
		"Use for builds, tests, git commands, and general system operations. " +
		"Set working_dir to run in a specific directory; it defaults to the current directory."
}

func (p *bashPlugin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Optional working directory for the command. Defaults to the current directory.",
			},
		},
		"required": []string{"command"},
	}
}

func (p *bashPlugin) Init() error {
	p.allowMap = make(map[string]bool, len(p.allowDirs))
	for _, dir := range p.allowDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve --bash-allow-dir %q: %w", dir, err)
		}
		p.allowMap[abs] = true
	}
	return nil
}

func (p *bashPlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args pluginArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("argument \"command\" is required")
	}

	workingDir, err := p.resolveWorkingDir(args.Directory)
	if err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", args.Command)
	cmd.Dir = workingDir
	cmd.Env = os.Environ()

	start := time.Now()
	out, runErr := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Command timed out after %v. Partial output:\n%s", 2*time.Minute, truncate(string(out), maxOutput)), nil
	}

	var result string
	if runErr == nil {
		result = "exit code: 0"
	} else if exitErr, ok := runErr.(*exec.ExitError); ok {
		result = fmt.Sprintf("exit code: %d", exitErr.ExitCode())
	} else {
		return "", fmt.Errorf("run command: %w", runErr)
	}
	result += fmt.Sprintf("\nworking_dir: %s\nelapsed: %v\n", workingDir, elapsed.Round(time.Millisecond))
	if len(out) > 0 {
		result += "\n" + truncate(string(out), maxOutput)
	}
	return result, nil
}

func (p *bashPlugin) resolveWorkingDir(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("working directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", abs)
	}
	if len(p.allowMap) > 0 {
		matched := false
		for allowed := range p.allowMap {
			if abs == allowed || hasPrefix(abs, allowed) {
				matched = true
				break
			}
		}
		if !matched {
			return "", fmt.Errorf("working directory %q is outside the allowed --bash-allow-dir paths", abs)
		}
	}
	return abs, nil
}

func hasPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix && path[len(prefix)] == '/'
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [output truncated, %d bytes total]", len(s))
}

// allowDirList is a repeatable string flag value.
type allowDirList []string

func (l *allowDirList) String() string {
	var sb strings.Builder
	for i, d := range *l {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(d)
	}
	return sb.String()
}

func (l *allowDirList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
