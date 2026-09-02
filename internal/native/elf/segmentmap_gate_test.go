package elf_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// callRe matches an assembler entry point being handed a segment map, e.g.
//
//	nativex86.AssembleProgramWX(asm, nativeelf.SegmentAddrsWXX86)
//
// group 1 is the package qualifier, group 2 the map's target suffix.
var callRe = regexp.MustCompile(`(\w+)\.Assemble\w+\([^()]*?\bSegmentAddrs(?:WX|PIE)(X86|Arm64)\b`)

// targetOf classifies a package qualifier as the ISA its assembler emits.
// The qualifiers in use are the import aliases this repository picks:
// nativex86 / x86_64 / x86, and nativearm64 / arm64 / na.
func targetOf(qualifier string) string {
	q := strings.ToLower(qualifier)
	switch {
	case strings.Contains(q, "x86"):
		return "X86"
	case strings.Contains(q, "arm64"), q == "na":
		return "Arm64"
	}
	return ""
}

// TestSegmentMapMatchesAssembler is the gate on a mismatch the compiler
// cannot see. elf.SegmentAddrsWXX86 and elf.SegmentAddrsWXArm64 have the
// same type, so handing the arm64 map to the x86-64 assembler builds
// cleanly — and then the assembler resolves .rodata references against a
// 64 KiB boundary while the ELF writer puts the data segment on a 4 KiB
// one. The binary is well-formed and reads the wrong bytes.
//
// Only a running binary catches that otherwise, and only on the arch whose
// e2e suite happens to run. Four call sites had it wrong when the segment
// map landed, which is why this scans instead of trusting review.
func TestSegmentMapMatchesAssembler(t *testing.T) {
	root := "../../.."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Vendored trees and scratch worktrees are not ours to gate.
			if n := d.Name(); n == ".git" || n == "vendor" || n == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			for _, m := range callRe.FindAllStringSubmatch(line, -1) {
				want := targetOf(m[1])
				if want == "" {
					t.Errorf("%s:%d: cannot tell which ISA %q assembles; teach targetOf about it or the gate is blind here", path, i+1, m[1])
					continue
				}
				if m[2] != want {
					t.Errorf("%s:%d: %s.Assemble… is given the %s segment map, want %s — the assembler and the ELF writer would place the data segment differently",
						path, i+1, m[1], m[2], want)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSegmentMapGateSeesCalls keeps the scan honest: if the call shape or the
// naming changes and the regex stops matching, every check above passes
// vacuously.
func TestSegmentMapGateSeesCalls(t *testing.T) {
	n := 0
	err := filepath.WalkDir("../../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if nm := d.Name(); nm == ".git" || nm == "vendor" || nm == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n += len(callRe.FindAllStringSubmatch(string(src), -1))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 15 {
		t.Fatalf("the scan found only %d segment-map call sites — the pattern has gone stale, which makes TestSegmentMapMatchesAssembler vacuous", n)
	}
}
