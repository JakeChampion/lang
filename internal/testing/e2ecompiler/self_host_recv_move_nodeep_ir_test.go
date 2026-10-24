package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var recvMoveNoDeepCases = []struct {
	name string
	src  string
	want int
}{
	{"receiver-move-inplace-append-survives-sweep", `struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S {
    return S { ops: self.ops.append(P { x: v }), n: self.n + 1 };
}
function build(): S {
    let ms: S = S { ops: [], n: 0 };
    ms = ms.emit(1);
    ms = ms.emit(2);
    ms = ms.emit(3);
    return ms.emit(4);
}
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let r: S = build();
    let c: i32 = churn(64);
    if (r.ops.len() != 4) { return 90; }
    if (r.ops[0].x != 1 || r.ops[1].x != 2 || r.ops[2].x != 3 || r.ops[3].x != 4) { return 91; }
    if (r.n != 4) { return 92; }
    if (c != 64) { return 93; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"snapshot-local-receiver-move-survives-sweep", `struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S {
    return S { ops: self.ops.append(P { x: v }), n: self.n + 1 };
}
function mk(): S {
    return S { ops: [], n: 0 };
}
function build(): S {
    let ms: S = mk();
    ms = ms.emit(1);
    ms = ms.emit(2);
    ms = ms.emit(3);
    return ms.emit(4);
}
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let r: S = build();
    let c: i32 = churn(64);
    if (r.ops.len() != 4) { return 90; }
    if (r.ops[0].x != 1 || r.ops[1].x != 2 || r.ops[2].x != 3 || r.ops[3].x != 4) { return 91; }
    if (r.n != 4) { return 92; }
    if (c != 64) { return 93; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"read-only-local-still-deep-reclaims", `struct P { x: i32 }
struct S { ops: P[], n: i32 }
function go(k: i32): i32 {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 3) { ps = ps.append(P { x: k + i }); i = i + 1; }
    let s: S = S { ops: ps, n: k };
    return s.ops[0].x + s.n;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 2000) { acc = (acc + go(i)) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 98; }
    return 0;
}`, 0},
}

func TestSelfHostRecvMoveNoDeepIRX86_64(t *testing.T) {
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

	for _, tc := range recvMoveNoDeepCases {
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
				t.Errorf("%s = %d, want %d (91 = moved element boxes freed by the receiver's sweep deep-drop and reused; 99 = over-release/underflow; 139 = heap corruption segfault)", tc.name, code, tc.want)
			}
		})
	}
}
