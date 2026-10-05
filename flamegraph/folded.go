package flamegraph

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// WriteFolded writes stacks in Brendan Gregg's folded format:
//
//	main;work;burnCPU 12345
//
// one line per stack. The output works with flamegraph.pl, Speedscope
// (via import), and `go tool pprof -traces` style ad-hoc analysis.
func WriteFolded(stacks []Stack, w io.Writer) error {
	for _, s := range stacks {
		if len(s.Funcs) == 0 || s.Value <= 0 {
			continue
		}
		line := strings.Join(s.Funcs, ";") + fmt.Sprintf(" %d\n", s.Value)
		if _, err := io.WriteString(w, line); err != nil {
			return fmt.Errorf("flamegraph: write folded: %w", err)
		}
	}
	return nil
}

// ParseFolded reads folded stacks (one `a;b;c value` line each, as
// produced by WriteFolded, stackcollapse scripts, or the attach
// sampler) and aggregates duplicate stacks. Blank lines and lines
// starting with '#' are ignored.
func ParseFolded(inputPath string) (*ProfileData, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("flamegraph: open %s: %w", inputPath, err)
	}
	defer f.Close()

	agg := map[string]int64{}
	funcsOf := map[string][]string{}
	var total int64
	lines := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.LastIndexByte(line, ' ')
		if sep < 0 {
			return nil, fmt.Errorf("flamegraph: bad folded line %d in %s (want 'a;b;c value'): %q", lines+1, inputPath, line)
		}
		v, err := strconv.ParseInt(strings.TrimSpace(line[sep+1:]), 10, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("flamegraph: bad folded value on line %d in %s: %q", lines+1, inputPath, line)
		}
		funcs := strings.Split(line[:sep], ";")
		key := strings.Join(funcs, "\x00")
		agg[key] += v
		if _, ok := funcsOf[key]; !ok {
			funcsOf[key] = funcs
		}
		total += v
		lines++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("flamegraph: read %s: %w", inputPath, err)
	}
	if len(agg) == 0 {
		return nil, fmt.Errorf("flamegraph: no stacks in %s", inputPath)
	}

	stacks := make([]Stack, 0, len(agg))
	for k, v := range agg {
		stacks = append(stacks, Stack{Funcs: funcsOf[k], Value: v})
	}
	sort.Slice(stacks, func(i, j int) bool {
		return strings.Join(stacks[i].Funcs, ";") < strings.Join(stacks[j].Funcs, ";")
	})
	return &ProfileData{
		Stacks:      stacks,
		Total:       total,
		Unit:        "samples",
		SampleType:  "collapsed",
		SampleIndex: 0,
	}, nil
}
