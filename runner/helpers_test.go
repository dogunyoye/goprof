package runner

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"goprof/attach"
)

// PerfAvailableForTest reports whether perf-based tests can run.
func PerfAvailableForTest() bool { return attach.PerfAvailable() == nil }

// killCheck succeeds while pid exists (EPERM counts as existing).
func killCheck(pid int) error {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

func terminateTestChild(t *testing.T, pid int) {
	t.Helper()
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 30; i++ {
		if killCheck(pid) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// portFree reports whether a TCP addr refuses connections right now.
func portFree(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return true
	}
	c.Close()
	return false
}

// repoRoot walks up from the test working directory to go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
