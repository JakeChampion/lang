FERN ?= ../../bin/fern
TARGET ?= x86-64-linux
FERNFLAGS ?= -stdlib ../../internal/stdlib
SOURCES := $(wildcard *.fern)

.PHONY: all check test clean
all: build/ulc

build:
	mkdir -p $@

build/ulc: $(SOURCES) | build
	$(FERN) $(FERNFLAGS) -target $(TARGET) -o $@ ulc.fern

check:
	$(FERN) $(FERNFLAGS) -check ulc.fern
	$(FERN) $(FERNFLAGS) -check tests/unit.fern

build/unit: tests/unit.fern $(SOURCES) | build
	$(FERN) $(FERNFLAGS) -target $(TARGET) -o $@ tests/unit.fern

build/trace: tests/trace.fern $(SOURCES) | build
	$(FERN) $(FERNFLAGS) -target $(TARGET) -o $@ tests/trace.fern

test: build/ulc build/unit build/trace
	python3 tests/generate_noise_tables.py
	./build/unit
	python3 tests/integration.py

clean:
	rm -rf build
