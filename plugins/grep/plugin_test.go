package grep

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepPluginExecute(t *testing.T) {
	dir := t.TempDir()
	p := &grepPlugin{enabled: true, rootDirs: rootList{dir}}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc needle() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("needle here too\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "hidden.go"), []byte("needle in hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := p.Execute(context.Background(), `{"pattern":"needle","path":"`+dir+`","include":"*.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go:3:") {
		t.Errorf("expected match in a.go line 3:\n%s", out)
	}
	if strings.Contains(out, "b.md") {
		t.Errorf("include filter should exclude b.md:\n%s", out)
	}
	if strings.Contains(out, ".git") {
		t.Errorf("hidden dirs should be skipped:\n%s", out)
	}

	// literal text with regex metacharacters
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("a.b is here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = p.Execute(context.Background(), `{"pattern":"a.b","path":"`+dir+`","literal_text":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c.txt") {
		t.Errorf("literal match expected in c.txt:\n%s", out)
	}
}
