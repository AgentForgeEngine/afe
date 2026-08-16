package pluginmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNameForURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/me/my_tool.git": "my-tool",
		"https://github.com/me/my_tool":     "my-tool",
		"/local/path/to/some_plugin":        "some-plugin",
		"https://example.com/repo.git/":     "repo",
		"git@github.com:me/cool-plugin.git": "cool-plugin",
	}
	for in, want := range cases {
		if got := NameForURL(in); got != want {
			t.Errorf("NameForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty manifest, got %v", m)
	}

	m = Manifest{{URL: "https://x/a.git", Name: "a", PackagePath: "mycli/plugins-remote/a"}}
	if !m.IsInstalled("https://x/a.git") {
		t.Fatal("IsInstalled should be true")
	}
	if m.IsInstalled("https://x/b.git") {
		t.Fatal("IsInstalled(b) should be false")
	}
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].URL != "https://x/a.git" {
		t.Fatalf("manifest round trip failed: %v", loaded)
	}
}

func TestAddAggregatorImport(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := AddAggregatorImport(repo, "mycli/plugins-remote/foo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "plugins", "remote.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `package plugins`) {
		t.Error("missing package clause:\n" + s)
	}
	if !strings.Contains(s, `_ "mycli/plugins-remote/foo"`) {
		t.Error("missing blank import:\n" + s)
	}

	// Second import appends to the same block.
	if err := AddAggregatorImport(repo, "mycli/plugins-remote/bar"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(repo, "plugins", "remote.go"))
	s = string(data)
	if !strings.Contains(s, `_ "mycli/plugins-remote/bar"`) {
		t.Error("missing second import:\n" + s)
	}
	// Idempotent.
	if err := AddAggregatorImport(repo, "mycli/plugins-remote/bar"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(repo, "plugins", "remote.go"))
	if strings.Count(string(data), `"mycli/plugins-remote/bar"`) != 1 {
		t.Errorf("import added twice:\n%s", data)
	}
}

func TestRewriteImports(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package sub

import (
	"extmod/pkg/plugin"
	"extmod/pkg/helper"
)

var _ = plugin.Name
var _ = helper.X
`
	if err := os.WriteFile(filepath.Join(sub, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RewriteImports(dir, "extmod", "mycli/plugins-remote/foo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(sub, "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"mycli/plugins-remote/foo/pkg/plugin"`) ||
		!strings.Contains(s, `"mycli/plugins-remote/foo/pkg/helper"`) {
		t.Errorf("imports not rewritten:\n%s", s)
	}
	if strings.Contains(s, "extmod") {
		t.Errorf("old module path remains:\n%s", s)
	}
}

func TestResolveSource(t *testing.T) {
	cases := []struct {
		src  string
		host string
		want string
	}{
		{"audstanley/myplugin", "github.com", "https://github.com/audstanley/myplugin.git"},
		{"audstanley/myplugin", "", "https://github.com/audstanley/myplugin.git"},
		{"me/tool", "gitlab.com/", "https://gitlab.com/me/tool.git"},
		{"https://example.com/me/tool.git", "github.com", "https://example.com/me/tool.git"},
		{"git@github.com:me/tool.git", "github.com", "git@github.com:me/tool.git"},
	}
	for _, c := range cases {
		got, err := ResolveSource(c.src, c.host)
		if err != nil {
			t.Errorf("ResolveSource(%q) error: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveSource(%q, %q) = %q, want %q", c.src, c.host, got, c.want)
		}
	}

	if _, err := ResolveSource("badshorthand", "github.com"); err == nil {
		t.Error("expected error for malformed shorthand")
	}
	if _, err := ResolveSource("a/b/c", "github.com"); err == nil {
		t.Error("expected error for owner/repo with extra segment")
	}
}

func TestCheckGo(t *testing.T) {
	if err := CheckGo(); err != nil {
		t.Skipf("go not on PATH in test env: %v", err)
	}
}

func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.go"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "in.go"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "dest")
	if err := CopyDir(src, dest); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"top.go", filepath.Join("nested", "in.go")} {
		if _, err := os.Stat(filepath.Join(dest, p)); err != nil {
			t.Errorf("missing copied file %s: %v", p, err)
		}
	}
}
