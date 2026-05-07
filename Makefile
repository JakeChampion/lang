ARM_GCC ?= arm-linux-gnueabihf-gcc
QEMU    ?= qemu-arm

EXAMPLES := $(basename $(notdir $(wildcard examples/*.lang)))
ASMS     := $(addprefix build/,$(addsuffix .s,$(EXAMPLES)))
BINS     := $(addprefix build/,$(EXAMPLES))
LANG_SRCS := $(wildcard examples/*.lang)

.PHONY: all build test vet clean examples run-% fmt fmt-check

all: build test

build: bin/lang

bin/lang: $(shell find . -name '*.go' -not -path './build/*')
	@mkdir -p bin
	go build -o $@ ./cmd/lang

test:
	go test ./...

vet:
	go vet ./...

# Compile every example to assembly and (if a cross-compiler is present)
# link to a static ARM binary.
examples: $(BINS)

build/%.s: examples/%.lang bin/lang
	@mkdir -p build
	./bin/lang $< > $@

build/%: build/%.s
	$(ARM_GCC) -static $< -o $@

# `make run-factorial`, `make run-fizzbuzz`, etc.
run-%: build/%
	$(QEMU) $<

# Re-format every .lang source under examples/ in place. Useful as
# a one-shot before-commit cleanup; the formatter is idempotent so
# running it on already-formatted files is a no-op.
fmt: bin/lang
	@for f in $(LANG_SRCS); do \
		./bin/lang -fmt -w "$$f"; \
	done

# Verify every .lang source is already formatted. Prints the
# unified-diff hunks for any file that would change and exits
# non-zero so CI fails on unformatted submissions.
fmt-check: bin/lang
	@status=0; \
	for f in $(LANG_SRCS); do \
		if ! ./bin/lang -fmt -d "$$f"; then status=1; fi; \
	done; \
	exit $$status

clean:
	rm -rf bin build
