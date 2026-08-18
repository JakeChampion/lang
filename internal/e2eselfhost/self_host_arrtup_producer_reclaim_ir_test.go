package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrtupProducerReclaimCases pin the #4353 CALL-BOUND array-of-boxes reclaim.
// `collect_fresh_arrtup_names` / `collect_fresh_arrstruct_names` admitted only a
// direct array LITERAL initialiser, so `var ps: (i32, i32[])[] = mk(k)` — and its
// array-of-structs sibling — leaked the buffer, every element box and every inner
// array per round: 80 B (x86-64 / arm64) and 48 B (wasm), where native is flat and
// the `string[]` equivalent has been flat since the "STRARR:" producer registry.
//
// The "ARRTUPF:" / "ARRSTRUCTF:" registries close it on the same terms: a free
// function whose EVERY return is a fresh array literal of fresh tuple (resp.
// struct) literals hands its caller a structure the caller solely owns, so a
// binding from it earns the same credit a literal earns. The callee's own value
// escapes by return and keeps the shallow dec, so each box is freed once.
//
// The erased-generic producer (`wrap[T](…): (T, i32[])[]`) is deliberately NOT
// admitted: its return type carries a type var, so there is no concrete element
// tuple for the deep free to walk. That case still measures 80 B/round.
var arrtupProducerReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn, array of tuples from a producer call.
	{"arrtup-producer-churn", `function mk(n: i32): (i32, i32[])[] { return [(n, [n, n + 1]), (n + 1, [n + 2, n + 3])]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var ps: (i32, i32[])[] = mk(i); acc = (acc + ps[0].0) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var qs: (i32, i32[])[] = mk(j); acc = (acc + qs[0].0) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Core churn, array of structs from a producer call.
	{"arrstruct-producer-churn", `struct Q { xs: i32[] }
function mkqs(n: i32): Q[] { return [Q { xs: [n, n + 1] }, Q { xs: [n + 2, n + 3] }]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var ps: Q[] = mkqs(i); acc = (acc + ps[0].xs[0]) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var qs: Q[] = mkqs(j); acc = (acc + qs[0].xs[0]) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE: both classes still read correctly through every position after the
	// reclaim. 5 + 5 + 8 + 2 + 3 + 4 + 1 = 28.
	{"arrtup-producer-value", `struct Q { xs: i32[] }
function mkps(n: i32): (i32, i32[])[] { return [(n, [n, n + 1]), (n + 1, [n + 2, n + 3])]; }
function mkqs(n: i32): Q[] { return [Q { xs: [n, n + 1] }]; }
function main(): i32 {
    var ps: (i32, i32[])[] = mkps(5);
    var qs: Q[] = mkqs(3);
    var v: i32 = ps[0].0 + ps[0].1[0] + ps[1].1[1] + ps.len() + qs[0].xs[0] + qs[0].xs[1] + qs.len();
    if (__rc_underflow() != 0) { return 99; }
    return v;
}`, 28},
	// NON-FRESH PRODUCER negative: `mkbad` puts a PARAM array at an element
	// position, so the returned structure aliases the caller's own array. The
	// registry must refuse it — a deep free here would release `shared` and the
	// reads after the loop would see freed bytes (97) or tick the detector (99).
	{"arrtup-producer-nonfresh-safe", `function mkbad(xs: i32[], n: i32): (i32, i32[])[] { return [(n, xs)]; }
function main(): i32 {
    var shared: i32[] = [1, 2];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var ps: (i32, i32[])[] = mkbad(shared, i);
        acc = (acc + ps[0].0 + ps[0].1[0]) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (shared[0] + shared[1] != 3) { return 97; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// FORWARDED RETURN negative: `keepit` binds the producer's result and hands it
	// straight back, so its local escapes and must keep the shallow dec — the
	// caller's own binding is the one that frees.
	{"arrtup-producer-forwarded-safe", `function mk(n: i32): (i32, i32[])[] { return [(n, [n, n + 1])]; }
function keepit(n: i32): (i32, i32[])[] { var ps: (i32, i32[])[] = mk(n); return ps; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { var r: (i32, i32[])[] = keepit(i); acc = (acc + r[0].0 + r[0].1[1]) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ELEMENT-ALIAS negative: `var t = ps[0]` holds an element tuple box past the
	// reclaim point, so arrarr_row_escapes must keep the local uncredited.
	{"arrtup-producer-elem-alias-safe", `function mk(n: i32): (i32, i32[])[] { return [(n, [n, n + 1])]; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var ps: (i32, i32[])[] = mk(i);
        var t: (i32, i32[]) = ps[0];
        acc = (acc + t.0 + t.1[1] + ps[0].1[0]) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostArrTupProducerReclaimIRX86_64 drives the cases through the
// self-hosted x86-64 compiler, heap-bump + underflow guarded.
func TestSelfHostArrTupProducerReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range arrtupProducerReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = call-bound array-of-boxes leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
