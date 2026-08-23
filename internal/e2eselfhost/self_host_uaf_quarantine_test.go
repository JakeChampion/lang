package e2eselfhost

import (
	"strings"
	"testing"
)

// --- The use-after-free quarantine (the native RcFreeDebug port) -------------
//
// Under FERN_RC_FREE_DEBUG — or FERN_SANITIZE, which implies it
// (ast.ApplySanitize parity) — NOTHING is recycled: every free site writes
// ast.RcPoison (0x7EEDFACE) over the block's rc word where it has one and
// declines its freelist push, and the rc helpers die with the named report the
// moment a stale reference touches a poisoned block. The poison is a large
// POSITIVE value precisely so the immortal (`js`) guard cannot swallow it.
//
// A quarantined block still counts as a FREE for the census (the hev hook runs
// at the release), so the leak detector composes with this one instead of
// reading every correct free as a leak — the clean-run census assertions in
// self_host_sanitizer_test.go pin that composition.
//
// This was the sanitizer's one behavioural gap versus native (#5545); the
// remaining gap is the missing backtrace under the report.

const uafPoisonDec = "2129656526" // ast.RcPoison, as the emitted decimal

// uafSelfHostIncSrc retains a block the runtime has already freed: the first
// __rc_dec reclaims the rc==1 buffer (this runtime's __rc_dec maps to the
// freeing __fn___fern_arr_dec) and quarantines it; the __rc_inc then touches
// the poison — the inc-side check, which the dec-side double-free test in
// self_host_sanitizer_test.go does not reach.
const uafSelfHostIncSrc = `function main(): i32 {
    var a: u8[] = __alloc_u8(16);
    __rc_dec(a);
    __rc_inc(a);
    return 0;
}`

func TestSelfHostUafIncAfterFreeReportedX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "uaf_inc", uafSelfHostIncSrc, []string{"FERN_SANITIZE=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != sanExitStatus {
		t.Errorf("exit=%d, want %d (a quarantine finding is fatal)", code, sanExitStatus)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
		t.Errorf("stderr does not carry the diagnostic: %q", stderr)
	}
}

// The flag stands alone, without the rest of the sanitizer — the
// FERN_RC_FREE_DEBUG=1 probe-binary path native documents on RcFreeDebug.
func TestSelfHostUafStandaloneFlagX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "uaf_alone", uafSelfHostIncSrc, []string{"FERN_RC_FREE_DEBUG=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != sanExitStatus {
		t.Errorf("exit=%d, want %d", code, sanExitStatus)
	}
	if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
		t.Errorf("stderr does not carry the diagnostic: %q", stderr)
	}
	// Standalone means standalone: no census summary, no over-release
	// machinery — the flag must not drag the rest of the sanitizer in.
	if strings.Contains(stderr, "leakcheck:") {
		t.Errorf("FERN_RC_FREE_DEBUG=1 alone printed a census: %q", stderr)
	}
}

// Without any flag the same program runs to completion silently — the
// detector is opt-in, not a change to what the compiler emits by default.
func TestSelfHostUafSilentWithoutFlagX86_64(t *testing.T) {
	bin, runner := sanSelfHostBuild(t, "uaf_off", uafSelfHostIncSrc, nil)
	stderr, code := hevRun(t, runner, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (an unsanitized build must not abort)", code)
	}
	if stderr != "" {
		t.Errorf("stderr=%q, want empty", stderr)
	}
}

// The asm contract, in both directions. Under the flag: the poison store, the
// poison compare routed to the named abort, the abort body with the exact
// message — and NO small-tier freelist push anywhere (every push site uses the
// `, %r8`-form freelist address; __fern_alloc's pop uses %rdx and survives, so
// the allocator keeps bumping over empty lists). Without the flag: none of it,
// the cheap proxy for byte-identical.
func TestSelfHostUafQuarantineAsmContractX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// String + string[] + array churn, so the str_free / str_arr_free /
	// arr_dec bodies are all emitted alongside the always-present helpers.
	src := `import "std/string";
function mk(a: string): string { return a + "!"; }
function main(): i32 {
    var xs: string[] = [mk("x"), mk("y")];
    var s: string = mk("ab");
    var n: i32[] = [1, 2, 3];
    return xs.len() + s.len() + n[0];
}`

	on := hevCompile(t, runner, driverBin, src, []string{"FERN_RC_FREE_DEBUG=1"})
	for _, want := range []string{
		"movl $" + uafPoisonDec + ",",      // the quarantine store
		"cmpl $" + uafPoisonDec + ", %ecx", // the rc-helper checks
		"je __fern_san_abort_uaf",          // ...routed to the named abort
		"__fern_san_abort_uaf:",            // the abort body
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
