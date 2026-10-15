package flamegraph

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/pprof/profile"
)

// Stack is one aggregated call stack: Funcs[0] is the root.
type Stack struct {
	Funcs []string
	Value int64
}

// ProfileData is a parsed, aggregated pprof profile ready for rendering.
type ProfileData struct {
	Stacks      []Stack // aggregated, sorted by stack for determinism
	Total       int64   // sum of all stack values
	Unit        string  // e.g. "nanoseconds", "bytes", "count"
	SampleType  string  // e.g. "cpu", "alloc_space"
	SampleIndex int
}

// ParseStacks opens a pprof profile (protobuf .pb.gz or legacy text) and
// aggregates samples into folded stacks using the given sample value index.
// Pass sampleIndex < 0 to auto-select (see DefaultSampleIndex).
func ParseStacks(inputPath string, sampleIndex int) (*ProfileData, error) {
	if inputPath == "" {
		return nil, fmt.Errorf("flamegraph: input path required")
	}
	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("flamegraph: open %s: %w", inputPath, err)
	}
	defer f.Close()

	p, err := profile.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("flamegraph: parse %s: %w (need a pprof profile, e.g. profiles/cpu.pb.gz)", inputPath, err)
	}

	if len(p.Sample) == 0 {
		return nil, fmt.Errorf("flamegraph: profile %s has no samples", inputPath)
	}
	idx := sampleIndex
	if idx < 0 {
		idx = DefaultSampleIndex(p)
	}
	if len(p.SampleType) == 0 {
		// Profiles without declared sample types (hand-built): use value 0.
		idx = 0
	} else if idx < 0 || idx >= len(p.SampleType) {
		return nil, fmt.Errorf("flamegraph: sample index %d out of range (profile has %d sample types)", idx, len(p.SampleType))
	}
	for _, s := range p.Sample {
		if idx >= len(s.Value) {
			return nil, fmt.Errorf("flamegraph: sample index %d out of range (sample has %d values)", idx, len(s.Value))
		}
	}

	unit, typ := "", ""
	if len(p.SampleType) > idx {
		unit = p.SampleType[idx].Unit
		typ = p.SampleType[idx].Type
	}

	agg := make(map[string]int64, len(p.Sample)*2)
	order := make(map[string][]string, len(p.Sample)*2)
	for _, s := range p.Sample {
		v := s.Value[idx]
		if v <= 0 {
			continue
		}
		funcs := stackFuncs(s)
		if len(funcs) == 0 {
			funcs = []string{"unknown"}
		}
		key := strings.Join(funcs, "\x00")
		agg[key] += v
		if _, ok := order[key]; !ok {
			order[key] = funcs
		}
	}
	if len(agg) == 0 {
		return nil, fmt.Errorf("flamegraph: no positive samples at index %d in %s", idx, inputPath)
	}

	stacks := make([]Stack, 0, len(agg))
	var total int64
	for k, v := range agg {
		stacks = append(stacks, Stack{Funcs: order[k], Value: v})
		total += v
	}
	sortStacks(stacks)

	return &ProfileData{
		Stacks:      stacks,
		Total:       total,
		Unit:        unit,
		SampleType:  typ,
		SampleIndex: idx,
	}, nil
}

// stackFuncs returns root-first function names for one sample.
// pprof stores locations leaf-first, so we walk them in reverse.
// Within one location, inlined lines are innermost-first, so those
// are reversed too (outermost caller first).
func stackFuncs(s *profile.Sample) []string {
	var funcs []string
	for i := len(s.Location) - 1; i >= 0; i-- {
		loc := s.Location[i]
		if loc == nil || len(loc.Line) == 0 {
			if loc != nil && loc.Address != 0 {
				funcs = append(funcs, fmt.Sprintf("0x%x", loc.Address))
			} else {
				funcs = append(funcs, "unknown")
			}
			continue
		}
		for j := len(loc.Line) - 1; j >= 0; j-- {
			line := loc.Line[j]
			if line.Function != nil && line.Function.Name != "" {
				funcs = append(funcs, line.Function.Name)
			} else if loc.Address != 0 {
				funcs = append(funcs, fmt.Sprintf("0x%x", loc.Address))
			} else {
				funcs = append(funcs, "unknown")
			}
		}
	}
	return funcs
}

// DefaultSampleIndex picks the most flamegraph-useful value:
// CPU time for cpu profiles, allocated bytes for heap profiles,
// delay for block/mutex profiles. Falls back to index 1 when a
// profile has multiple values, else 0.
func DefaultSampleIndex(p *profile.Profile) int {
	for i, st := range p.SampleType {
		t := strings.ToLower(st.Type)
		u := strings.ToLower(st.Unit)
		if strings.Contains(t, "cpu") ||
			u == "nanoseconds" || u == "microseconds" ||
			u == "milliseconds" || u == "seconds" {
			return i
		}
	}
	for i, st := range p.SampleType {
		if strings.ToLower(st.Type) == "alloc_space" {
			return i
		}
	}
	for i, st := range p.SampleType {
		if strings.ToLower(st.Type) == "inuse_space" {
			return i
		}
	}
	if len(p.SampleType) > 1 {
		return 1
	}
	return 0
}

// SampleTypes describes the value slots in a profile for --list-samples.
func SampleTypes(inputPath string) ([]string, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("flamegraph: open %s: %w", inputPath, err)
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("flamegraph: parse %s: %w", inputPath, err)
	}
	out := make([]string, len(p.SampleType))
	for i, st := range p.SampleType {
		out[i] = fmt.Sprintf("%d: %s (%s)", i, st.Type, st.Unit)
	}
	return out, nil
}
