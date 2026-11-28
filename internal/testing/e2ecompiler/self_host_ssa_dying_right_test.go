package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A commutative operation whose result is spilled computes in the register
// of its right operand when that operand dies there and the left one does
// not, so no copy of the left operand goes through %r11. The chi step of
// Keccak, x ^ (~y & z), is that shape: x lives on, and ~y & z dies at the xor.
func TestSelfHostSSASpilledXorWritesDyingRight(t *testing.T) {
	fn := sha3BlocksListing(t)
	copied := regexp.MustCompile(`(?m)^\s+movq %r\w+, %r11\n\s+xorq %r\w+, %r11\n\s+movq %r11, -\d+\(%rbp\)$`)
	if n := len(copied.FindAllString(fn, -1)); n != 0 {
		t.Errorf("sha3_blocks copies a live left operand to %%r11 for %d spilled xors:\n%s", n, fn)
	}
	inPlace := 0
	xor := regexp.MustCompile(`(?m)^\s+xorq (?:%r\w+|-\d+\(%rbp\)), (%r\w+)\n\s+movq (%r\w+), -\d+\(%rbp\)$`)
	for _, m := range xor.FindAllStringSubmatch(fn, -1) {
		if m[1] == m[2] && m[1] != "%r11" {
			inPlace++
		}
	}
	if inPlace == 0 {
		t.Errorf("sha3_blocks spills no xor into a dying register, so the shape is not exercised:\n%s", fn)
	}
}

// andn reads its first operand, the one that it does not negate, straight
// from a frame slot, with no load into %rcx first.
func TestSelfHostSSAAndNotReadsSlot(t *testing.T) {
	fn := sha3BlocksListing(t)
	loaded := regexp.MustCompile(`(?m)^\s+movq -\d+\(%rbp\), %rcx\n(?:\s+movq -\d+\(%rbp\), %rdx\n)?\s+andnq %rcx, `)
	if n := len(loaded.FindAllString(fn, -1)); n != 0 {
		t.Errorf("sha3_blocks loads %d andn operands into %%rcx:\n%s", n, fn)
	}
	if !regexp.MustCompile(`(?m)^\s+andnq -\d+\(%rbp\), %r\w+, %r\w+$`).MatchString(fn) {
		t.Errorf("sha3_blocks has no andn that reads a frame slot, so the shape is not exercised:\n%s", fn)
	}
}

// The x86-64 listing of crypto.__sha3_blocks, from a program that hashes
// with SHA3-256.
func sha3BlocksListing(t *testing.T) string {
	t.Helper()
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "sha3.fern")
	prog := `import "std/crypto";

function main(): i32 {
    let d = crypto.sha3_256(__alloc_u8(136 * 4 + args().len()));
    return d[0] as i32;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "sha3.s")
	if report, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, report)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	fn := functionListing(string(asm), "__fn_crypto____sha3_blocks")
	if fn == "" {
		t.Fatal("no __fn_crypto____sha3_blocks in the listing")
	}
	return fn
}
