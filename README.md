# goprof — tiny profiler for Go apps (v0.1)

CPU + heap sampling built on `runtime/pprof`, plus a small CLI to pull
profiles from a live `net/http/pprof` endpoint. Stdlib only.

## Layout

```
goprof/
  profiler/        library: Start/Stop, CaptureCPU, SnapshotHeap, pprof server
    config.go     Config + validation
    profiler.go   Profiler (CPU file + heap dump)
    http.go       StartPprofServer helper
  collector/       file listing + remote fetch (/debug/pprof)
    remote.go     FetchRemote(addr, type, seconds, output)
    file.go       List(dir)
  cmd/goprof/     CLI: collect + list
  example/        demo app that burns CPU and churns heap
  profiles/       output dir (gitignored except .gitkeep)
```

## Quickstart

```bash
cd goprof
go vet ./...
go test ./...

# 1. Run instrumented example (~12s, writes profiles/cpu.pb.gz + mem.pb.gz)
go run ./example

# 2. Inspect
go tool pprof profiles/cpu.pb.gz
go tool pprof profiles/mem.pb.gz

# 3. While example runs (it serves :6060), pull remotely:
go run ./cmd/goprof collect --addr localhost:6060 --type heap --output profiles/remote-heap.pb.gz
go run ./cmd/goprof collect --addr localhost:6060 --type cpu --seconds 10 --output profiles/remote-cpu.pb.gz
go run ./cmd/goprof list --dir profiles
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

## Next steps (v0.2 ideas)

- Goroutine / block / mutex capture in `Profiler`
- `trace` support (`runtime/trace`)
- TUI live view (goroutines, heap) or flamegraph export
- Auth wrapper for the HTTP endpoint
- CI + `goreleaser` + `go tool pprof -http` integration

## Module path

Currently `module goprof` for local use. To publish:

```bash
go mod edit -module github.com/<you>/goprof
# then fix imports: goprof/profiler -> github.com/<you>/goprof/profiler
```
