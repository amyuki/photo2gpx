package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactAndTail(t *testing.T) {
	old := home
	home = "/home/yuki"
	defer func() { home = old }()
	if got := redact("/home/yuki/Documents/a.gpx"); got != "~/Documents/a.gpx" {
		t.Fatalf("redact: %q", got)
	}
	p := filepath.Join(t.TempDir(), "x.log")
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("last\n")
	os.WriteFile(p, []byte(b.String()), 0o644)
	l := tailLines(p, 200)
	if len(l) != 200 || l[len(l)-1] != "last" {
		t.Fatalf("tail: %d %q", len(l), l[len(l)-1])
	}
}
