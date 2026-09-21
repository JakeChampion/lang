package e2eselfhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/ast"
)

// --- The quarantine on arm64 (#9882) ----------------------------------------
//
// The x86-64 half of this detector is in self_host_uaf_quarantine_test.go; this
// file holds the arm64 half. The contract is the same one — poison every
// released rc word, decline every freelist push, abort on a stale touch — and
// the poison word is ast.RcPoison on both sides, so these assertions derive it
// rather than repeating a literal.

// armPoisonPair is ast.RcPoison as arm64 materialises it: no 32-bit move
// immediate reaches the value, so it arrives in two halves. Register-agnostic,
// because which scratch a site is free to clobber is the emitter's business.
var armPoisonPair = fmt.Sprintf(`movz w\d+, #%d\n\s+movk w\d+, #%d, lsl #16`,
	ast.RcPoison&0xffff, (ast.RcPoison>>16)&0xffff)

// mustMatch reports whether the multi-line pattern appears in the asm. The
// patterns here span instructions, so they are compiled per call rather than
// hoisted: a bad one is a test bug, and failing at the assertion names it.
func mustMatch(t *testing.T, pat, asm string) bool {
	t.Helper()
	re, err := regexp.Compile(pat)
	if err != nil {
		t.Fatalf("bad pattern %q: %v", pat, err)
	}
	return re.MatchString(asm)
}

// armQuarantineSrc churns strings, a string[] and an array, so str_free,
// str_arr_free and arr_dec are all emitted alongside the always-present rc
// helpers — every body the quarantine has to reach.
const armQuarantineSrc = `import "std/string";
function mk(a: string): string { return a + "!"; }
function main(): i32 {
    var xs: string[] = [mk("x"), mk("y")];
    var s: string = mk("ab");
    var n: i32[] = [1, 2, 3];
    return xs.len() + s.len() + n[0];
}`

// arm64Target picks the arm64-linux entry out of this host's runnable targets,
// skipping when the host neither is arm64 nor has qemu-aarch64.
func arm64Target(t *testing.T, h ssaBackendHost) ssaBackendTarget {
	t.Helper()
	for _, tg := range h.targets {
		if tg.target == "arm64-linux" {
			return tg
		}
	}
	t.Skip("no runnable arm64-linux target on this host (needs arm64 or qemu-aarch64)")
	return ssaBackendTarget{}
}

// runCapturingStderr is runProduced with the diagnostic kept: the sanitizer
// writes its report to stderr and nothing else reads it.
func runCapturingStderr(t *testing.T, tg ssaBackendTarget, bin string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if len(tg.runner) == 0 {
		cmd = exec.CommandContext(ctx, bin)
	} else {
		cmd = exec.CommandContext(ctx, tg.runner[0], append(tg.runner[1:], bin)...)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally: %v", bin, cmd.ProcessState)
	}
	return stderr.String(), cmd.ProcessState.ExitCode()
}

