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

// mapRe matches a use of one of the unwind-carrying address maps.
var mapRe = regexp.MustCompile(`\bSegmentMapWXEh(X86|Arm64)\b`)

// TestUnwindMapUsesAreTypePinned covers the same mismatch for the unwind
// layout, which does not go through an Assemble… call and so is invisible to
// the scan above. The pairing is meant to be a compile error there — the
// address map is chosen inside layoutX86 / layoutArm64, whose parameter types
// name the assembler — so what this checks is that it stayed that way: every
// production use of one of these maps sits in a function whose signature
// names the matching target.
//
// A use in a function that names neither is the shape the gate exists to
// stop: it means the map is being picked somewhere the compiler cannot check
// the choice.
func TestUnwindMapUsesAreTypePinned(t *testing.T) {
	uses := 0
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
		// Test files pick maps freely and assert the addresses they get back.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn := ""
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(line, "func ") {
				fn = line
			}
			// A doc comment names the function it documents, so matching in
			// one would attribute the use to whatever came before it.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			m := mapRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			uses++
			if targetOf(fn) != m[1] {
				t.Errorf("%s:%d: the %s unwind map is chosen in a function whose signature does not name that target (%q) — pick it in a wrapper typed to the assembler instead, so a mismatch does not compile",
					path, i+1, m[1], strings.TrimSuffix(fn, " {"))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The elf package's own two definitions, plus one wrapper per target in
	// the driver. Fewer means the naming moved and this scan sees nothing.
	if uses < 4 {
		t.Fatalf("the scan found only %d uses of the unwind maps — the pattern has gone stale, which makes this test vacuous", uses)
	}
}
