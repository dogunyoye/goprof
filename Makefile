.PHONY: vet test example cli build clean

vet:
	go vet ./...

test:
	go test ./... -count=1 -v

build:
	go build -o /tmp/goprof ./cmd/goprof

example:
	go run ./example

clean:
	rm -f profiles/*.pb.gz profiles/*.prof profiles/*.out
