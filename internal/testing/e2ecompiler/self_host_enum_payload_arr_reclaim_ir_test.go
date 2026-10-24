package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var enumPayloadArrReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"recursive-enum-scalar-variant-flat", `enum N { Leaf(i32), Seq(N[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let n: N = Leaf(i); acc = (acc + 1) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let n2: N = Leaf(j); acc = (acc + 1) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recursive-enum-array-payload-flat", `enum N { Leaf(i32), Seq(N[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let n: N = Seq([Leaf(i), Leaf(8)]); acc = (acc + 1) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let n2: N = Seq([Leaf(j), Leaf(8)]); acc = (acc + 1) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recursive-enum-payload-via-local-flat", `enum N { Leaf(i32), Seq(N[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let kids: N[] = [Leaf(i), Leaf(8)]; let n: N = Seq(kids); acc = (acc + 1) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let k2: N[] = [Leaf(j), Leaf(8)]; let n2: N = Seq(k2); acc = (acc + 1) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recursive-enum-values-readable", `enum N { Leaf(i32), Seq(N[]) }
function first_leaf(n: N): i32 {
    match (n) {
        Leaf(v) => { return v; },
        Seq(kids) => { return kids.len(); }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let n: N = Seq([Leaf(i), Leaf(8), Leaf(9)]);
        if (first_leaf(n) != 3) { return 97; }
        let m: N = Leaf(i);
        if (first_leaf(m) != i) { return 97; }
        acc = (acc + 1) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 96; }
    return 0;
}`, 0},
	{"recursive-enum-nested-correct", `enum N { Leaf(i32), Seq(N[]) }
function depth_sum(n: N): i32 {
    match (n) {
        Leaf(v) => { return v; },
        Seq(kids) => {
            let s: i32 = 0;
            let i: i32 = 0;
            while (i < kids.len()) { s = s + depth_sum(kids[i]); i = i + 1; }
            return s;
        }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let inner: N = Seq([Leaf(1), Leaf(2)]);
        let outer: N = Seq([inner, Leaf(3)]);
        if (depth_sum(outer) != 6) { return 97; }
        acc = (acc + 1) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 96; }
    return 0;
}`, 0},
}

func TestSelfHostEnumPayloadArrReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range enumPayloadArrReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = payload still per-iteration; 99 = over-release/underflow; 96-97 = value corrupted)",
					tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostEnumPayloadArrReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range enumPayloadArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
