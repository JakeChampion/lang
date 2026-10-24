//go:build ignore

package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

func main() {
	var ranges strings.Builder
	for r := rune(128); r <= unicode.MaxRune; {
		if !strconv.IsPrint(r) {
			r++
			continue
		}
		start := r
		for r < unicode.MaxRune && strconv.IsPrint(r+1) {
			r++
		}
		fmt.Fprintf(&ranges, "%06x%06x", start, r)
		r++
	}
	fmt.Println("// Generator: scripts/gen-doc-printable.go.")
	fmt.Printf("// Unicode version: %s. Reference: `strconv.IsPrint`.\n", unicode.Version)
	fmt.Println("//")
	fmt.Println("// Each pair is an inclusive range of six-digit hexadecimal scalars.")
	fmt.Printf("pub function printable_ranges(): string {\n  return %q;\n}\n", ranges.String())
}
