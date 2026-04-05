VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o gpuaudit ./cmd/gpuaudit

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o gpuaudit-linux ./cmd/gpuaudit

build-all: build build-linux
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o gpuaudit-darwin-arm64 ./cmd/gpuaudit
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o gpuaudit-darwin-amd64 ./cmd/gpuaudit
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o gpuaudit-linux-arm64 ./cmd/gpuaudit

test:
	go test ./... -v

vet:
	go vet ./...

clean:
	rm -f gpuaudit gpuaudit-linux gpuaudit-linux-arm64 gpuaudit-darwin-*

.PHONY: build build-linux build-all test vet clean
