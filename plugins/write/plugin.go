// Package write implements the write tool plugin: creates or overwrites
// files, including creating parent directories.
package write

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"mycli/pkg/plugin"
)

type pluginArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writePlugin struct {
	enabled  bool
	rootDirs rootList
	rootSet  map[string]bool
}

var instance = &writePlugin{}

func init() {
	plugin.Register(instance)
}

func (p *writePlugin) Name() string { return "write" }

func (p *writePlugin) IsEnabled() bool { return p.enabled }

func (p *writePlugin) RegisterFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&p.enabled, "write", false, "enable the write tool (create or overwrite files)")
	fs.Var(&p.rootDirs, "write-root", "directory the write tool may write into (repeatable; default: current directory)")
}

func (p *writePlugin) Description() string {
	return "Create or overwrite a file with the given content. Parent directories are created automatically. " +
		"Use for creating new files or replacing an entire file's content. " +
		"Returns the absolute path written and byte count on success."
}

func (p *writePlugin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or relative path of the file to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The full content to write to the file.",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (p *writePlugin) Init() error {
	if len(p.rootDirs) == 0 {
		p.rootDirs = rootList{"."}
	}
	p.rootSet = make(map[string]bool, len(p.rootDirs))
	for _, dir := range p.rootDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve --write-root %q: %w", dir, err)
		}
		p.rootSet[abs] = true
	}
	return nil
}

func (p *writePlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args pluginArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("argument \"path\" is required")
	}
	if args.Content == "" {
		return "", fmt.Errorf("argument \"content\" is required")
	}

	abs, err := p.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("create parent directories for %q: %w", abs, err)
	}

	existing, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if string(existing) == args.Content {
			return fmt.Sprintf("path: %s\naction: unchanged (content identical)", abs), nil
		}
		if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
			return "", fmt.Errorf("write %q: %w", abs, err)
		}
		return fmt.Sprintf("path: %s\naction: overwritten\nbytes: %d\nlines: %d", abs, len(args.Content), strings.Count(args.Content, "\n")+1), nil
	case os.IsNotExist(err):
	default:
		return "", fmt.Errorf("stat %q: %w", abs, err)
	}

	if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %w", abs, err)
	}

	return fmt.Sprintf("path: %s\naction: created\nbytes: %d\nlines: %d", abs, len(args.Content), strings.Count(args.Content, "\n")+1), nil
}

func (p *writePlugin) resolvePath(raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", raw, err)
	}
	dir := filepath.Dir(abs)
	for root := range p.rootSet {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return abs, nil
		}
	}
	roots := make([]string, 0, len(p.rootSet))
	for r := range p.rootSet {
		roots = append(roots, r)
	}
	return "", fmt.Errorf("path %q is outside the allowed write roots (%s)", abs, strings.Join(roots, ", "))
}

// rootList is a repeatable string flag value.
type rootList []string

func (l *rootList) String() string { return strings.Join(*l, ",") }

func (l *rootList) Type() string { return "string" }
func (l *rootList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