// emitArm64Asm compiles src for arm64-linux with the given environment and
// returns the emitted assembly.
func emitArm64Asm(t *testing.T, h ssaBackendHost, dir, src, name string, env []string) string {
	t.Helper()
	out := filepath.Join(dir, name+".s")
	cmd := exec.Command(h.cli, "-target", "arm64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	cmd.Env = append(os.Environ(), env...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting %s: %v\n%s", name, err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(asm)
}

// The asm contract, in both directions. Under the flag: the poison store, the
// poison compare routed to the named abort, the abort body with the exact
// message — and no small-tier freelist push anywhere. Every push site takes the
// freelist address into x3, x5 or x6; __fern_alloc's own pop uses x1 and
// survives, so the allocator keeps bumping over empty lists. Without the flag:
// none of it, the cheap proxy for byte-identical.
func TestSelfHostUafQuarantineAsmContractArm64(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "churn.fern")
	if err := os.WriteFile(src, []byte(armQuarantineSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	on := emitArm64Asm(t, h, dir, src, "on", []string{"FERN_RC_FREE_DEBUG=1"})
	if !mustMatch(t, armPoisonPair+`\n\s+stur w\d+, \[x\d+, #-8\]`, on) {
		t.Error("flag-on asm has no quarantine store")
	}
	if !mustMatch(t, armPoisonPair+`\n\s+cmp w\d+, w\d+\n\s+b\.ne \S+\n\s+bl __fern_san_abort_uaf`, on) {
		t.Error("flag-on asm has no poison compare routed to the abort")
	}
	for _, want := range []string{
		"__fern_san_abort_uaf:",
		"fern-sanitizer: use-after-free (touched a quarantined block)",
	} {
		if !strings.Contains(on, want) {
			t.Errorf("flag-on asm is missing %q", want)
		}
	}
	if mustMatch(t, `add x[356], x[356], :lo12:__fern_freelist`, on) {
		t.Error("flag-on asm still pushes onto a freelist — a recycled block would overwrite its own poison")
	}
	// The large tier is the same hazard one size class up, and it has its own
	// choke point: every >=512 KiB free tail-branches to __fern_large_push, so
	// the decline lives in that body rather than at each arm. x3 is the push's
	// freelist address and x1 the allocator's own pop, as in the small tier —
	// matching on the bare symbol would also hit the arena checkpoint's copy.
	if strings.Contains(on, "add x3, x3, :lo12:__fern_large_freelist") {
		t.Error("flag-on asm still pushes onto the large freelist — a recycled >=512 KiB block would overwrite its own poison")
	}
	if !strings.Contains(on, "add x1, x1, :lo12:__fern_large_freelist") {
		t.Error("flag-on asm lost __fern_alloc's large-tier pop")
	}
	if !strings.Contains(on, "add x1, x1, :lo12:__fern_freelist") {
		t.Error("flag-on asm lost __fern_alloc's pop — the allocator must still consult (empty) freelists")
	}

	off := emitArm64Asm(t, h, dir, src, "off", nil)
	if mustMatch(t, armPoisonPair, off) {
		t.Error("flag-off asm materialises the poison — the detector is not fully gated")
	}
	for _, marker := range []string{"__fern_san_abort_uaf", ".Lsan_uaf"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the detector is not fully gated", marker)
		}
	}
	if !mustMatch(t, `add x[356], x[356], :lo12:__fern_freelist`, off) {
		t.Error("flag-off asm has no freelist pushes — the ordinary allocator lost its recycling")
	}
	if !strings.Contains(off, "add x3, x3, :lo12:__fern_large_freelist") {
		t.Error("flag-off asm has no large-tier recycling — the >=512 KiB blocks would leak")
	}
}

// The behaviour the asm contract stands for: a retain of a block the runtime
// already freed dies with the named report, under FERN_SANITIZE and under the
// standalone FERN_RC_FREE_DEBUG alike.
func TestSelfHostUafIncAfterFreeReportedArm64(t *testing.T) {
	h := selfHostCLIForHost(t)
	tg := arm64Target(t, h)
	dir := t.TempDir()
	src := filepath.Join(dir, "uaf.fern")
	if err := os.WriteFile(src, []byte(uafSelfHostIncSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"FERN_SANITIZE=1", "FERN_RC_FREE_DEBUG=1"} {
		bin := filepath.Join(dir, "uaf-"+strings.Split(flag, "=")[0])
		cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		cmd.Env = append(os.Environ(), flag)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		stderr, code := runCapturingStderr(t, tg, bin)
		if code != sanExitStatus {
			t.Errorf("%s: exit=%d, want %d (a quarantine finding is fatal)", flag, code, sanExitStatus)
		}
		if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
			t.Errorf("%s: stderr does not carry the diagnostic: %q", flag, stderr)
		}
		// Standalone means standalone: the quarantine flag alone must not drag
		// the census in.
		if flag == "FERN_RC_FREE_DEBUG=1" && strings.Contains(stderr, "leakcheck:") {
			t.Errorf("FERN_RC_FREE_DEBUG=1 alone printed a census: %q", stderr)
		}
	}
}

// Without any flag the same program runs to completion silently — the detector
// is opt-in, not a change to what the compiler emits by default.
func TestSelfHostUafSilentWithoutFlagArm64(t *testing.T) {
	h := selfHostCLIForHost(t)
	tg := arm64Target(t, h)
	dir := t.TempDir()
	src := filepath.Join(dir, "uaf.fern")
	if err := os.WriteFile(src, []byte(uafSelfHostIncSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "uaf-off")
	cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	stderr, code := runCapturingStderr(t, tg, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0 (an unsanitized build must not abort)", code)
	}
	if stderr != "" {
		t.Errorf("stderr=%q, want empty", stderr)
	}
}

// The over-release report is the second thing FERN_SANITIZE now means on
// arm64. It has no reachable program in this runtime — __rc_dec maps to the
// freeing __fn___fern_arr_dec, so a double free hits the quarantine's poison
// one instruction before the underflow test would see a zero that no longer
// exists, exactly as the x86-64 leg documents. So this is an asm contract: the
// bump arms call the report, the report body carries the native text, and both
// vanish with the flag.
func TestSelfHostOverReleaseReportArm64(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "churn.fern")
	if err := os.WriteFile(src, []byte(armQuarantineSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	on := emitArm64Asm(t, h, dir, src, "df-on", []string{"FERN_SANITIZE=1"})
	for _, want := range []string{
		"bl __fern_san_abort\n",
		"__fern_san_abort:",
		"fern-sanitizer: rc over-release (double free)",
	} {
		if !strings.Contains(on, want) {
			t.Errorf("flag-on asm is missing %q", want)
		}
	}
	off := emitArm64Asm(t, h, dir, src, "df-off", nil)
	for _, marker := range []string{"__fern_san_abort", ".Lsan_df"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the report is not fully gated", marker)
		}
	}
}

// FERN_SANITIZE folds the census in on arm64 as it does on x86-64, and a
// program that reclaims everything it takes is silent of `fern-sanitizer:`
// lines — so "was this run clean" is answerable without reading the numbers.
func TestSelfHostSanitizeCleanRunIsSilentArm64(t *testing.T) {
	h := selfHostCLIForHost(t)
	tg := arm64Target(t, h)
	dir := t.TempDir()
	src := filepath.Join(dir, "clean.fern")
	if err := os.WriteFile(src, []byte(sanSelfHostCleanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "clean")
	cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	stderr, code := runCapturingStderr(t, tg, bin)
	if code != 0 {
		t.Errorf("exit=%d, want 0", code)
	}
	if !strings.Contains(stderr, "leakcheck: allocs=") {
		t.Errorf("FERN_SANITIZE did not fold the census in: %q", stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("a clean run reported a finding: %q", stderr)
	}
}

// The leak verdict, the third: a positive balance at exit names itself in the
// native backends' words. The block count is what this asserts — the byte
// figure depends on an allocation granularity the allocator is free to change.
func TestSelfHostSanitizeLeakVerdictArm64(t *testing.T) {
	h := selfHostCLIForHost(t)
	tg := arm64Target(t, h)
	dir := t.TempDir()
	src := filepath.Join(dir, "leak.fern")
	if err := os.WriteFile(src, []byte(sanSelfHostLeakSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "leak")
	cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	stderr, code := runCapturingStderr(t, tg, bin)
	if code != 42 {
		t.Errorf("exit=%d, want 42 (the verdict must not clobber main's exit code)", code)
	}
	m := sanSelfHostLeakRe.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict line in stderr: %q", stderr)
	}
	blocks, _ := strconv.Atoi(m[2])
	if blocks != 3 {
		t.Errorf("verdict says %d blocks, want 3", blocks)
	}
}
