package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type f32ElemTagCase struct {
	name string
	src  string
	want int
}

func f32ElemTagCases() []f32ElemTagCase {
	return []f32ElemTagCase{
		{
			name: "tuple_elem_f32_renders_at_f32",
			src: `import "std/f64";
function main(): i32 {
  let t = (3.14159 as f32 * 2.5 as f32, 1);
  return t.0.to_string().len();
}`,
			want: 9,
		},
		{
			name: "inferred_option_f32_payload",
			src: `import "std/f64";
function main(): i32 {
  let o = Some(3.14159 as f32 * 2.5 as f32);
  match (o) { Some(v) => { return v.to_string().len(); }, None => {} }
  return 0;
}`,
			want: 9,
		},
		{
			name: "annotated_option_f32_payload",
			src: `import "std/f64";
function main(): i32 {
  let o: Option[f32] = Some(3.14159 as f32 * 2.5 as f32);
  match (o) { Some(v) => { return v.to_string().len(); }, None => {} }
  return 0;
}`,
			want: 9,
		},
		{
			name: "tuple_mixes_f32_and_f64_elems",
			src: `import "std/f64";
function main(): i32 {
  let t = (3.14159 as f32 * 2.5 as f32, 3.14159 as f64 * 2.5 as f64, 3);
  return t.0.to_string().len() * 10 + t.1.to_string().len();
}`,
			want: 98,
		},
		{
			name: "plain_f32_local_unchanged",
			src: `import "std/f64";
function main(): i32 {
  let v = 3.14159 as f32 * 2.5 as f32;
  return v.to_string().len();
}`,
			want: 9,
		},
	}
}

func f32ElemTagDriver(t *testing.T) (gcc string, dir string, fernBin string, stdlibRoot string) {
	t.Helper()
	gcc, _ = x86_64Tooling(t)
	dir = writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return gcc, dir, fernBin, root
}

func f32ElemTagEmit(t *testing.T, fernBin, dir, stdlibRoot, name, src, target string) string {
	t.Helper()
	srcPath := filepath.Join(dir, name+".fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	_, runner := x86_64Tooling(t)
	cmd := runX86_64Bin(runner, fernBin, "-target", target, "-emit", "asm", srcPath, stdlibRoot)
	out, _ := cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("%s: self-host emit for %s exited %d", name, target, code)
	}
	if len(out) == 0 {
		t.Fatalf("%s: self-host compiler emitted 0 bytes for %s", name, target)
	}
	return string(out)
}

func TestSelfHostF32ElemTagX86_64(t *testing.T) {
	gcc, dir, fernBin, stdlibRoot := f32ElemTagDriver(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range f32ElemTagCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := f32ElemTagEmit(t, fernBin, dir, stdlibRoot, tc.name, tc.src, "x86-64-linux")
			progBin := buildBin(t, gcc, dir, "f32elemtag_"+tc.name, asm)
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if exit := cmd.ProcessState.ExitCode(); exit != tc.want {
				t.Errorf("%s rendered length = %d, want %d — a LONGER value is the f32 "+
					"element tagged \"f64\" and dispatching f64.to_string (17 chars where "+
					"f32 renders 9)", tc.name, exit, tc.want)
			}
		})
	}
}

func TestSelfHostF32ElemTagWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping f32 element-tag wasm e2e")
	}
	_, dir, fernBin, stdlibRoot := f32ElemTagDriver(t)

	for _, tc := range f32ElemTagCases() {
		t.Run(tc.name, func(t *testing.T) {
			wat := f32ElemTagEmit(t, fernBin, dir, stdlibRoot, tc.name+"_w", tc.src, "wasm32-wasi")
			watPath := filepath.Join(dir, "f32elemtag_"+tc.name+".wat")
			if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("f32 element-tag wasm %q = %d, want %d (a wasm VALIDATION "+
					"failure reports 1: the module was refused, which is what a wrong "+
					"element width looks like here)\n%s", tc.name, got, tc.want, out)
			}
		})
	}
}

func TestSelfHostF32ElemTagArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, dir, fernBin, stdlibRoot := f32ElemTagDriver(t)

	for _, tc := range f32ElemTagCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := f32ElemTagEmit(t, fernBin, dir, stdlibRoot, tc.name+"_a", tc.src, "arm64-linux")
			progBin := buildBinArm64(t, arm64gcc, dir, "f32elemtag_"+tc.name+"_arm64", asm)
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("f32 element-tag arm64 %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
