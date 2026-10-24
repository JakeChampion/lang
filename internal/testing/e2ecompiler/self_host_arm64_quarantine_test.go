package e2ecompiler

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

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var armPoisonPair = fmt.Sprintf(`movz w\d+, #%d\n\s+movk w\d+, #%d, lsl #16`,
	ast.RcPoison&0xffff, (ast.RcPoison>>16)&0xffff)

func mustMatch(t *testing.T, pat, asm string) bool {
	t.Helper()
	re, err := regexp.Compile(pat)
	if err != nil {
		t.Fatalf("bad pattern %q: %v", pat, err)
	}
	return re.MatchString(asm)
}

const armQuarantineSrc = `import "std/string";
function mk(a: string): string { return a + "!"; }
function main(): i32 {
    let xs: string[] = [mk("x"), mk("y")];
    let s: string = mk("ab");
    let n: i32[] = [1, 2, 3];
    let big: u8[] = __alloc_u8(600000);
    __rc_dec(big);
    return xs.len() + s.len() + n[0];
}`

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

func emitArm64Asm(t *testing.T, h ssaBackendHost, dir, src, name string, env []string) string {
	t.Helper()
	out := filepath.Join(dir, name+".s")
	cmd := exec.Command(h.cli, "-target", "arm64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	cmd.Env = append(append(os.Environ(), uncertified), env...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting %s: %v\n%s", name, err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(asm)
}

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
		cmd.Env = append(os.Environ(), flag, uncertified)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		stderr, code := runCapturingStderr(t, tg, bin)
		if code != e2eharness.ExitSanitizer {
			t.Errorf("%s: exit=%d, want %d (a quarantine finding is fatal)", flag, code, e2eharness.ExitSanitizer)
		}
		if !strings.Contains(stderr, "fern-sanitizer: use-after-free (touched a quarantined block)\n") {
			t.Errorf("%s: stderr does not carry the diagnostic: %q", flag, stderr)
		}
		if flag == "FERN_RC_FREE_DEBUG=1" && strings.Contains(stderr, "leakcheck:") {
			t.Errorf("FERN_RC_FREE_DEBUG=1 alone printed a census: %q", stderr)
		}
	}
}

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
	cmd.Env = append(os.Environ(), uncertified)
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
	for _, marker := range []string{"__fern_san_abort", ".Lsan_", "fern-sanitizer"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off asm contains %q — the mode is not fully gated", marker)
		}
	}
}

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
	if blocks != 2 {
		t.Errorf("verdict says %d blocks, want 2 (the same count native's "+
			"TestX86_64SanitizeLeakVerdict reads for this program)", blocks)
	}
}
