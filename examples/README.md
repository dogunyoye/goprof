# goprof examples

Five small apps, each with a different profile shape. Every one serves
`net/http/pprof` (ports below) and prints the commands to try on startup.
Run one at a time from the repo root, e.g. `go run ./examples/cpu-bound`.

| example | shape | pprof | try |
|---|---|---|---|
| `cpu-bound` | hot CPU (sieve+sha256), flat heap; writes profiles + SVGs via the `profiler` lib | :6061 | `collect --type cpu` → `flame` |
| `alloc-heavy` | churn + slow leak; climbing RSS | :6062 | `collect --type heap` → `flame`; `run --pprof-addr` mem growth |
| `http-server` | API with fast/slow/alloc endpoints + self load | :6063 (api :8080) | `collect --type cpu`, compare `slowHandler` vs `allocHandler` |
| `goroutine-leak` | leaked blocked workers, growing count | :6064 | `collect --type goroutine`, `-top` twice, compare |
| `mutex-contention` | contended mutex + queued readers | :6065 | `collect --type mutex`, `--type block` |

Flags (all): `-duration` (e.g. `-duration 30s`), `-pprof` (endpoint addr,
empty disables). `cpu-bound` adds `-out` (profile dir). `http-server`
adds `-addr`, `-load=false`, `-clients`. `goroutine-leak` adds `-rate`.
`mutex-contention` adds `-workers`.

## Suggested tour (each in its own terminal)

```bash
# 1. CPU flamegraph, instrumented app
go run ./examples/cpu-bound -duration 20s
# (writes profiles/cpu.pb.gz + profiles/flame-cpu-bound.svg by itself)

# 2. Heap of a leaky app, remote fetch
go run ./examples/alloc-heavy -duration 60s
go run ./cmd/goprof collect --addr localhost:6062 --type heap --output profiles/alloc-heap.pb.gz
go run ./cmd/goprof flame --input profiles/alloc-heap.pb.gz --output profiles/flame-alloc.svg

# 3. One command: launch + profile the leak (heap + RSS)
go run ./cmd/goprof run --pprof-addr localhost:6062 --pprof-seconds 10 --no-cpu -- ./examples/alloc-heavy -duration 20s

# 4. Goroutine leak, before/after
go run ./examples/goroutine-leak -duration 60s -rate 20
go run ./cmd/goprof collect --addr localhost:6064 --type goroutine --output profiles/g1.pb.gz
# ...wait 20s...
go run ./cmd/goprof collect --addr localhost:6064 --type goroutine --output profiles/g2.pb.gz
go tool pprof -top profiles/g2.pb.gz

# 5. Lock contention (needs the in-code profile rates, already set)
go run ./examples/mutex-contention -duration 60s
go run ./cmd/goprof collect --addr localhost:6065 --type mutex --output profiles/mutex.pb.gz
go run ./cmd/goprof collect --addr localhost:6065 --type block --output profiles/block.pb.gz
go tool pprof -top profiles/mutex.pb.gz

# 6. Zero-instrumentation attach of any of the above (needs perf)
goprof attach --name leak --seconds 20
```

The original single demo still lives at `example/` (used by the test suite).
