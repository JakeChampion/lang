package e2ecompiler

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var uafPoisonDec = strconv.Itoa(ast.RcPoison)

const uafSelfHostIncSrc = `function main(): i32 {
    let a: u8[] = __alloc_u8(16);
    __rc_dec(a);
    __rc_inc(a);
    return 0;
}`

func TestSelfHostUafIncAfterFreeReportedX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "uaf_inc", uafSelfHostIncSrc, []string{"FERN_SANITIZE=1", uncertified})
	stderr, code := hevRun(t, runner, bin)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d (a quarantine finding is fatal)", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
		t.Errorf("stderr does not carry the diagnostic: %q", stderr)
	}
}

func TestSelfHostUafStandaloneFlagX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "uaf_alone", uafSelfHostIncSrc, []string{"FERN_RC_FREE_DEBUG=1", uncertified})
	stderr, code := hevRun(t, runner, bin)
	if code != e2eharness.ExitSanitizer {
		t.Errorf("exit=%d, want %d", code, e2eharness.ExitSanitizer)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
		t.Errorf("stderr does not carry the diagnostic: %q", stderr)
	}
	if strings.Contains(stderr, "leakcheck:") {
		t.Errorf("FERN_RC_FREE_DEBUG=1 alone printed a census: %q", stderr)
	}
}

func TestSelfHostUafSilentWithoutFlagX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "uaf_off", uafSelfHostIncSrc, []string{uncertified})
	stderr, code := hevRun(t, runner, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (an unsanitized build must not abort)", code)
	}
	if stderr != "" {
		t.Errorf("stderr=%q, want empty", stderr)
	}
}

func TestSelfHostUafQuarantineAsmContractX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := `import "std/string";
function mk(a: string): string { return a + "!"; }
function main(): i32 {
    let xs: string[] = [mk("x"), mk("y")];
    let s: string = mk("ab");
    let n: i32[] = [1, 2, 3];
    return xs.len() + s.len() + n[0];
}`

	on := hevCompile(t, runner, driverBin, src, []string{"FERN_RC_FREE_DEBUG=1"})
	for _, want := range []string{
		"movl $" + uafPoisonDec + ",",
		"cmpl $" + uafPoisonDec + ", %ecx",
		"je __fern_san_abort_uaf",
		"__fern_san_abort_uaf:",
		"fern-sanitizer: use-after-free (touched a quarantined block)",
	} {
		if !strings.Contains(on, want) {
			t.Errorf("flag-on asm is missing %q", want)
		}
	}
	if strings.Contains(on, "__fern_freelist(%rip), %r8") {
		t.Error("flag-on asm still pushes onto a freelist — a recycled block would overwrite its own poison")
	}
	if !strings.Contains(on, "__fern_freelist(%rip), %rdx") {
		t.Error("flag-on asm lost __fern_alloc's pop — the allocator must still consult (empty) freelists")
	}

	off := hevCompile(t, runner, driverBin, src, nil)
	for _, marker := range []string{uafPoisonDec, "__fern_san_abort_uaf", ".Lsan_uaf"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the detector is not fully gated", marker)
		}
	}
	if !strings.Contains(off, "__fern_freelist(%rip), %r8") {
		t.Error("flag-off asm has no freelist pushes — the ordinary allocator lost its recycling")
	}
}
