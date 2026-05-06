ARM_GCC ?= arm-linux-gnueabihf-gcc
QEMU    ?= qemu-arm

EXAMPLES := $(basename $(notdir $(wildcard examples/*.lang)))
ASMS     := $(addprefix build/,$(addsuffix .s,$(EXAMPLES)))
BINS     := $(addprefix build/,$(EXAMPLES))

.PHONY: all build test vet clean examples run-%

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

clean:
	rm -rf bin build
