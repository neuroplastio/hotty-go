# hotty-go. Toolchain from mise.toml (Go): `mise install` first.
GO          ?= mise x -- go
GOFMT       ?= mise x -- gofmt
COVER_MIN   ?= 85
HOTTY_DIR   ?= ../../hotty/main
# staticcheck 2026.2.1, the first that knows Go 1.26.
STATICCHECK ?= honnef.co/go/tools/cmd/staticcheck@v0.8.1
# The repository's modules. The SDK's packages that need more than the
# standard library are modules of their own, so that a program takes on
# only what it imports; the example programs are one too. Each target below
# that runs Go runs it in every module.
SDK         := . hottyterm hottytea hottydoc hottytest
MODULES     := $(SDK) examples

.PHONY: check fmt tidy vet lint test cover docs docs-check examples pin vectors clean

check: fmt tidy vet lint cover docs-check examples   ## the gate

fmt:   ## fails, listing them, when gofmt would change files
	@out="$$($(GOFMT) -l .)"; if [ -n "$$out" ]; then echo "gofmt would change:"; echo "$$out"; exit 1; fi

tidy:   ## fails when a module's go.mod or go.sum is not what go mod tidy makes
	@for m in $(MODULES); do echo "$$m: go mod tidy -diff"; (cd $$m && $(GO) mod tidy -diff) || exit 1; done

vet:
	@for m in $(MODULES); do echo "$$m: go vet"; (cd $$m && $(GO) vet ./...) || exit 1; done

lint:   ## staticcheck, pinned
	@for m in $(MODULES); do echo "$$m: staticcheck"; (cd $$m && $(GO) run $(STATICCHECK) ./...) || exit 1; done

test:   ## the tests, with the race detector
	@for m in $(MODULES); do (cd $$m && $(GO) test -race ./...) || exit 1; done

cover:   ## the SDK's tests with coverage (coverage.out): a table by package, and every public package at least COVER_MIN%
	@status=0; echo "mode: atomic" > coverage.out; \
	for m in $(SDK); do \
		(cd $$m && $(GO) test -race -covermode=atomic -coverprofile=$(CURDIR)/coverage.part ./...) || status=1; \
		[ -f coverage.part ] && tail -n +2 coverage.part >> coverage.out; rm -f coverage.part; \
	done; \
	GO="$(GO)" MODULES="$(SDK)" sh scripts/cover.sh coverage.out $(COVER_MIN) && exit $$status

docs:   ## docs/api and README's tables, written from the code (internal/docgen)
	$(GO) run ./internal/docgen

docs-check:   ## fails when docs/api or README's tables are not what make docs writes
	$(GO) run ./internal/docgen -check

examples:   ## the example programs' tests, with the race detector
	cd examples && $(GO) test -race ./...

pin:   ## the requirements between the modules, at REV: a release (v0.1.0) or a pushed commit (scripts/pin.sh)
	GO="$(GO)" sh scripts/pin.sh $(REV)

vectors:   ## the conformance vectors, from a checkout of neuroplastio/hotty (HOTTY_DIR)
	cp $(HOTTY_DIR)/conformance/vectors.json testdata/conformance/vectors.json

clean:
	rm -f coverage.out coverage.part coverage.html
