package read

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPluginExecute(t *testing.T) {
	dir := t.TempDir()
	p := &readPlugin{enabled: true, rootDirs: rootList{dir}}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "sub", "hello.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := p.Execute(context.Background(), `{"path":"`+path+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1| line one", "2| line two", "3| line three"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// offset + limit paging
	out, err = p.Execute(context.Background(), `{"path":"`+path+`","offset":1,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2| line two") || strings.Contains(out, "1| line one") {
		t.Errorf("paging failed:\n%s", out)
	}

	// outside root must be rejected
	other := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(other, []byte("nope"), 0o644)
	if _, err := p.Execute(context.Background(), `{"path":"`+other+`"}`); err == nil {
		t.Error("expected error for path outside read roots")
	}
}
