VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test vet lint clean release-dry

build:
	go build -ldflags "$(LDFLAGS)" ./cmd/ladle/

test:
	go test ./... -v -race

vet:
	go vet ./...

lint:
	golangci-lint run

clean:
	rm -f ladle
	rm -rf dist/

release-dry:
	goreleaser release --snapshot --clean
