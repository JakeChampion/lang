package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var arrstructBoundElemCases = []struct {
	name string
	src  string
	want int
}{
	{"loop-bound-elem", `struct Val { kind: i32, kids: i32[] }
function bnd(n: i32): i32 {
    let vals: Val[] = [];
    let total: i32 = 0;
    for i in 0..n {
        let v: Val = Val { kind: i, kids: [i, i + 1] };
        vals = vals.append(v);
        total = total + vals.len();
    }
    return total;
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    for i in 0..n { acc = acc + bnd(8); }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1800) { return 91; }
    if (y != 3600) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"straightline-bound-elem", `struct Val { kind: i32, kids: i32[] }
function line(k: i32): i32 {
    let vals: Val[] = [];
    let v: Val = Val { kind: k, kids: [k] };
    vals = vals.append(v);
    return vals.len();
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    for i in 0..n { acc = acc + line(i); }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 50) { return 91; }
    if (y != 100) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"inline-elem-still-flat", `struct Val { kind: i32, kids: i32[] }
function inl(n: i32): i32 {
    let vals: Val[] = [];
    let total: i32 = 0;
    for i in 0..n {
        vals = vals.append(Val { kind: i, kids: [i, i + 1] });
        total = total + vals.len();
    }
    return total;
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    for i in 0..n { acc = acc + inl(8); }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1800) { return 91; }
    if (y != 3600) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"outside-loop-elem-shared", `struct Val { kind: i32, kids: i32[] }
function haz(n: i32): i32 {
    let v: Val = Val { kind: 7, kids: [7, 8] };
    let vals: Val[] = [];
    let total: i32 = 0;
    for i in 0..n {
        vals = vals.append(v);
        total = total + vals.len();
    }
    return total + v.kids[0] + v.kids[1];
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    for i in 0..n { acc = acc + haz(4); }
    return acc;
}
function main(): i32 {
    let x: i32 = rounds(50);
    let y: i32 = rounds(100);
    if (x != 1250) { return 90; }
    if (y != 2500) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"read-after-push-shared", `struct Val { kind: i32, kids: i32[] }
function after(k: i32): i32 {
    let vals: Val[] = [];
    let v: Val = Val { kind: k, kids: [k] };
    vals = vals.append(v);
    return vals.len() + v.kids[0];
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    for i in 0..n { acc = acc + after(i); }
    return acc;
}
function main(): i32 {
    let x: i32 = rounds(50);
    let y: i32 = rounds(100);
    if (x != 1275) { return 90; }
    if (y != 5050) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostArrStructBoundElemX86_64(t *testing.T) {
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

	for _, tc := range arrstructBoundElemCases {
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
				t.Errorf("%s = %d, want %d (90 = a live buffer was freed and reused; "+
					"91/93 = wrong value; 92 = fresh bytes grew where the shape must be flat; "+
					"99 = over-release/underflow)", tc.name, code, tc.want)
			}
		})
	}
}
