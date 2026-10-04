package flamegraph

import (
	"fmt"
	"io"
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
