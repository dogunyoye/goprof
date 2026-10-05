.PHONY: vet test example cli build flame clean

vet:
	go vet ./...

test:
	go test ./... -count=1 -v

build:
	go build -o /tmp/goprof ./cmd/goprof

example:
	go run ./example

# Convert the latest example output to flamegraphs.
flame:
	go run ./cmd/goprof flame --input profiles/cpu.pb.gz --output profiles/flame-cpu.svg --folded profiles/flame.folded
	go run ./cmd/goprof flame --input profiles/mem.pb.gz --output profiles/flame-mem.svg

# List processes (attach candidates) and show attach help.
ps:
	go run ./cmd/goprof ps --go-only

clean:
	rm -f profiles/*.pb.gz profiles/*.prof profiles/*.out profiles/*.svg profiles/*.folded profiles/*.csv profiles/*.data
	rm -rf profiles/attach-* profiles/verify-*
