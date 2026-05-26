AARCH64_GCC ?= aarch64-linux-gnu-gcc
QEMU        ?= qemu-aarch64

EXAMPLES := $(basename $(notdir $(wildcard examples/*.fern)))
ASMS     := $(addprefix build/,$(addsuffix .s,$(EXAMPLES)))
BINS     := $(addprefix build/,$(EXAMPLES))
LANG_SRCS := $(wildcard examples/*.fern)

.PHONY: all build test vet deadcode clean examples run-% fmt fmt-check

all: build test

build: bin/fern

bin/fern: $(shell find . -name '*.go' -not -path './build/*')
	@mkdir -p bin
	go build -o $@ ./cmd/fern

test:
	go test ./...

vet:
	go vet ./...

# Fail on unreachable functions not listed in the allowlist. See
# tools/deadcode_gate.sh and tools/deadcode-allowlist.txt.
deadcode:
	./tools/deadcode_gate.sh

# Compile every example to arm64 Linux assembly and (if the
# cross-compiler is present) link to a static arm64 binary.
examples: $(BINS)

build/%.s: examples/%.fern bin/fern
	@mkdir -p build
	./bin/fern -target arm64 $< > $@

build/%: build/%.s
	$(AARCH64_GCC) -static -nostdlib $< -o $@

# `make run-factorial`, `make run-fizzbuzz`, etc.
run-%: build/%
	$(QEMU) $<

# Re-format every .fern source under examples/ in place. Useful as
# a one-shot before-commit cleanup; the formatter is idempotent so
# running it on already-formatted files is a no-op.
fmt: bin/fern
	@for f in $(LANG_SRCS); do \
		./bin/fern -fmt -w "$$f"; \
	done

# Verify every .fern source is already formatted. Prints the
# unified-diff hunks for any file that would change and exits
# non-zero so CI fails on unformatted submissions.
fmt-check: bin/fern
	@status=0; \
	for f in $(LANG_SRCS); do \
		if ! ./bin/fern -fmt -d "$$f"; then status=1; fi; \
	done; \
	exit $$status

clean:
	rm -rf bin build
