package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var nestedPassthroughFnValueCases = []struct {
	name string
	src  string
}{
	{"array_elem_nested_passthrough_lambda", `function id[T](x: T): T { return x; }
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(c: boolean, p1: i32): ((i32) => i32)[] { let b: boolean = c; return (if (b) { [((x: i32) => x)] } else { [pick(!b, id(((y: i32) => (y + p1))), ((z: i32) => 9i32))] }); }
function main(): i32 { let fs: ((i32) => i32)[] = gen(false, 5i32); return fs[0i32](3i32) & 63i32; }`},
	{"iife_arm_passthrough_holds_lambda", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 40i32); let n: i32 = 2i32; let t: boolean = false; let xs: ((i32) => i32)[] = [(if (t) { v0 } else { pick(t, v0, ((x: i32) => (x + n))) })]; return xs[0i32](3i32) & 63i32; }`},

	{"returned_array_passthrough_element", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(c: boolean, p1: i32): ((i32) => i32)[] { let s: boolean = !c; return [pick(s, ((y: i32) => (y + p1)), ((z: i32) => 9i32))]; }
function main(): i32 { let fs: ((i32) => i32)[] = gen(false, 5i32); return fs[0i32](3i32) & 63i32; }`},
	{"returned_iife_of_arrays_passthrough", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(c: boolean, p1: i32): ((i32) => i32)[] { let t: boolean = c; let s: boolean = !c; return (if (t) { [((x: i32) => x)] } else { [pick(s, ((y: i32) => (y + p1)), ((z: i32) => 9i32)), ((w: i32) => 7i32)] }); }
function main(): i32 { let fs: ((i32) => i32)[] = gen(false, 5i32); return (fs[0i32](3i32) + fs[1i32](0i32)) & 63i32; }`},

	{"iife_capture_bound_from_passthrough", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(p0: i32): i32 { let v0: (i32) => i32 = pick(true, ((a: i32) => 45i32), ((b: i32) => 46i32)); let v1: ((i32) => i32)[] = [(if (false) { v0 } else { pick(false, v0, ((x: i32) => (x + p0))) })]; return v1[0i32](1i32); }
function main(): i32 { return gen(3i32) & 63i32; }`},

	{"arm_array_payload_capture_scope", `function mk(r: Result[i32, i32]): ((i32) => i32)[] { return (match (r) { Ok(a) => [((x: i32) => (x + a))], Err(b) => (match (r) { Ok(c) => [((y: i32) => y)], Err(d) => [((z: i32) => (z + d))] }) }); }
function main(): i32 { let fs: ((i32) => i32)[] = mk(Err(4i32)); return fs[0i32](3i32) & 63i32; }`},
	{"arm_array_passthrough_forwards_literal", `enum Status { Active, Inactive, Pending }
function id[T](x: T): T { return x; }
function main(): i32 { let v1: Status = Pending; let v3: ((i32) => i32)[] = (match (v1) { Active => [((a: i32) => (match (v1) { Active => a, Inactive => a, Pending => 673i32 }))], Inactive => [((b: i32) => b)], Pending => id([((c: i32) => 126i32)]) }); return v3[0i32](3i32) & 63i32; }`},

	{"arm_array_nested_passthrough_forwards_literal", `function id[T](x: T): T { return x; }
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
enum S { A, B }
function main(): i32 { let p: i32 = 5i32; let v: S = B; let fs: ((i32) => i32)[] = (match (v) { A => [((x: i32) => (x + p))], B => id(pick(true, [((y: i32) => (y * 2i32))], [((z: i32) => 9i32)])) }); return fs[0i32](3i32) & 63i32; }`},

	{"array_elem_single_passthrough_control", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 { let n: i32 = 4i32; let t: boolean = true; let xs: ((i32) => i32)[] = [pick(t, ((x: i32) => (x + n)), ((z: i32) => 9i32))]; return xs[0i32](1i32) & 63i32; }`},
}

func TestSelfHostNestedPassthroughFnValueX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range nestedPassthroughFnValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-no-pie", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			var rcmd *exec.Cmd
			if len(runner) == 0 {
				rcmd = exec.Command(binPath)
			} else {
				rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
			}
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostNestedPassthroughFnValueWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping nested-passthrough fn-value wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range nestedPassthroughFnValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
