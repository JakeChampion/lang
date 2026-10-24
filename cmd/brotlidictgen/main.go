package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
)

const (
	outputPath = "internal/stdlib/std/brotli/dictionary.fern"
	size       = 122784
	digest     = "20e42eb1b511c21806d4d227d07e5dd06877d8ce7b3a817f378f313653f35c70"
)

func main() {
	data, err := os.ReadFile("cmd/brotlidictgen/dictionary.bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := generate(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputPath, []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	if len(data) != size || hex.EncodeToString(sum[:]) != digest {
		return "", fmt.Errorf("dictionary.bin is not RFC 7932's dictionary: %d bytes, sha256 %x", len(data), sum)
	}
	return fmt.Sprintf(`// This module provides the static Brotli dictionary from RFC 7932, Appendix A.
//
// The dictionary contains %d bytes.
//
// Generator: cmd/brotlidictgen.
// Source: cmd/brotlidictgen/dictionary.bin.
// SHA-256: %s.
//
// Use `+"`go run ./cmd/brotlidictgen`"+` to regenerate this file.

import "std/base64";
import "std/codec";

pub const SIZE: i32 = %d;

pub function bytes(): Result[u8[], codec.DecodeError] {
  return base64.decode("%s");
}
`, size, digest, size, base64.StdEncoding.EncodeToString(data)), nil
}
