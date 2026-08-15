// Package pluginmgr manages external afe plugins referenced by URL in
// the config file. It downloads plugin sources, wires them into the
// afe module, rebuilds the binary, and tracks what is already
// installed.
package pluginmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry records an installed external plugin.
type Entry struct {
	URL         string    `json:"url"`
	Name        string    `json:"name"`
	PackagePath string    `json:"package_path"`
	InstalledAt time.Time `json:"installed_at"`
}

// Manifest is the set of installed external plugins, persisted as
// installed.json inside the plugin directory.
type Manifest []Entry

const manifestFile = "installed.json"

// ManifestPath returns the path of the manifest file in dir.
func ManifestPath(dir string) string { return filepath.Join(dir, manifestFile) }

// LoadManifest reads the manifest from dir. A missing file yields an
// empty manifest.
func LoadManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestFile, err)
	}
	return m, nil
}

// Save writes the manifest to dir.
func (m Manifest) Save(dir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestFile), append(data, '\n'), 0o644)
}

// IsInstalled reports whether url is already present in the manifest.
func (m Manifest) IsInstalled(url string) bool {
	for _, e := range m {
		if e.URL == url {
			return true
		}
	}
	return false
}

// NameForURL derives a stable directory/package name from a plugin URL
// or local path.
func NameForURL(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	base := filepath.Base(trimmed)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "plugin"
	}
	base = strings.ReplaceAll(base, "_", "-")
	base = strings.ToLower(base)
	if base == "" {
		base = "plugin"
	}
	return base
}

// Install downloads the plugin at url into dir/<name>, rewrites its
// imports to belong to the afe module, adds a blank import to the
// remote aggregator in repoRoot, rebuilds the afe binary, and records
// the installation in the manifest.
//
// repoRoot and dir must be absolute paths. It returns the installed
// package path (e.g. mycli/plugins-remote/foo).
func Install(ctx context.Context, url, dir, repoRoot string) (string, error) {
	name := NameForURL(url)
	dest := filepath.Join(dir, name)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create plugin dir %q: %w", dir, err)
	}

	modulePath, err := fetch(ctx, url, dest)
	if err != nil {
		return "", err
	}

	pkgPath, err := packagePathFor(repoRoot, dir, name)
	if err != nil {
		return "", err
	}

	// Drop the nested module files so the sources compile as part of
	// the afe module, then rewrite self-imports to the afe module path.
	for _, f := range []string{"go.mod", "go.sum"} {
		os.Remove(filepath.Join(dest, f))
	}
	if modulePath != "" && modulePath != "mycli" {
		if err := RewriteImports(dest, modulePath, pkgPath); err != nil {
			return "", fmt.Errorf("rewrite imports in %q: %w", dest, err)
		}
	}

	if err := AddAggregatorImport(repoRoot, pkgPath); err != nil {
		return "", err
	}

	if err := Rebuild(ctx, repoRoot); err != nil {
		return "", fmt.Errorf("rebuild afe binary: %w", err)
	}

	m, err := LoadManifest(dir)
	if err != nil {
		return "", err
	}
	m = append(m, Entry{
		URL:         url,
		Name:        name,
		PackagePath: pkgPath,
		InstalledAt: time.Now(),
	})
	m.Sort()
	if err := m.Save(dir); err != nil {
		return "", fmt.Errorf("save manifest: %w", err)
	}
	return pkgPath, nil
}

func (m Manifest) Sort() {
	sort.Slice(m, func(i, j int) bool { return m[i].Name < m[j].Name })
}

// packagePathFor computes the Go package path of dir/name relative to
// repoRoot, both of which must be absolute.
func packagePathFor(repoRoot, dir, name string) (string, error) {
	rel, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		return "", fmt.Errorf("plugin dir %q is not under repo root %q: %w", dir, repoRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plugin dir %q is not under repo root %q", dir, repoRoot)
	}
	if rel == "." {
		return "mycli/" + name, nil
	}
	return "mycli/" + filepath.ToSlash(rel) + "/" + name, nil
}

// fetch clones url (git) or copies a local path into dest, returning
// the module path declared by the source's go.mod (may be empty).
func fetch(ctx context.Context, url, dest string) (string, error) {
	modulePath, err := readModulePath(url)
	if err != nil {
		return "", err
	}

	if isLocalPath(url) {
		if err := CopyDir(url, dest); err != nil {
			return "", fmt.Errorf("copy %q: %w", url, err)
		}
		return modulePath, nil
	}

	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is required to download %q but was not found on PATH", url)
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, dest)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dest)
		return "", fmt.Errorf("git clone %q: %w", url, err)
	}

	// The cloned repo's go.mod is authoritative; re-read from dest.
	if mp, err := readModulePath(dest); err == nil && mp != "" {
		modulePath = mp
	}
	return modulePath, nil
}

func isLocalPath(url string) bool {
	if strings.HasPrefix(url, "git@") || strings.Contains(url, "://") || strings.HasSuffix(url, ".git") {
		return false
	}
	_, err := os.Stat(url)
	return err == nil
}

func readModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", nil
}

// RewriteImports replaces the module path prefix in every .go file
// under root, converting imports of the external module into imports
// of its new location inside the afe module.
func RewriteImports(root, oldPrefix, newPrefix string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		s := string(data)
		oldQuoted := `"` + oldPrefix + `"`
		newQuoted := `"` + newPrefix + `"`
		oldSub := `"` + oldPrefix + `/`
		newSub := `"` + newPrefix + `/`
		if !strings.Contains(s, oldQuoted) && !strings.Contains(s, oldSub) {
			return nil
		}
		s = strings.ReplaceAll(s, oldQuoted, newQuoted)
		s = strings.ReplaceAll(s, oldSub, newSub)
		return os.WriteFile(path, []byte(s), 0o644)
	})
}

// CopyDir recursively copies src into dest (dest is created).
func CopyDir(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

const remoteAggregator = "plugins/remote.go"

// AddAggregatorImport ensures repoRoot/plugins/remote.go contains a
// blank import of pkgPath, creating the file if needed.
func AddAggregatorImport(repoRoot, pkgPath string) error {
	path := filepath.Join(repoRoot, remoteAggregator)
	importLine := fmt.Sprintf("\t_ %q\n", pkgPath)

	data, err := os.ReadFile(path)
	if err == nil {
		if strings.Contains(string(data), importLine) {
			return nil
		}
		// Insert before the final closing paren of the import block.
		idx := strings.LastIndex(string(data), ")")
		if idx < 0 {
			return fmt.Errorf("could not find import block in %s", path)
		}
		s := string(data)
		s = s[:idx] + importLine + s[idx:]
		return os.WriteFile(path, []byte(s), 0o644)
	}
	if !os.IsNotExist(err) {
		return err
	}

	content := `// Code generated by afe plugin installation. DO NOT EDIT BY HAND.
//
// Remote plugins installed via the "plugin.urls" config section are
// blank-imported here so their init() functions register with the
// central plugin registry before main() runs.
package plugins

import (
` + importLine + ")\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// Rebuild runs "go build -o afe ." in repoRoot.
func Rebuild(ctx context.Context, repoRoot string) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-o", "afe", ".")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// FindRepoRoot walks up from start looking for a directory containing
// go.mod, returning it as an absolute path. It fails if none is found.
func FindRepoRoot(start string) (string, error) {
	absStart, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	dir := absStart
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s; run afe from inside the afe source tree to install plugins", start)
		}
		dir = parent
	}
}

// ReadChoice reads a single-line choice from stdin, defaulting to
// def on EOF or empty input. It returns the trimmed lowercased line.
func ReadChoice(def string) string {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line
}
