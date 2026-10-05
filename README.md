# goprof — profiler for Go apps (v0.7)

CPU + heap sampling for instrumented apps (`runtime/pprof`), flamegraph
rendering, zero-instrumentation attach profiling of running processes,
and one-shot `run` (launch the app under the profiler in a single
command). New dep: `github.com/google/pprof` (profile parsing).

## Layout

```
goprof/
  profiler/        library: Start/Stop, CaptureCPU, SnapshotHeap, pprof server
  collector/       file listing + remote fetch (/debug/pprof)
  flamegraph/      pprof/folded -> folded stacks -> self-contained SVG
  attach/          external profiling without instrumentation (Linux)
    process.go    /proc discovery + Go binary detection (debug/buildinfo)
    perf.go       perf record/session + perf script -> folded stacks
    mem.go        RSS polling -> streaming CSV + stats
    attach.go     Attach(opts): CPU flamegraph + memory series together
  runner/          launch + profile in one command (Linux)
    run.go        spawn, window/timeout/detach, signals, exit codes
    gorun.go      .go files / package dirs via `go run`, build flags
    pprof.go      optional endpoint fetch + render
  cmd/goprof/     CLI: run + attach + ps + collect + list + flame
  example/        original single demo (used by tests)
  examples/       five profile-shape demos, see examples/README.md
    cpu-bound, alloc-heavy, http-server, goroutine-leak, mutex-contention
  profiles/       output dir (gitignored except .gitkeep)
```

## Quickstart

```bash
cd goprof
go vet ./...
go test ./...

# 1. Run instrumented example (~12s, writes profiles/cpu.pb.gz + mem.pb.gz
#    plus flame-cpu.svg + flame-mem.svg automatically)
go run ./example

# 2. Inspect
go tool pprof profiles/cpu.pb.gz
go tool pprof profiles/mem.pb.gz
# or open profiles/flame-cpu.svg in a browser (search + click-to-zoom)

# 3. While example runs (it serves :6060), pull remotely:
go run ./cmd/goprof collect --addr localhost:6060 --type heap --output profiles/remote-heap.pb.gz
go run ./cmd/goprof collect --addr localhost:6060 --type cpu --seconds 10 --output profiles/remote-cpu.pb.gz
go run ./cmd/goprof list --dir profiles

# 4. Convert any saved profile to a flamegraph:
go run ./cmd/goprof flame --input profiles/cpu.pb.gz --output profiles/flame.svg
go run ./cmd/goprof flame --input profiles/mem.pb.gz --output profiles/flame-mem.svg --folded profiles/mem.folded

# 5. Or launch + profile in one command (external mode needs perf):
go run ./cmd/goprof run --seconds 12 -- go run ./example
# ...or fetch the example's :6060 endpoint while it runs (no perf needed):
go run ./cmd/goprof run --pprof-addr localhost:6060 --no-cpu -- go run ./example
```

## Library use

```go
import "goprof/profiler"

p := profiler.New(profiler.DefaultConfig())
if err := p.Start(); err != nil { log.Fatal(err) }
defer p.Stop()

// ... your app ...

// Optional mid-run heap snapshot (e.g. on signal or interval):
_ = p.SnapshotHeap("profiles/mem-snapshot.pb.gz")
```

Enable the HTTP endpoint for live debugging (localhost only):

```go
cfg := profiler.DefaultConfig()
cfg.ServerAddr = "localhost:6060"
p := profiler.New(cfg)
```

Then:

```bash
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
go tool pprof http://localhost:6060/debug/pprof/heap
```

> Never expose pprof publicly without auth — it can leak memory contents.

## CLI

```bash
go build -o /tmp/goprof ./cmd/goprof
/tmp/goprof collect --help
/tmp/goprof list --dir profiles
/tmp/goprof ps --go-only
/tmp/goprof attach --pid 1234 --seconds 30
```

Types: `cpu | heap | goroutine | block | mutex | trace`.

```bash
go build -o /tmp/goprof ./cmd/goprof
/tmp/goprof flame --input profiles/cpu.pb.gz --output profiles/flame.svg
/tmp/goprof flame --input profiles/cpu.pb.gz --list-samples
```

## Flamegraphs

`goprof flame` parses any saved pprof profile (CPU, heap, goroutine,
block, mutex) and renders a self-contained SVG: hover any frame for the
exact value + share, use the search box to dim non-matches, click a
frame to zoom (double-click background or `reset zoom` to zoom out).

Sample value selection is automatic: CPU time for `cpu` profiles,
`alloc_space` for heap profiles, delay for block/mutex. Override with
`--sample-index N` (discover slots via `--list-samples`).

```bash
# folded sidecar for flamegraph.pl / Speedscope / ad-hoc analysis
goprof flame --input profiles/cpu.pb.gz --output profiles/flame.svg \
  --folded profiles/flame.folded --title "myservice cpu"

# tune layout
goprof flame --input profiles/cpu.pb.gz --output profiles/flame.svg \
  --width 1600 --row-height 20 --min-width 1 --max-depth 30
```

