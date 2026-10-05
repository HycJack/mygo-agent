APP     := mygo-agent
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build run test race vet fmt release clean

## build: compile a binary for this machine into ./
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(APP) .

## run: run from source
run:
	go run .

## test: all packages
test:
	go test ./...

## race: all packages under the race detector
race:
	go test -race ./...

## vet: go vet
vet:
	go vet ./...

## fmt: gofmt the tree
fmt:
	gofmt -l -w .

## release: cross-compile dist/ for macOS (binaries + .app), Linux and
## Windows, then checksum everything.
release:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		ext=$$([ $$os = windows ] && echo .exe); \
		echo "==> $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/$(APP)-$$os-$$arch$$ext .; \
	done
	@for os in darwin; do \
		for arch in amd64 arm64; do \
			echo "==> .app bundle for $$os/$$arch"; \
			bundle=dist/MyGoAgent-$$os-$$arch.app/Contents/MacOS; \
			mkdir -p $$bundle; \
			cp dist/$(APP)-$$os-$$arch $$bundle/MyGoAgent; \
			sed -e "s/VERSION/$(VERSION)/" packaging/Info.plist > dist/MyGoAgent-$$os-$$arch.app/Contents/Info.plist; \
			(cd dist/MyGoAgent-$$os-$$arch.app && zip -qr ../MyGoAgent-$$os-$$arch.app.zip .); \
			rm -rf dist/MyGoAgent-$$os-$$arch.app; \
		done; \
	done
	@cd dist && shasum -a 256 * > checksums.txt
	@ls -la dist

## clean: remove build outputs
clean:
	rm -rf dist $(APP)
