package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostStructDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const cap = "16777216"
	cases := []struct {
		name     string
		src      string
		wantFn   string
		wantBody string
	}{
		{
			"scalar-array-field-reclaim",
			"struct Bag { items: i32[] } " +
				"@noinline function id(xs: i32[]): i32[] { return xs; } " +
				"function mk(): i32 { let b: Bag = Bag { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }; return b.items[0] + b.items[15]; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 500000) { s = mk(); k = k + 1; } return s - 17; }",
			"$__sem_drop_Bag",
			"call $__fern_arr_dec",
		},
		{
			"struct-array-field-reclaim",
			"struct Inner { v: i32 } struct Nest { inners: Inner[] } " +
				"@noinline function id(xs: i32[]): i32[] { return xs; } " +
				"function mk(): i32 { let o: i32 = id([0])[0]; let nz: Nest = Nest { inners: [Inner{v:o+1},Inner{v:o+2},Inner{v:o+3},Inner{v:o+4},Inner{v:o+5},Inner{v:o+6},Inner{v:o+7},Inner{v:o+8}] }; return nz.inners[0].v + nz.inners[7].v; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 9; }",
			"$__sem_drop_Nest",
			"call $__fern_arr_dec",
		},
		{
			"nested-struct-field-reclaim",
			"struct Inner { v: i32, w: i32 } struct Outer { inner: Inner, tag: i32 } " +
				"@noinline function id(xs: i32[]): i32[] { return xs; } " +
				"function mk(): i32 { let o: Outer = Outer { inner: Inner { v: id([5])[0], w: 6 }, tag: 3 }; return o.inner.v + o.inner.w + o.tag; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 500000) { s = mk(); k = k + 1; } return s - 14; }",
			"$__sem_drop_Outer",
			"call $__fern_arr_dec",
		},
		{
			"nested-struct-field-deep-drop",
			"struct Inner { items: i32[] } struct Outer { inner: Inner, tag: i32 } " +
				"@noinline function id(xs: i32[]): i32[] { return xs; } " +
				"function mk(): i32 { let o: Outer = Outer { inner: Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, tag: 7 }; return o.inner.items[0] + o.inner.items[15] + o.tag; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 24; }",
			"$__sem_drop_Outer",
			"call $__sem_release_Inner",
		},
		{
			"nested-struct-field-deep-drop-depth2",
			"struct Inner { items: i32[] } struct Mid { inner: Inner, m: i32 } struct Outer { mid: Mid, tag: i32 } " +
				"@noinline function id(xs: i32[]): i32[] { return xs; } " +
				"function mk(): i32 { let o: Outer = Outer { mid: Mid { inner: Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, m: 2 }, tag: 7 }; return o.mid.inner.items[0] + o.mid.inner.items[15] + o.mid.m + o.tag; } " +
				"function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(); k = k + 1; } return s - 26; }",
			"$__sem_drop_Mid",
			"call $__sem_release_Inner",
		},
		{
			"string-field-reclaim",
			"struct R { name: string, items: i32[] } " +
				"function mk(pre: string): i32 { let r: R = R { name: pre + \"x\", items: [1,2,3,4] }; return r.name.len() + r.items[0]; } " +
				"function main(): i32 { let p: string = \"aa\"; let s: i32 = 0; let k: i32 = 0; while (k < 400000) { s = mk(p); k = k + 1; } return s - 4; }",
			"$__sem_drop_R",
			"call $__fern_arr_dec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			bodies := wasmFuncBodies(string(wat), tc.wantFn+" ")
			if len(bodies) != 1 {
				t.Fatalf("%s: want exactly one %s body, found %d\n--- WAT ---\n%s", tc.name, tc.wantFn, len(bodies), wat)
			}
			if !strings.Contains(bodies[0], tc.wantBody) {
				t.Fatalf("%s: %s body missing real deep-drop\nwant substring:\n%s\n--- BODY ---\n%s", tc.name, tc.wantFn, tc.wantBody, bodies[0])
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run",
				"-W", "max-memory-size="+cap,
				"-W", "trap-on-grow-failure=y",
				"--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s: wasm exited %d, want 0 (a non-zero/trap means the field buffer leaked past the %s-byte cap — the drop did not reclaim)\n--- WAT ---\n%s", tc.name, code, cap, wat)
			}
		})
	}
}
