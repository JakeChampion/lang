package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupStructTypePathCases pin the #4365 reclaim of a struct inside a tuple
// element: `Option[(i32, P)]` / `(i32, P)[]`, where P sole-owns an rc-array
// field, must deep-drop the struct every iteration — its rc-array fields, then
// the struct box — along with the option box or the array buffer, on the
// register and wasm backends alike.
//
// SCOPE: the element is consumed SCALAR-only (the consuming match reads `g.0` /
// `xs[i].0`, never the struct). A WHOLE struct extraction (`keep = g.1`) must
// never be over-released.
var tupStructTypePathCases = []struct {
	name string
	src  string
	want int
}{
	// OPTTUP struct element, scalar-consumed: reclaims the struct + option box each iter.
	{"opttup-struct-scalar-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i })); match (o) { Some(g) => { acc = (acc + g.0) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, P)] = Some((j, P { xs: [j, j + 1], y: j })); match (o2) { Some(g) => { acc = (acc + g.0) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ARRTUP struct element, scalar-consumed: reclaims each element's struct + the buffer.
	{"arrtup-struct-scalar-churn", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })]; acc = (acc + xs[0].0) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: (i32, P)[] = [(j, P { xs: [j, j + 1], y: j })]; acc = (acc + ys[0].0) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// OPTTUP struct WHOLE-EXTRACT: `keep = g.1` moves the struct out — leak-safe (the
	// option is left uncredited), never over-released.
	{"opttup-struct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let keep: P = P { xs: [0, 0], y: 0 };
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[(i32, P)] = Some((i, P { xs: [i, i + 1], y: i }));
        match (o) { Some(g) => { keep = g.1; }, None => {} }
        i = i + 1;
    }
    let acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// ARRTUP struct WHOLE-EXTRACT: `keep = xs[0].1` — leak-safe, never over-released.
	{"arrtup-struct-escape-store-safe", `struct P { xs: i32[], y: i32 }
function main(): i32 {
    let keep: P = P { xs: [0, 0], y: 0 };
    let i: i32 = 0;
    while (i < 50) {
        let xs: (i32, P)[] = [(i, P { xs: [i, i + 1], y: i })];
        keep = xs[0].1;
        i = i + 1;
    }
    let acc: i32 = keep.xs[0] + keep.y;
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// REGRESSION: the pre-existing plain array-element OPTTUP still reclaims (the shared
	// admission predicates now thread structs, but the array path is unchanged).
	{"opttup-arrtuple-regression", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) { let o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(g) => { acc = (acc + g.0 + g.1[0]) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, i32[])] = Some((j, [j, j + 1])); match (o2) { Some(g) => { acc = (acc + g.1[1]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

// TestSelfHostTupStructTypePathReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTupStructTypePathReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range tupStructTypePathCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
