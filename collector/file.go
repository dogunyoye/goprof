package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProfileInfo describes a profile file on disk.
type ProfileInfo struct {
	Name string
	Path string
	Size int64
}

// List returns profile-like files in dir sorted by name.
// Matches *.pb.gz, *.prof, *.out, *.pprof plus generated
// flamegraph outputs (*.svg, *.folded) for convenience.
func List(dir string) ([]ProfileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("collector: read dir %s: %w", dir, err)
	}
	var out []ProfileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		// Accept .pb.gz double extension plus common pprof suffixes,
		// plus flamegraph outputs.
		matched := strings.HasSuffix(name, ".pb.gz") || ext == ".prof" || ext == ".out" ||
			ext == ".pprof" || ext == ".svg" || ext == ".folded"
		if !matched {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, ProfileInfo{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
			Size: fi.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
