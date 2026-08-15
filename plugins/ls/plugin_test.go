package ls

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLsPluginExecute(t *testing.T) {
	dir := t.TempDir()
	p := &lsPlugin{enabled: true, rootDirs: rootList{dir}}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := p.Execute(context.Background(), `{"path":"`+dir+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "README.md") || !strings.Contains(out, "src/") || !strings.Contains(out, "main.go") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, ".git") {
		t.Errorf("hidden dirs should be skipped:\n%s", out)
	}
}
