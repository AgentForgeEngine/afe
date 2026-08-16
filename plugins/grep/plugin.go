// Package grep implements the grep tool plugin: searches file contents
// by regex or literal text, respecting .gitignore-style hidden-file
// skipping and returning matching file paths.
package grep

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/pflag"

	"mycli/pkg/plugin"
)

const (
	maxResults   = 100
	maxFileSize  = 1024 * 1024
	maxLineLen   = 500
	contextLines = 3
)

type pluginArgs struct {
	Pattern     string `json:"pattern"`
	Include     string `json:"include"`
	Path        string `json:"path"`
	LiteralText bool   `json:"literal_text"`
}

type grepPlugin struct {
	enabled  bool
	rootDirs rootList
	rootSet  map[string]bool
}

var instance = &grepPlugin{}

func init() {
	plugin.Register(instance)
}

func (p *grepPlugin) Name() string { return "grep" }

func (p *grepPlugin) IsEnabled() bool { return p.enabled }

func (p *grepPlugin) RegisterFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&p.enabled, "grep", false, "enable the grep tool (search file contents)")
	fs.Var(&p.rootDirs, "grep-root", "directory the grep tool may search (repeatable; default: current directory)")
}

func (p *grepPlugin) Description() string {
	return "Search file contents for a regex pattern (or literal text when literal_text is true). " +
		"Returns matching lines with file path and line number, up to 100 matches. " +
		"Hidden files and .gitignore-style ignored directories are skipped. " +
		"Use the include parameter to restrict by file pattern (e.g. \"*.go\")."
}

func (p *grepPlugin) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The regex pattern to search for in file contents.",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "Optional file glob pattern to include (e.g. \"*.go\").",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "The directory to search in. Defaults to the current working directory.",
			},
			"literal_text": map[string]any{
				"type":        "boolean",
				"description": "If true, treat the pattern as literal text (escape regex characters). Defaults to false.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (p *grepPlugin) Init() error {
	if len(p.rootDirs) == 0 {
		p.rootDirs = rootList{"."}
	}
	p.rootSet = make(map[string]bool, len(p.rootDirs))
	for _, dir := range p.rootDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve --grep-root %q: %w", dir, err)
		}
		p.rootSet[abs] = true
	}
	return nil
}

type hit struct {
	path string
	line int
	text string
}

func (p *grepPlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
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

	pattern := args.Pattern
	if args.LiteralText {
		pattern = regexp.QuoteMeta(pattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("compile pattern %q: %w", args.Pattern, err)
	}

	var includeRe *regexp.Regexp
	if args.Include != "" {
		includeRe, err = compileGlob(args.Include)
		if err != nil {
			return "", err
		}
	}

	var hits []hit
	var filesScanned int

	walkErr := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
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
		if d.IsDir() {
			return nil
		}
		if includeRe != nil && !includeRe.MatchString(name) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxFileSize {
			return nil
		}

		fileHits, scanErr := scanFile(path, re)
		if scanErr != nil {
			return nil
		}
		if len(fileHits) == 0 {
			return nil
		}
		filesScanned++
		hits = append(hits, fileHits...)
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("walk %q: %w", abs, walkErr)
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].path != hits[j].path {
			return hits[i].path < hits[j].path
		}
		return hits[i].line < hits[j].line
	})
	truncated := false
	if len(hits) > maxResults {
		hits = hits[:maxResults]
		truncated = true
	}

	if len(hits) == 0 {
		return fmt.Sprintf("No matches for %q under %s", args.Pattern, abs), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d matching line(s) for %q under %s:\n", len(hits), args.Pattern, abs)
	if truncated {
		fmt.Fprintf(&sb, "... [matches truncated, showing first %d]\n", maxResults)
	}
	for _, h := range hits {
		fmt.Fprintf(&sb, "%s:%d: %s\n", h.path, h.line, truncateLine(h.text))
	}
	return sb.String(), nil
}

func (p *grepPlugin) resolveDir(raw string) (string, error) {
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
	return "", fmt.Errorf("path %q is outside the allowed grep roots (%s)", abs, strings.Join(roots, ", "))
}

func scanFile(path string, re *regexp.Regexp) ([]hit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hits []hit
	buf := make([]byte, 64*1024)
	lineNo := 0
	var partial strings.Builder
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			data := append(append([]byte{}, partial.String()...), chunk...)
			lines := strings.Split(string(data), "\n")
			partial.Reset()
			for i, line := range lines {
				isLast := readErr != nil && i == len(lines)-1
				if isLast {
					partial.WriteString(line)
					continue
				}
				lineNo++
				if re.MatchString(line) {
					hits = append(hits, hit{path: path, line: lineNo, text: line})
					if len(hits) >= maxResults*2 {
						return hits, nil
					}
				}
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return hits, readErr
			}
			break
		}
	}
	return hits, nil
}

// compileGlob converts a glob like "*.go" or "*.{ts,tsx}" into an
// anchored regexp.
func compileGlob(glob string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString("^")
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch c {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteByte('.')
		case '{':
			j := i + 1
			var parts []string
			var cur strings.Builder
			closed := false
			for ; j < len(runes); j++ {
				switch runes[j] {
				case ',':
					parts = append(parts, cur.String())
					cur.Reset()
				case '}':
					parts = append(parts, cur.String())
					j++
					closed = true
				default:
					cur.WriteRune(runes[j])
				}
				if closed {
					break
				}
			}
			if !closed {
				sb.WriteString(regexp.QuoteMeta(string(c)))
				continue
			}
			sb.WriteByte('(')
			for k, part := range parts {
				if k > 0 {
					sb.WriteByte('|')
				}
				sb.WriteString(globToRegexp(part))
			}
			sb.WriteByte(')')
			i = j - 1
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

func globToRegexp(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteByte('.')
		default:
			sb.WriteString(regexp.QuoteMeta(string(s[i])))
		}
	}
	return sb.String()
}

func truncateLine(s string) string {
	s = strings.TrimRight(s, "\r")
	if len(s) > maxLineLen {
		return s[:maxLineLen] + "..."
	}
	return s
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
