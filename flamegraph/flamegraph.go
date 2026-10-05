package flamegraph

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options controls Generate. Use DefaultOptions for sane v1 defaults.
type Options struct {
	// Title shown at the top of the SVG. Defaults to "Flamegraph — <sample type>".
	Title string
	// Width is the SVG canvas width in px. Default 1200.
	Width int
	// RowHeight is the px height of one stack frame. Default 22.
	RowHeight int
	// MinWidth hides frames narrower than this. Default 0.5.
	MinWidth float64
	// MaxDepth truncates stacks deeper than this (0 = unlimited).
	MaxDepth int
	// SampleIndex selects the profile value to render (-1 = auto).
	SampleIndex int
	// FoldedOutput optionally also writes folded stacks
	// (flamegraph.pl / Speedscope compatible) to this path.
	FoldedOutput string
}

// DefaultOptions returns recommended defaults (auto sample selection,
// 1200px wide SVG, no folded sidecar).
func DefaultOptions() Options {
	return Options{
		Width:       1200,
		RowHeight:   22,
		MinWidth:    0.5,
		SampleIndex: -1,
	}
}

// Summary describes what Generate produced.
type Summary struct {
	Input      string
	SVG        string
	Folded     string
	Total      int64
	Unit       string
	SampleType string
	Stacks     int
	Frames     int
}

// Generate converts a pprof profile into a self-contained flamegraph SVG.
//
//	input:  path to a pprof profile (cpu.pb.gz, mem.pb.gz, goroutine, …)
//	output: destination .svg path (directories are created)
func Generate(inputPath, outputPath string, opts Options) (Summary, error) {
	var s Summary
	if inputPath == "" {
		return s, fmt.Errorf("flamegraph: input path required")
	}
	data, err := ParseStacks(inputPath, opts.SampleIndex)
	if err != nil {
		return s, err
	}
	return renderData(inputPath, data, outputPath, opts)
}

// GenerateFromFolded converts folded stacks (`a;b;c value` lines, as
// produced by WriteFolded, stackcollapse scripts, or the attach sampler)
// into a self-contained flamegraph SVG.
func GenerateFromFolded(foldedPath, outputPath string, opts Options) (Summary, error) {
	var s Summary
	if foldedPath == "" {
		return s, fmt.Errorf("flamegraph: folded path required")
	}
	data, err := ParseFolded(foldedPath)
	if err != nil {
		return s, err
	}
	return renderData(foldedPath, data, outputPath, opts)
}

func renderData(inputPath string, data *ProfileData, outputPath string, opts Options) (Summary, error) {
	var s Summary
	if outputPath == "" {
		return s, fmt.Errorf("flamegraph: output path required")
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return s, fmt.Errorf("flamegraph: mkdir: %w", err)
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return s, fmt.Errorf("flamegraph: create %s: %w", outputPath, err)
	}
	frames, err := RenderSVG(data, out, SVGOptions{
		Title:     opts.Title,
		Width:     opts.Width,
		RowHeight: opts.RowHeight,
		MinWidth:  opts.MinWidth,
		MaxDepth:  opts.MaxDepth,
	})
	closeErr := out.Close()
	if err != nil {
		return s, err
	}
	if closeErr != nil {
		return s, fmt.Errorf("flamegraph: write %s: %w", outputPath, closeErr)
	}

	s = Summary{
		Input:      inputPath,
		SVG:        outputPath,
		Total:      data.Total,
		Unit:       data.Unit,
		SampleType: data.SampleType,
		Stacks:     len(data.Stacks),
		Frames:     frames,
	}

	if opts.FoldedOutput != "" {
		if err := os.MkdirAll(filepath.Dir(opts.FoldedOutput), 0o755); err != nil {
			return s, fmt.Errorf("flamegraph: mkdir folded: %w", err)
		}
		ff, err := os.Create(opts.FoldedOutput)
		if err != nil {
			return s, fmt.Errorf("flamegraph: create %s: %w", opts.FoldedOutput, err)
		}
		err = WriteFolded(data.Stacks, ff)
		closeErr := ff.Close()
		if err != nil {
			return s, err
		}
		if closeErr != nil {
			return s, fmt.Errorf("flamegraph: write %s: %w", opts.FoldedOutput, closeErr)
		}
		s.Folded = opts.FoldedOutput
	}
	return s, nil
}
