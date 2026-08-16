// Package glob implements the glob tool plugin: finds files by name
// pattern, sorted by modification time.
package glob

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"mycli/pkg/plugin"
)

const maxResults = 100

type pluginArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

type globPlugin struct {
	enabled  bool
	rootDirs rootList
	rootSet  map[string]bool
}

var instance = &globPlugin{}

func init() {
	plugin.Register(instance)
}

func (p *globPlugin) Name() string { return "glob" }

func (p *globPlugin) IsEnabled() bool { return p.enabled }

func (p *globPlugin) RegisterFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&p.enabled, "glob", false, "enable the glob tool (find files by name pattern)")
	fs.Var(&p.rootDirs, "glob-root", "directory the glob tool may search (repeatable; default: current directory)")
}

func (p *globPlugin) Description() string {
	return "Find files whose names match a glob pattern (e.g. \"*.go\", \"**/*.test.js\"). " +
		"Returns up to 100 matching file paths sorted by modification time (newest first). " +
		"Hidden files are skipped. Use to locate files by name; use grep to search file contents."
}

func (p *globPlugin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The glob pattern to match file names against (e.g. \"*.go\").",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "The directory to search in. Defaults to the current working directory.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (p *globPlugin) Init() error {
	if len(p.rootDirs) == 0 {
		p.rootDirs = rootList{"."}
	}
	p.rootSet = make(map[string]bool, len(p.rootDirs))
	for _, dir := range p.rootDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve --glob-root %q: %w", dir, err)
		}
		p.rootSet[abs] = true
	}
	return nil
}

type match struct {
	path string
	mod  time.Time
}

func (p *globPlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args pluginArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("argument \"pattern\" is required")
	}

	baseDir := args.Path
	if baseDir == "" {
		baseDir = "."
	}
	abs, err := p.resolveDir(baseDir)
	if err != nil {
		return "", err
	}

	pattern := filepath.Join(abs, args.Pattern)

	var matches []match
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if isHidden(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if matched(path, pattern) {
			info, statErr := d.Info()
			var mod time.Time
			if statErr == nil {
				mod = info.ModTime()
			}
			matches = append(matches, match{path: path, mod: mod})
			if len(matches) >= maxResults*5 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %q: %w", abs, err)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].mod.Equal(matches[j].mod) {
			return matches[i].path < matches[j].path
		}
		return matches[i].mod.After(matches[j].mod)
	})
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No files matched pattern %q under %s", args.Pattern, abs), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d file(s) matching %q under %s (newest first):\n", len(matches), args.Pattern, abs)
	for _, m := range matches {
		sb.WriteString("  ")
		sb.WriteString(m.path)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func (p *globPlugin) resolveDir(raw string) (string, error) {
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
	return "", fmt.Errorf("path %q is outside the allowed glob roots (%s)", abs, strings.Join(roots, ", "))
}

// matched reports whether the full path matches the pattern. Unlike
// filepath.Match, it understands "**" as matching any number of path
// segments (including zero).
func matched(path, pattern string) bool {
	pathSegs := splitSegments(path)
	patSegs := splitSegments(pattern)
	return segMatch(pathSegs, patSegs)
}

func splitSegments(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func segMatch(path, pat []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(path); i++ {
			if segMatch(path[i:], pat[1:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], path[0])
	if err != nil || !ok {
		return false
	}
	return segMatch(path[1:], pat[1:])
}

func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// rootList is a repeatable string flag value.
type rootList []string

func (l *rootList) String() string { return strings.Join(*l, ",") }

func (l *rootList) Type() string { return "string" }
func (l *rootList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
