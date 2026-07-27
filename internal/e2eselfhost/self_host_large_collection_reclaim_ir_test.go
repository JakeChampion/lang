package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLargeCollectionReclaimIRX86_64 pins the self-host runtime's
// LARGE-TIER recycle of collection OUTER buffers (#3425 follow-up): the
// __fn___fern_str_arr_free (.Lsaf) and __fern_arr_push_owned (.Lapo) buffer
// frees now return a >=512 KiB block to __fern_large_push instead of leaking it
// (sound), the same redirect __fern_arr_dec / __fern_str_free already use.
//
// Small collection buffers were never the issue — they recycle through the
// exact-word-class small freelist regardless. The gap was the >=512 KiB tier:
// pre-fix, .Lsaf / .Lapo `jae`-jumped straight past the freelist push and
// leaked. In a fresh-process emit that never mattered (the block dies with the
// process); for a LONG-RUNNING program that repeatedly builds and drops a large
// fresh collection — the general-purpose workload Fern now targets — it grew RSS
// without bound.
//
// The proof is a BOUNDED HIGH-WATER assertion, the same shape as the element
// reclaim tests: build a fresh string[] whose OUTER buffer clears 512 KiB
// (~70 000 fresh-concat elements → cap ~131 072 → ~1 MiB buffer, and the grow
// reallocs free ~512 KiB old buffers via .Lapo), churn it, and require the bump
// pointer to stay flat across a second churn. With the large-tier recycle every
// build re-serves its big buffers from the large freelist (flat); revert either
// redirect and each build leaks ~1 MiB → the high-water climbs → exit 98. A
// double-free would tick the over-release detector → 99. All probes are IR-path
// builtins (__heap_bump_bytes / __rc_underflow) so the program stays on the IR
// path.
func TestSelfHostLargeCollectionReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	// build() constructs a fresh, non-escaping string[] of ~70 000 fresh-concat
	// elements (SARR-admitted: every element is a provably fresh concat, so the
	// exit sweep frees it with __fern_str_arr_free, whose outer buffer is >512 KiB
	// and takes .Lsaf's large tier). The grow reallocs free the superseded old
	// buffers via .Lapo, whose >=512 KiB steps take .Lapo's large tier. Only the
	// array length is read out, so nothing escapes.
	prog := `function build(pre: string): i32 {
  var xs: string[] = [pre + "a"];
  var i: i32 = 0;
  while (i < 70000) { xs = xs.append(pre + "b"); i = i + 1; }
  return xs.len();
}
function churn(n: i32): i32 {
  var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0;
  while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; }
  return acc;
}
function main(): i32 {
  var w: i32 = churn(3);
  var b1: i32 = __heap_bump_bytes();
  var x: i32 = churn(3);
  var b2: i32 = __heap_bump_bytes();
  if (__rc_underflow() != 0) { return 99; }
  if (b2 - b1 >= 1048576) { return 98; }
  if (w != x) { return 97; }
  return 0;
}`

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "large-collection-reclaim", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("large-collection-reclaim exited %d, want 0 (98 = >=512 KiB outer buffer leaked → bump grew; 99 = over-release; 97 = value corrupted)", code)
	}
}
