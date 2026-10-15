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

// sortStacks orders stacks deterministically by their joined frames,
// precomputing keys once instead of re-joining on every comparison.
func sortStacks(stacks []Stack) {
	keys := make([]string, len(stacks))
	for i := range stacks {
		keys[i] = strings.Join(stacks[i].Funcs, ";")
	}
	order := make([]int, len(stacks))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return keys[order[a]] < keys[order[b]] })
	sorted := make([]Stack, len(stacks))
	for i, oi := range order {
		sorted[i] = stacks[oi]
	}
	copy(stacks, sorted)
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
	var lineNo int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.LastIndexByte(line, ' ')
		if sep < 0 {
			return nil, fmt.Errorf("flamegraph: bad folded line %d in %s (want 'a;b;c value'): %q", lineNo, inputPath, line)
		}
		v, err := strconv.ParseInt(strings.TrimSpace(line[sep+1:]), 10, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("flamegraph: bad folded value on line %d in %s: %q", lineNo, inputPath, line)
		}
		raw := strings.TrimSpace(line[:sep])
		if raw == "" {
			return nil, fmt.Errorf("flamegraph: bad folded line %d in %s (empty stack): %q", lineNo, inputPath, line)
		}
		funcs := strings.Split(raw, ";")
		for _, fn := range funcs {
			if fn == "" {
				return nil, fmt.Errorf("flamegraph: bad folded line %d in %s (empty frame): %q", lineNo, inputPath, line)
			}
		}
		key := strings.Join(funcs, "\x00")
		agg[key] += v
		if _, ok := funcsOf[key]; !ok {
			funcsOf[key] = funcs
		}
		total += v
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
	sortStacks(stacks)
	return &ProfileData{
		Stacks:      stacks,
		Total:       total,
		Unit:        "samples",
		SampleType:  "collapsed",
		SampleIndex: 0,
	}, nil
}
