.PHONY: vet test starter examples cli build flame clean ps run run-pprof

vet:
	go vet ./...

test:
	go test ./... -count=1 -v

build:
	go build -o /tmp/goprof ./cmd/goprof

starter:
	go run ./examples/starter

# Convert the latest example output to flamegraphs.
flame:
	go run ./cmd/goprof flame --input profiles/cpu.pb.gz --output profiles/flame-cpu.svg --folded profiles/flame.folded
	go run ./cmd/goprof flame --input profiles/mem.pb.gz --output profiles/flame-mem.svg

# List processes (attach candidates).
ps:
	go run ./cmd/goprof ps --go-only

# Launch + profile in one command: external mode (needs perf).
run:
	go run ./cmd/goprof run --seconds 12 -- ./examples/starter

# Launch + profile in one command: endpoint mode via the starter's :6060.
run-pprof:
	go run ./cmd/goprof run --pprof-addr localhost:6060 --no-cpu -- ./examples/starter

# Build all example apps (smoke check).
examples:
	go build ./examples/...

clean:
	rm -f profiles/*.pb.gz profiles/*.prof profiles/*.out profiles/*.svg profiles/*.folded profiles/*.csv profiles/*.data
	rm -rf profiles/attach-* profiles/run-* profiles/verify-*
