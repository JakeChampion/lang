package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostIRStringFieldRcX86_64(t *testing.T) {
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
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"rebuild-struct-array-string-field", `struct Node { name: string, kids: i32[], n: i32 }
function rebuild(src: Node[]): Node[] {
	let out: Node[] = [];
	let i: i32 = 0;
	while (i < src.len()) {
		let s: Node = src[i];
		out = out.append(Node { name: s.name, kids: s.kids, n: s.n });
		i = i + 1;
	}
	return out;
}
function main(): i32 {
	let src: Node[] = [];
	let i: i32 = 0;
	while (i < 6) { src = src.append(Node { name: "x", kids: [], n: i }); i = i + 1; }
	let r: Node[] = rebuild(src);
	let sum: i32 = 0; let j: i32 = 0;
	while (j < r.len()) { sum = sum + r[j].n; j = j + 1; }
	return r.len() + sum;
}`, 6 + 15},

		{"struct-string-field-beside-array", `struct H { tag: string, n: i32 }
function mk(name: string, k: i32): H { return H { tag: name, n: k }; }
function main(): i32 {
	let guard: i32[] = [7, 8, 9];
	let h: H = mk("hello", guard.len());
	return guard.len() * 10 + h.n + h.tag.len();
}`, 3*10 + 3 + 5},

		{"string-field-through-rebuilds", `struct B { s: string, v: i32 }
function copy_b(x: B): B { return B { s: x.s, v: x.v }; }
function main(): i32 {
	let a: B = B { s: "abcd", v: 3 };
	let b: B = copy_b(a);
	let c: B = copy_b(b);
	return c.s.len() * 10 + c.v;
}`, 4*10 + 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: driver produced no asm", tc.name)
			}
			progBin := buildBin(t, gcc, dir, "sfr_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
