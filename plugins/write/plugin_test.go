package write

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePluginExecute(t *testing.T) {
	dir := t.TempDir()
	p := &writePlugin{enabled: true, rootDirs: rootList{dir}}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "nested", "out.txt")
	out, err := p.Execute(context.Background(), `{"path":"`+target+`","content":"hello\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("expected created action: %s", out)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Errorf("file content = %q", data)
	}

	// overwrite
	out, err = p.Execute(context.Background(), `{"path":"`+target+`","content":"bye\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "overwritten") {
		t.Errorf("expected overwritten action: %s", out)
	}

	// unchanged short-circuit
	if _, err := p.Execute(context.Background(), `{"path":"`+target+`","content":"bye\n"}`); err != nil {
		t.Fatal(err)
	}

	// outside root must be rejected
	if _, err := p.Execute(context.Background(), `{"path":"/etc/passwd","content":"x"}`); err == nil {
		t.Error("expected error for path outside write roots")
	}
}
