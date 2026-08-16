// Package ls implements the ls tool plugin: lists a directory tree with
// indentation, skipping hidden files and common system directories.
package ls

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/pflag"

	"mycli/pkg/plugin"
)

const (
	maxDepth   = 10
	maxEntries = 1000
)

var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "__pycache__": true, ".cache": true,
}

type pluginArgs struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

type lsPlugin struct {
	enabled  bool
	rootDirs rootList
	rootSet  map[string]bool
}

var instance = &lsPlugin{}

func init() {
	plugin.Register(instance)
}

func (p *lsPlugin) Name() string { return "ls" }

func (p *lsPlugin) IsEnabled() bool { return p.enabled }

func (p *lsPlugin) RegisterFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&p.enabled, "ls", false, "enable the ls tool (list directory contents as a tree)")
	fs.Var(&p.rootDirs, "ls-root", "directory the ls tool may list (repeatable; default: current directory)")
}

func (p *lsPlugin) Description() string {
	return "List a directory as an indented tree, showing files and subdirectories. " +
		"Hidden files and common system directories (.git, node_modules, etc.) are skipped. " +
		"Use depth to limit traversal (default 10, max 10). Returns up to 1000 entries."
}

func (p *lsPlugin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The path to the directory to list. Defaults to the current working directory.",
			},
			"depth": map[string]any{
				"type":        "integer",
				"description": "Maximum depth to traverse (default 10).",
			},
		},
		"required": []string{},
	}
}

func (p *lsPlugin) Init() error {
	if len(p.rootDirs) == 0 {
		p.rootDirs = rootList{"."}
	}
	p.rootSet = make(map[string]bool, len(p.rootDirs))
	for _, dir := range p.rootDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve --ls-root %q: %w", dir, err)
		}
		p.rootSet[abs] = true
	}
	return nil
}

type entry struct {
	path  string
	isDir bool
}

func (p *lsPlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args pluginArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}

	baseDir := args.Path
	if baseDir == "" {
		baseDir = "."
	}
	abs, err := p.resolveDir(baseDir)
	if err != nil {
		return "", err
	}

	depth := args.Depth
	if depth <= 0 || depth > maxDepth {
		depth = 10
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", abs, err)
	}
	if !info.IsDir() {
		return fmt.Sprintf("%s (file, %d bytes)", abs, info.Size()), nil
	}

	var entries []entry
	var count int
	var truncated bool

	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		if level > depth || count >= maxEntries {
			return
		}
		dirEntries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return
		}
		sort.Slice(dirEntries, func(i, j int) bool {
			if dirEntries[i].IsDir() != dirEntries[j].IsDir() {
				return dirEntries[i].IsDir()
			}
			return dirEntries[i].Name() < dirEntries[j].Name()
		})
		for _, d := range dirEntries {
			if count >= maxEntries {
				truncated = true
				return
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				continue
			}
			entries = append(entries, entry{path: filepath.Join(dir, name), isDir: d.IsDir()})
			count++
			if d.IsDir() {
				walk(filepath.Join(dir, name), level+1)
			}
		}
	}
	walk(abs, 0)

	if count == 0 {
		return fmt.Sprintf("%s (empty directory)", abs), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s/\n", abs)
	relBase := abs + string(filepath.Separator)
	for _, e := range entries {
		rel := strings.TrimPrefix(e.path, relBase)
		depthOf := strings.Count(rel, string(filepath.Separator))
		sb.WriteString(strings.Repeat("  ", depthOf+1))
		sb.WriteString(rel)
		if e.isDir {
			sb.WriteByte('/')
		}
		sb.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&sb, "... [listing truncated at %d entries]", maxEntries)
	}
	return sb.String(), nil
}

func (p *lsPlugin) resolveDir(raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", raw, err)
	}
	for root := range p.rootSet {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return abs, nil
		}
	}
	roots := make([]string, 0, len(p.rootSet))
	for r := range p.rootSet {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	return "", fmt.Errorf("path %q is outside the allowed ls roots (%s)", abs, strings.Join(roots, ", "))
}

// rootList is a repeatable string flag value.
type rootList []string

func (l *rootList) String() string { return strings.Join(*l, ",") }

func (l *rootList) Type() string { return "string" }
func (l *rootList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
