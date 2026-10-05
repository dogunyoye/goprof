package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goMemOnly(cmd []string) Options {
	o := DefaultOptions(cmd)
	o.CPU = false
	o.Mem = true
	o.MemInterval = 50 * time.Millisecond
	o.OutputDir = ""
	return o
}

func TestResolveGoFile(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.go")
	writeFile(t, main, "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"go-file-works\") }\n")

	var stdout bytes.Buffer
	o := goMemOnly([]string{main})
	o.OutputDir = t.TempDir()
	o.Stdout = &stdout
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ViaGoRun {
		t.Fatal("expected ViaGoRun")
	}
	if !s.ExitedEarly || s.ExitCode != 0 {
		t.Fatalf("unexpected exit: %+v", s)
	}
	if !strings.Contains(stdout.String(), "go-file-works") {
		t.Fatalf("missing program output: %q", stdout.String())
	}
}

func TestResolveGoFileArgs(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "args.go")
	writeFile(t, main, `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() { fmt.Println("ARGS:" + strings.Join(os.Args[1:], ",")) }
`)
	var stdout bytes.Buffer
	o := goMemOnly([]string{main, "a1", "--flag", "a3"})
	o.OutputDir = t.TempDir()
	o.Stdout = &stdout
	if _, err := Run(o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "ARGS:a1,--flag,a3") {
		t.Fatalf("args not passed through: %q", stdout.String())
	}
}

func TestResolveMultipleGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(helper()) }\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package main\n\nfunc helper() string { return \"multi-ok\" }\n")

	var stdout bytes.Buffer
	o := goMemOnly([]string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")})
	o.OutputDir = t.TempDir()
	o.Stdout = &stdout
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ViaGoRun || !strings.Contains(stdout.String(), "multi-ok") {
		t.Fatalf("multi-file go run failed: %+v %q", s, stdout.String())
	}
}

func TestResolvePackageDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tmphello\n\ngo 1.23\n")
	writeFile(t, filepath.Join(dir, "sub", "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"pkg-dir-works\") }\n")

	var stdout bytes.Buffer
	o := goMemOnly([]string{"./sub"})
	o.Dir = dir
	o.OutputDir = t.TempDir()
	o.Stdout = &stdout
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ViaGoRun {
		t.Fatal("expected ViaGoRun for package dir")
	}
	if !strings.Contains(stdout.String(), "pkg-dir-works") {
		t.Fatalf("missing output: %q", stdout.String())
	}
}

func TestResolveBareDirName(t *testing.T) {
	argv, viaGo, err := resolveCommand([]string{"example"}, "/repo", nil)
	if err != nil {
		t.Fatalf("resolve should not fail without checking fs: %v", err)
	}
	_ = argv
	_ = viaGo
	// Bare "example" with no such local dir passes through to direct exec.
	if viaGo {
		t.Fatal("nonexistent dir must not trigger go run")
	}
}

func TestResolveGoBuildFlags(t *testing.T) {
	// Build tags are honored in package mode (explicit file lists
	// ignore constraints, so this uses a package dir).
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tmpflags\n\ngo 1.23\n")
	writeFile(t, filepath.Join(dir, "app", "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(which + \"-build\") }\n")
	writeFile(t, filepath.Join(dir, "app", "plain.go"),
		"//go:build !tagx\n\npackage main\n\nconst which = \"plain\"\n")
	writeFile(t, filepath.Join(dir, "app", "tagged.go"),
		"//go:build tagx\n\npackage main\n\nconst which = \"tagged\"\n")

	run := func(flags []string) string {
		var stdout bytes.Buffer
		o := goMemOnly([]string{"./app"})
		o.Dir = dir
		o.GoFlags = flags
		o.OutputDir = t.TempDir()
		o.Stdout = &stdout
		if _, err := Run(o); err != nil {
			t.Fatalf("Run(%v): %v", flags, err)
		}
		return stdout.String()
	}
	if out := run(nil); !strings.Contains(out, "plain-build") {
		t.Fatalf("default build: %q", out)
	}
	if out := run([]string{"-tags=tagx"}); !strings.Contains(out, "tagged-build") {
		t.Fatalf("tagged build: %q", out)
	}
}

func TestResolveMissingGoFile(t *testing.T) {
	o := goMemOnly([]string{"/nonexistent/main.go"})
	o.OutputDir = t.TempDir()
	if _, err := Run(o); err == nil {
		t.Fatal("expected error for missing .go file")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveDirectPassthrough(t *testing.T) {
	argv, viaGo, err := resolveCommand([]string{"sh", "-c", "echo hi"}, "", nil)
	if err != nil || viaGo {
		t.Fatalf("direct binary must pass through: %v %v", argv, err)
	}
	if len(argv) != 3 || argv[0] != "sh" {
		t.Fatalf("argv changed: %v", argv)
	}
	// Missing binary also passes through (exec reports the real error).
	if _, viaGo, err := resolveCommand([]string{"/nonexistent-bin-xyz"}, "", nil); err != nil || viaGo {
		t.Fatalf("missing binary must pass through: %v %v", err, viaGo)
	}
}

func TestStartHint(t *testing.T) {
	if startHint("sh") != "" {
		t.Fatal("plain binary should get no hint")
	}
	for _, target := range []string{"./main.go", "cmd/server", "github.com/x/y"} {
		if startHint(target) == "" {
			t.Fatalf("expected hint for %q", target)
		}
	}
}
