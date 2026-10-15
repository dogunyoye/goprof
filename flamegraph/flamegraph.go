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
	// HTMLOutput optionally also writes a standalone HTML viewer page
	// (sticky toolbar + inline SVG) to this path.
	HTMLOutput string
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
	HTML       string
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

// writeFileAtomic writes path via a temp file + rename so a render
// failure never leaves a truncated output behind.
func writeFileAtomic(path string, write func(f *os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("flamegraph: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.part")
	if err != nil {
		return fmt.Errorf("flamegraph: create %s: %w", path, err)
	}
	tmpName := tmp.Name()
	// Best effort cleanup on failure; success renames tmp away.
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("flamegraph: write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("flamegraph: write %s: %w", path, err)
	}
	failed = false
	return nil
}

func renderData(inputPath string, data *ProfileData, outputPath string, opts Options) (Summary, error) {
	var s Summary
	if outputPath == "" {
		return s, fmt.Errorf("flamegraph: output path required")
	}
	if opts.FoldedOutput == outputPath || opts.HTMLOutput == outputPath {
		return s, fmt.Errorf("flamegraph: sidecar output must differ from svg output")
	}

	var frames int
	if err := writeFileAtomic(outputPath, func(out *os.File) error {
		var err error
		frames, err = RenderSVG(data, out, SVGOptions{
			Title:         opts.Title,
			Width:         opts.Width,
			RowHeight:     opts.RowHeight,
			MinWidth:      opts.MinWidth,
			MaxDepth:      opts.MaxDepth,
			EmbedControls: true,
		})
		return err
	}); err != nil {
		return s, err
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
		if err := writeFileAtomic(opts.FoldedOutput, func(ff *os.File) error {
			return WriteFolded(data.Stacks, ff)
		}); err != nil {
			return s, err
		}
		s.Folded = opts.FoldedOutput
	}

	if opts.HTMLOutput != "" {
		if err := writeFileAtomic(opts.HTMLOutput, func(hf *os.File) error {
			_, err := RenderHTML(data, hf, SVGOptions{
				Title:     opts.Title,
				Width:     opts.Width,
				RowHeight: opts.RowHeight,
				MinWidth:  opts.MinWidth,
				MaxDepth:  opts.MaxDepth,
			})
			return err
		}); err != nil {
			return s, err
		}
		s.HTML = opts.HTMLOutput
	}
	return s, nil
}