Library use:

```go
import "goprof/flamegraph"

s, err := flamegraph.Generate("profiles/cpu.pb.gz", "profiles/flame.svg",
    flamegraph.DefaultOptions())
// s.Stacks, s.Frames, s.Total, s.Unit, s.SampleType
```

Folded stacks render directly too (`--format` defaults to auto by
extension):

```bash
goprof flame --input stacks.folded --output flame.svg
```

## Attach: profile a running process (Linux, no instrumentation)

`attach` samples an already-running process: CPU with `perf record`
(DWARF unwinding, reliable for Go) collapsed to folded stacks and
rendered as a flamegraph, plus resident memory polled from `/proc`
into `rss.csv`. No code changes, no restart, no pprof endpoint.

Requirements: Linux, the `perf` binary (`apt install linux-tools-generic`),
and permission to trace the target (same UID or root;
`kernel.perf_event_paranoid` ≤ 2 for user-space sampling).

> WSL2 notes: `WARNING: perf not found for kernel …-microsoft` means the
> distro wrapper has no microsoft-flavoured tools package — goprof routes
> around this automatically (override with `GOPROF_PERF_BIN=/path/to/perf`).
> To fix `perf` itself: `sudo ln -sf /usr/lib/linux-tools-<ver>/perf
> /usr/local/bin/perf`. If the VM has no PMU passthrough (hardware `cycles`
> unsupported), sample with `--event cpu-clock` instead.

```bash
# find candidates (Go binaries flagged via build info)
goprof ps --go-only

# attach for 30s: CPU flamegraph + memory series under profiles/attach-<exe>-<pid>-<ts>/
goprof attach --pid 1234 --seconds 30
goprof attach --name myservice --seconds 30

# memory only (works without perf), or custom sampling
goprof attach --pid 1234 --seconds 60 --no-cpu --mem-interval 200ms
goprof attach --pid 1234 --seconds 30 --freq 199 --callgraph fp --include-kernel
```

Artifacts per run: `perf.data` (raw, for `perf report -i`),
`cpu.folded`, `flame.svg`, `rss.csv`. Memory-only runs skip perf
entirely; non-Go targets get a warning but still profile.

Library use:

```go
import "goprof/attach"

s, err := attach.Attach(attach.Options{PID: 1234, Seconds: 30})
// s.SVG, s.Folded, s.PerfData, s.Mem (Min/Max/Avg/Growth), s.Warnings
```

## Run: launch the app under the profiler (Linux, one command)

`run` spawns your application and profiles it simultaneously — stdio
passes straight through, so it feels like running the app directly:

```bash
# external mode: perf CPU + RSS, until the app exits (needs perf)
goprof run -- ./myapp --serve
goprof run --seconds 30 -- ./myapp --serve

# endpoint mode: also fetch the app's pprof endpoint (no perf needed)
goprof run --pprof-addr localhost:6060 -- ./myapp --serve
goprof run --pprof-addr localhost:6060 --pprof-seconds 10 --no-cpu -- go run ./example

# Go source targets run via `go run` (no need to build first)
goprof run --seconds 30 -- ./main.go --serve
goprof run --seconds 30 -- ./cmd/server --serve
goprof run --go-flag -race --seconds 30 -- ./main.go   # build flags supported

# environment, working dir, kernel frames, leave servers running
goprof run -e PORT=8080 -e GOMAXPROCS=4 --dir ./service -- ./myapp
goprof run --seconds 30 --detach -- ./myapp --serve
```

Artifacts land in `profiles/run-<cmd>-<timestamp>/` (`flame.svg`,
`cpu.folded`, `perf.data`, `rss.csv`, plus `pprof-*.pb.gz` and
`flame-pprof-*.svg` in endpoint mode). Semantics:

- No `--seconds`: profiles until the app exits; its exit code is
  propagated (nonzero is not a profiler error).
- `--seconds N`: sampling window; the app is then terminated (or left
  running with `--detach`).
- Ctrl-C: terminates the app, keeps partial artifacts (CSVs stream,
  perf finalizes gracefully), exits 130.

Library use:

```go
import "goprof/runner"

s, err := runner.Run(runner.Options{
    Command: []string{"./myapp", "--serve"},
    Seconds: 30, PprofAddr: "localhost:6060",
})
// s.ExitCode, s.SVG, s.Mem, s.PprofHeap, s.Warnings, ...
```

## Next steps (v0.6 ideas)

- Goroutine / block / mutex capture in `Profiler`
- `trace` support (`runtime/trace`)
- TUI live view (goroutines, heap)
- Auth wrapper for the HTTP endpoint
- CI + `goreleaser` + `go tool pprof -http` integration

## Module path

Currently `module goprof` for local use. To publish:

```bash
go mod edit -module github.com/<you>/goprof
# then fix imports: goprof/profiler -> github.com/<you>/goprof/profiler
```
