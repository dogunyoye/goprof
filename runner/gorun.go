package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveCommand maps the requested target to an argv to execute.
//
//   - "./main.go [args...]" (any leading run of .go files) runs via
//     the Go toolchain: `go run [goFlags] files... args...`.
//   - A local directory holding .go files (e.g. "./examples/starter") runs via
//     `go run [goFlags] <pkg> args...`.
//   - Anything else (executables, PATH binaries) runs directly.
//
// Paths are resolved relative to dir (the child's working directory)
// for existence checks; the returned argv keeps the user's original
// spellings so child-relative resolution matches.
func resolveCommand(cmd []string, dir string, goFlags []string) (argv []string, viaGo bool, err error) {
	if len(cmd) == 0 {
		return nil, false, fmt.Errorf("runner: no command to run")
	}
	target := cmd[0]

	if strings.HasSuffix(target, ".go") {
		var files []string
		i := 0
		for i < len(cmd) && strings.HasSuffix(cmd[i], ".go") {
			files = append(files, cmd[i])
			i++
		}
		for _, f := range files {
			if !fileExists(resolvePath(dir, f)) {
				return nil, false, fmt.Errorf("runner: go file not found: %s", f)
			}
		}
		if err := needGoToolchain(); err != nil {
			return nil, false, err
		}
		argv = append([]string{"go", "run"}, goFlags...)
		argv = append(argv, files...)
		argv = append(argv, cmd[i:]...)
		return argv, true, nil
	}

	if isGoDir(resolvePath(dir, target)) {
		if err := needGoToolchain(); err != nil {
			return nil, false, err
		}
		pkg := target
		if !strings.Contains(pkg, "/") && !strings.HasPrefix(pkg, ".") {
			pkg = "./" + pkg // bare dir name -> local package path
		}
		argv = append([]string{"go", "run"}, goFlags...)
		argv = append(argv, pkg)
		argv = append(argv, cmd[1:]...)
		return argv, true, nil
	}

	return cmd, false, nil
}

// resolvePath anchors a possibly-relative path at dir.
func resolvePath(dir, p string) string {
	if dir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

func isGoDir(p string) bool {
	entries, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

func needGoToolchain() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("runner: target is Go source but the go toolchain was not found in PATH")
	}
	return nil
}

// startHint suggests Go-source usage when a direct launch fails on a
// target that smells like Go (a path with slashes or dots).
func startHint(target string) string {
	if strings.HasSuffix(target, ".go") ||
		strings.Contains(target, "/") || strings.HasPrefix(target, ".") {
		return " (hint: pass a .go file, a package dir, or a binary — e.g. goprof run -- ./main.go, goprof run -- ./cmd/server, goprof run -- ./myapp)"
	}
	return ""
}
