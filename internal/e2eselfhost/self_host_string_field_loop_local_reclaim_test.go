package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4357 (string-field sibling of #4733): a reclaimable struct loop-local carrying
// a `string` field set from a FRESH string (`while { var t: S = S { x: i, name:
// pre + "x" }; }`) leaked its string box every iteration — the loop-rebind reinit
// freed only the box / array fields (the array-only __field_reclaim path skips
// strings). The fix routes such a binding through __struct_drop + box dec
// (emit_struct_deep_reinit_store), whose k_str arm frees the string field.
//
// SOUNDNESS: it fires ONLY when every string field of the literal is a provably
// FRESH, sole-owned string (expr_is_fresh_str — a concat / fresh-producer call).
// A NON-fresh (aliased bare-ident) string field is retained by its aliasing owner,
// so freeing it would double-release (guarded by strdrop-two-alias-detector in
// TestSelfHostRcPreciseDropX86IR); those fall through to the leak-safe path. The
// exit sweep is deliberately NOT widened (it can't see the construction site).
//
// Gated on the self-host x86-64 IR path: FIXPOINT (bump growth equal at N=50 /
// N=5000) + OVER-RELEASE (the freshly-built string is read each iteration).

func stringFieldLoopLocalFreshSrc(n string) string {
	return `struct S { x: i32, name: string }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var pre: string = "n";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var t: S = S { x: i, name: pre + "x" }; acc = acc + t.x + t.name.len(); i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A struct carrying BOTH an rc-array and a fresh-string field: the loop-rebind must
// free both the xs buffer and the name box each iteration.
func stringAndArrayFieldLoopLocalSrc(n string) string {
	return `struct S { x: i32, xs: i32[], name: string }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var pre: string = "n";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var t: S = S { x: i, xs: [i, i + 1], name: pre + "x" }; acc = acc + t.x + t.xs[0] + t.name.len(); i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// stringFieldLoopLocalRetainedSrc is the fixpoint check's positive control: the
// same shape, but every struct is kept alive in an array, so the boxes cannot be
// recycled and the bump MUST move. It answers 1/0 rather than a byte count
// because the value leaves as a process exit code, which is masked to 8 bits — a
// magnitude would compare mod 256.
//
// It replaces an earlier `small != 0` guard on the bounded cases. That guard read
// "the measurement is live", but what it actually measured was the one-time boxing
// of the `"n"` / `"x"` literals: since #7080 a literal is static data and allocates
// nothing, so a fully-reclaiming loop legitimately reports a high-water of 0 and
// the old guard fired on the fix. This control tests the property the guard was
// reaching for, and does it on a shape where a zero answer is unambiguously wrong.
func stringFieldLoopLocalRetainedSrc(n string) string {
	return `struct S { x: i32, name: string }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var pre: string = "n";
    var keep: S[] = [];
    var i: i32 = 0;
    while (i < ` + n + `) { var t: S = S { x: i, name: pre + "x" }; keep = keep.append(t); i = i + 1; }
    if (keep.len() != ` + n + `) { return 9; }
    if ((__heap_bump_bytes() as i32) > before) { return 1; }
    return 0;
}`
}

// The freshly-built `name` (pre + "cd" = "abcd", len 4) is read every iteration; a
// wrong free of the live string corrupts the sum or trips __rc_underflow. sum x =
// 0..199 = 19900; + 4*200 = 800 -> 20700.
const stringFieldLoopLocalDetectorSrc = `struct S { x: i32, name: string }
function main(): i32 {
    var pre: string = "ab";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { var t: S = S { x: i, name: pre + "cd" }; acc = acc + t.x + t.name.len(); i = i + 1; }
    if (acc != 20700) { return 99; }
    return __rc_underflow();
}`

func TestSelfHostStringFieldLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	shapes := []struct {
		name string
		src  func(n string) string
	}{
		{"fresh-string-field", stringFieldLoopLocalFreshSrc},
		{"fresh-string-and-array-field", stringAndArrayFieldLoopLocalSrc},
	}
	for _, sh := range shapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("struct-with-fresh-string loop-local (%s) bump must be bounded: N=50 -> %d, N=5000 -> %d (string box leaked per iteration)", sh.name, small, large)
			}
		})
	}

	// Non-vacuity for the two cases above: a bounded high-water is only evidence
	// of reclaim if the bump would have moved without it. Retaining every struct
	// is the same allocation pattern with the frees removed, and it must be seen.
	t.Run("fixpoint-bounded/retained-control-grows", func(t *testing.T) {
		if got := run(t, "stringfield-retained-5000", stringFieldLoopLocalRetainedSrc("5000")); got != 1 {
			t.Errorf("retaining 5000 structs must move the heap bump, got %d (9=length mismatch, 0=bump did not move, so the bounded cases above prove nothing)", got)
		}
	})

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "stringfield-loop-detector", stringFieldLoopLocalDetectorSrc); code != 0 {
			t.Errorf("fresh-string-field loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow)", code)
		}
	})
}
