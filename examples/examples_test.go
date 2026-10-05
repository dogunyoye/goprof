// Smoke tests for the example apps: each must start, run its
// workload briefly, and exit 0. Ports use :0-style disabling via
// -pprof "" to avoid clashes when tests run together.
package examples_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runExample(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"run", "./" + dir}, args...)
	cmd := exec.Command("go", args...)
	cmd.Dir = examplesDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", dir, err, out)
	}
	return string(out)
}

func TestExamplesSmoke(t *testing.T) {
	cases := []struct {
		dir  string
		args []string
		want string
	}{
		{"cpu-bound", []string{"-duration=2s", "-pprof=", "-out=" + t.TempDir()}, "=== goprof example: cpu-bound ==="},
		{"alloc-heavy", []string{"-duration=2s", "-pprof="}, "=== goprof example: alloc-heavy ==="},
		{"http-server", []string{"-duration=2s", "-pprof=", "-addr=localhost:18082", "-clients=2"}, "=== goprof example: http-server ==="},
		{"goroutine-leak", []string{"-duration=2s", "-pprof=", "-rate=10"}, "=== goprof example: goroutine-leak ==="},
		{"mutex-contention", []string{"-duration=2s", "-pprof=", "-workers=4"}, "=== goprof example: mutex-contention ==="},
	}
	for _, c := range cases {
		out := runExample(t, c.dir, c.args...)
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: missing banner, got:\n%s", c.dir, out)
		}
		if !strings.Contains(out, "done:") {
			t.Errorf("%s: missing completion line, got:\n%s", c.dir, out)
		}
	}
}

func TestCpuBoundArtifacts(t *testing.T) {
	outDir := t.TempDir()
	runExample(t, "cpu-bound",
		"-duration=2s", "-pprof=", "-out="+outDir)
	for _, name := range []string{"cpu.pb.gz", "mem.pb.gz", "flame-cpu-bound.svg", "flame-mem-cpu-bound.svg"} {
		fi, err := os.Stat(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("expected artifact %s: %v", name, err)
		}
		if fi.Size() == 0 {
			t.Fatalf("artifact %s is empty", name)
		}
	}
}

// examplesDir is the directory this test file lives in; `go run`
// resolves ./<example> package paths against it.
func examplesDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}
