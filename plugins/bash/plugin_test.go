package bash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashPluginExecute(t *testing.T) {
	dir := t.TempDir()
	p := &bashPlugin{enabled: true, allowDirs: allowDirList{dir}}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}

	out, err := p.Execute(context.Background(), `{"command":"echo hello","working_dir":"`+dir+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exit code: 0") || !strings.Contains(out, "hello") {
		t.Errorf("unexpected output: %s", out)
	}

	// non-zero exit reported, not an error
	out, err = p.Execute(context.Background(), `{"command":"exit 3","working_dir":"`+dir+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exit code: 3") {
		t.Errorf("unexpected output: %s", out)
	}

	// outside allowed dir rejected
	outside, err := filepath.Abs(os.TempDir())
	if err == nil && outside != dir {
		if _, err := p.Execute(context.Background(), `{"command":"echo x","working_dir":"`+outside+`"}`); err == nil {
			t.Error("expected error for working dir outside allowed dirs")
		}
	}
}
