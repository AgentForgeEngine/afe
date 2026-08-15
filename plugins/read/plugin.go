// Package read implements the read tool plugin: reads file contents with
// line numbers, optional offset and limit.
package read

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mycli/pkg/plugin"
)

const (
	defaultLimit = 200
	maxReadBytes = 200 * 1024
)

type pluginArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type readPlugin struct {
	enabled  bool
	rootDirs rootList
	rootSet  map[string]bool
}

var instance = &readPlugin{}

func init() {
	plugin.Register(instance)
}

func (p *readPlugin) Name() string { return "read" }

func (p *readPlugin) IsEnabled() bool { return p.enabled }

func (p *readPlugin) RegisterFlags(fs *flag.FlagSet) {
	fs.BoolVar(&p.enabled, "read", false, "enable the read tool (read file contents)")
	fs.Var(&p.rootDirs, "read-root", "directory the read tool may access (repeatable; default: current directory)")
}

func (p *readPlugin) Description() string {
	return "Read a file and return its contents with line numbers. " +
		"Use offset (0-based line number) and limit (number of lines, default 200) to page through large files. " +
		"Always read a file before editing or modifying it."
}

func (p *readPlugin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or relative path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "0-based line number to start reading from (default 0).",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of lines to read (default 200).",
			},
		},
		"required": []string{"path"},
	}
}

func (p *readPlugin) Init() error {
	if len(p.rootDirs) == 0 {
		p.rootDirs = rootList{"."}
	}
	p.rootSet = make(map[string]bool, len(p.rootDirs))
	for _, dir := range p.rootDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve --read-root %q: %w", dir, err)
		}
		p.rootSet[abs] = true
	}
	return nil
}

func (p *readPlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args pluginArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("argument \"path\" is required")
	}
	if args.Offset < 0 || args.Limit <= 0 {
		args.Offset = 0
	}
	if args.Limit == 0 {
		args.Limit = defaultLimit
	}

	path, err := p.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}

	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
	}

	lines := strings.Split(string(data), "\n")
	end := args.Offset + args.Limit
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := args.Offset; i < end; i++ {
		fmt.Fprintf(&sb, "%6d| %s\n", i+1, lines[i])
	}
	header := fmt.Sprintf("File: %s (lines %d-%d of %d shown)\n", path, args.Offset+1, end, len(lines))
	if len(data) == maxReadBytes {
		header += "Warning: file larger than 200KB; only the first 200KB was read.\n"
	}
	if args.Offset >= len(lines) {
		return header + "offset is beyond the end of the file", nil
	}
	return header + sb.String(), nil
}

func (p *readPlugin) resolvePath(raw string) (string, error) {
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
	return "", fmt.Errorf("path %q is outside the allowed read roots (%s)", abs, strings.Join(roots, ", "))
}

// rootList is a repeatable string flag value.
type rootList []string

func (l *rootList) String() string { return strings.Join(*l, ",") }

func (l *rootList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
