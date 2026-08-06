AARCH64_GCC ?= aarch64-linux-gnu-gcc
QEMU        ?= qemu-aarch64

ACTIONLINT_VERSION ?= v1.7.10

EXAMPLES := $(basename $(notdir $(wildcard examples/*.fern)))
ASMS     := $(addprefix build/,$(addsuffix .s,$(EXAMPLES)))
BINS     := $(addprefix build/,$(EXAMPLES))
LANG_SRCS := $(wildcard examples/*.fern)

.PHONY: all build test vet deadcode actionlint freeze selfhost-cli clean examples run-% fmt fmt-check gofmt gofmt-check

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

# Lint every .github/workflows/*.yml + composite action. Nothing checked
# these files before, so a typo'd `runs-on`, an expression referencing a
# context that doesn't exist in that trigger, or a missing `permissions:`
# scope only surfaced as a red — or silently wrong — run. Version-pinned
# via `go run` like the deadcode gate, so local and CI lint identically.
# Picks up shellcheck for `run:` blocks when it's on PATH (it is on the
# GitHub ubuntu runners), which is why the checked-in workflows quote
# their shell expansions.
actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

# Report the live state of the native-convergence freeze preconditions,
# derived from the tree rather than read off #4451. Fails only on a
# REGRESSION (ground lost). See tools/freeze_gate.sh.
freeze:
	./tools/freeze_gate.sh

# Build the SELF-HOST compiler to a native binary for THIS host, so self-host
# behaviour can be checked locally in seconds instead of only in CI.
#
# Why this is worth a target. The local loop for a self-host change was "push and
# wait": internal/e2eselfhost unsharded exceeds 90 minutes, internal/e2e will not
# fit in 45, and on Apple Silicon every x86 leg SKIPs for want of qemu-x86_64.
# Interpreting a driver works and is the documented fallback, but once the stdlib
# is loaded it is minutes per program — a #5311 repro timed out at 40.
#
# A native build is ~15 minutes ONCE and then ~1.3s per program, which is what
# made it practical to run the whole 335-fixture corpus through the self-host
# compiler and find twelve divergences (see internal/e2e/testdata/
# selfhost-wasm-known-divergences.txt). On Apple Silicon it needs the
# dyld-loaded PIE Mach-O container (#6000); before that every binary this
# produced was SIGKILLed at exec, which is why the target did not exist.
#
#   make selfhost-cli
#   bin/fern-selfhost -target wasm /ABS/prog.fern $(PWD)/internal/stdlib -o p.wat
#   wasmtime run p.wat; echo $$?        # oracle: ./bin/fern -interp /ABS/prog.fern
#
# Use ABSOLUTE paths. A relative one was unopenable from an arm64-darwin binary
# until #6002 (AT_FDCWD is -2 on XNU, not -100), and absolute is what every
# harness uses anyway. Note the exit code cannot carry a value >= 126: WASI
# refuses anything outside [0..126), so wasmtime reports 1.
SELFHOST_TARGET ?= $(shell uname -s | grep -qi darwin && echo arm64-darwin || echo x86-64)
selfhost-cli: bin/fern
	@mkdir -p bin
	./bin/fern -target $(SELFHOST_TARGET) -o bin/fern-selfhost examples/self_host/fern.fern
	@chmod +x bin/fern-selfhost
	@echo "built bin/fern-selfhost ($(SELFHOST_TARGET)) — see the Makefile comment for the loop"

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

# The Go-side counterpart to fmt / fmt-check. `fmt-check` covers only
# examples/*.fern, so nothing gated Go formatting and it drifted —
# gofmt's trailing-comment and map-literal alignment goes stale as soon
# as a longer entry lands beside an existing one.
gofmt:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then gofmt -w $$out; echo "reformatted:"; echo "$$out"; fi

gofmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "not gofmt-clean:"; echo "$$out"; \
		gofmt -d $$out; \
		exit 1; \
	fi

clean:
	rm -rf bin build
