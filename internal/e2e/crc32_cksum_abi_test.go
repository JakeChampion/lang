package e2e

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// AAPCS64 makes x19..x28 the callee's to preserve and reserves x18 as the
// platform register, and generated code leans on the first half of that: the
// closure path parks its data and vtable in x19/x20 across a `bl` BECAUSE the
// callee restores them. A runtime helper that writes either one corrupts its
// caller rather than itself.
//
// The differential corpus cannot see this. Its call sites are straight-line
// expressions with nothing live across the call, so a kernel that destroyed
// x19/x20 passed all 487 cases on every leg and still miscompiled. The
// contract is what needs asserting, not a program that happens to expose it.
//
// Scoped to __fern_crc32_cksum because that is the kernel this guards; the
// helpers that legitimately use the callee-saved range (strcat and friends)
// save and restore them around their own bodies.
func TestArm64Crc32CksumKeepsCalleeSavedRegisters(t *testing.T) {
	const src = `function main(): i32 { return __crc32_cksum(0, "abcdefghijklmnopqrstuvwxyz0123456789"); }`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	body := kernelBody(t, asm, "__fern_crc32_cksum")
	// x18 and x19..x28, in both the x and w spellings, as whole tokens so
	// that x1 / x2 and the .Lcrc32_bit labels do not match.
	bad := regexp.MustCompile(`\b[xw](1[89]|2[0-8])\b`)
	var hits []string
	for _, line := range strings.Split(body, "\n") {
		if m := bad.FindString(line); m != "" {
			hits = append(hits, strings.TrimSpace(line)+"   ("+m+")")
		}
	}
	if len(hits) > 0 {
		t.Errorf("__fern_crc32_cksum touches %d callee-saved or platform register(s) with no save/restore pair; "+
			"a caller holding a live value there is silently corrupted:\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// kernelBody returns the lines of one emitted runtime helper, from its label
// to its .size directive.
func kernelBody(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+name+":")
	if start < 0 {
		t.Fatalf("%s is not in the emitted assembly — the program did not reach the kernel", name)
	}
	rest := asm[start:]
	if end := strings.Index(rest, ".size "+name); end >= 0 {
		return rest[:end]
	}
	t.Fatalf("%s has no .size directive; cannot bound its body", name)
	return ""
}
