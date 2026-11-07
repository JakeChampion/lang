FERN ?= ../../bin/fern
TARGET ?= x86-64-linux
SOURCES := $(wildcard *.fern)

.PHONY: all check test clean
all: build/ulc

build:
	mkdir -p $@

build/ulc: $(SOURCES) | build
	$(FERN) -target $(TARGET) -o $@ ulc.fern

check:
	$(FERN) -check ulc.fern
	$(FERN) -check tests/unit.fern

build/unit: tests/unit.fern $(SOURCES) | build
	$(FERN) -target $(TARGET) -o $@ tests/unit.fern

test: build/ulc build/unit
	./build/unit
	python3 tests/integration.py

clean:
	rm -rf build
