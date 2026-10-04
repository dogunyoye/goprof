# goprof — tiny profiler for Go apps (v0.2)

CPU + heap sampling built on `runtime/pprof`, plus a small CLI to pull
profiles from a live `net/http/pprof` endpoint and render flamegraphs.
Only external dep: `github.com/google/pprof` (profile parsing).

## Layout

```
goprof/
  profiler/        library: Start/Stop, CaptureCPU, SnapshotHeap, pprof server
    config.go     Config + validation
    profiler.go   Profiler (CPU file + heap dump)
    http.go       StartPprofServer helper
  collector/       file listing + remote fetch (/debug/pprof)
    remote.go     FetchRemote(addr, type, seconds, output)
    file.go       List(dir) — profiles + flamegraph outputs
  flamegraph/      pprof -> folded stacks -> self-contained SVG
    flamegraph.go Generate(input, output, opts) + Summary
    stacks.go     ParseStacks, DefaultSampleIndex, SampleTypes
    folded.go     WriteFolded (flamegraph.pl / Speedscope compatible)
    svg.go        RenderSVG (search, tooltips, click-to-zoom)
  cmd/goprof/     CLI: collect + list + flame
  example/        demo app that burns CPU and churns heap
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

## Next steps (v0.3 ideas)

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
