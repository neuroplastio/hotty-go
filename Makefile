# hotty-go. Toolchain from mise.toml (Go): `mise install` first.
GO          ?= mise x -- go
GOFMT       ?= mise x -- gofmt
COVER_MIN   ?= 85
HOTTY_DIR   ?= ../../hotty/main
# staticcheck 2026.2.1, the first that knows Go 1.26.
STATICCHECK ?= honnef.co/go/tools/cmd/staticcheck@v0.8.1

.PHONY: check fmt vet lint test cover docs docs-check examples vectors clean

check: fmt vet lint cover docs-check examples   ## the gate

fmt:   ## fails, listing them, when gofmt would change files
	@out="$$($(GOFMT) -l .)"; if [ -n "$$out" ]; then echo "gofmt would change:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

lint:   ## staticcheck, pinned
	$(GO) run $(STATICCHECK) ./...

test:   ## the tests, with the race detector
	$(GO) test -race ./...

cover:   ## the tests with coverage (coverage.out): a table by package, and every public package at least COVER_MIN%
	status=0; $(GO) test -race -covermode=atomic -coverprofile=coverage.out ./... || status=$$?; \
	GO="$(GO)" sh scripts/cover.sh coverage.out $(COVER_MIN) && exit $$status

docs:   ## docs/api and README's tables, written from the code (internal/docgen)
	$(GO) run ./internal/docgen

docs-check:   ## fails when docs/api or README's tables are not what make docs writes
	$(GO) run ./internal/docgen -check

examples:   ## the example programs build
	@if [ -d examples ]; then $(GO) build ./examples/...; fi

vectors:   ## the conformance vectors, from a checkout of neuroplastio/hotty (HOTTY_DIR)
	cp $(HOTTY_DIR)/conformance/vectors.json testdata/conformance/vectors.json

clean:
	rm -f coverage.out coverage.html
