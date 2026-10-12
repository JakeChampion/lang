package main

import (
	"os"
	"testing"
)

// The committed module must be what the generator makes of the committed
// dictionary.
func TestCommittedFileIsUpToDate(t *testing.T) {
	const path = "../../" + outputPath
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("dictionary.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := generate(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s is out of date for cmd/brotlidictgen/dictionary.bin. Re-run: go run ./cmd/brotlidictgen", path)
	}
}

// A byte changed is refused, not written.
func TestRefusesAnotherDictionary(t *testing.T) {
	data, err := os.ReadFile("dictionary.bin")
	if err != nil {
		t.Fatal(err)
	}
	data[100] ^= 1
	if _, err := generate(data); err == nil {
		t.Fatal("a changed dictionary was accepted")
	}
}
