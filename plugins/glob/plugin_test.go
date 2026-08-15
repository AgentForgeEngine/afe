package glob

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobPluginExecute(t *testing.T) {
	dir := t.TempDir()
	p := &globPlugin{enabled: true, rootDirs: rootList{dir}}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "util.go", "notes.md", ".hidden.go"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := p.Execute(context.Background(), `{"pattern":"**/*.go","path":"`+dir+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "util.go") {
		t.Errorf("expected go files in output:\n%s", out)
	}
	if strings.Contains(out, ".hidden.go") {
		t.Errorf("hidden file should be skipped:\n%s", out)
	}
	if strings.Contains(out, "notes.md") {
		t.Errorf("md file should not match:\n%s", out)
	}
}
