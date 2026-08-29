package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An inferred f32 element keeps its width in elem_type_tag (#7756) --------
//
// `elem_type_tag` tested `expr_is_f64` with no `expr_is_f32` arm ahead of it. An
// f32 value is also is_f64 — both ride the 8-byte slot — so an inferred f32
// element was tagged "f64" and lost the width its method dispatch keys on. The
// stored value was right; only the RENDERING was wrong:
//
//	var t = (3.14159 as f32 * 2.5 as f32, 1); print(t.0.to_string());
//	native   19.634937
//	selfhost 19.634937286376953
//
// These rows assert the rendered LENGTH as the exit code rather than capturing
// stdout, because the length is what separates the two precisions and an exit
// code is comparable on all three backends. f32 renders 9 characters here, f64
// renders 17 — so every row below returns 17 (or 178) on a compiler without the
// fix, which is what makes them non-vacuous. Verified against a pre-fix binary,
// not assumed.
//
// Every want was confirmed against BOTH oracles — `bin/fern -interp` and the
// native x86-64 backend — and on wasm, and every value stays under 126 because
// WASI refuses anything outside [0..126) and reports 1 instead (the phantom
// mismatch docs/LOCAL-DEV-LOOP.md records).
//
// THE CONFORMANCE CORPUS DOES NOT COVER THESE SHAPES. A before/after
// `scripts/selfhost-emit-hashes` sweep over all 1554 (fixture, target) pairs is
// byte-identical, which is why the bug survived: nothing in the corpus puts an
// f32 in a tuple element or an Option payload. These rows are the only coverage.
type f32ElemTagCase struct {
	name string
	src  string
	want int
}

func f32ElemTagCases() []f32ElemTagCase {
	return []f32ElemTagCase{
		{
			// THE REPRO, as a length: the tuple ELEMENT spelling. Was 17.
			name: "tuple_elem_f32_renders_at_f32",
			src: `import "std/float";
function main(): i32 {
  var t = (3.14159 as f32 * 2.5 as f32, 1);
  return t.0.to_string().len();
}`,
			want: 9,
		},
		{
			// An INFERRED Option payload (`var o = Some(<f32>)`). Its tag reaches
			// the payload admission list, which dropped an f32 outright rather than
			// widening it — losing the payload type, not just its width.
			name: "inferred_option_f32_payload",
			src: `import "std/float";
function main(): i32 {
  var o = Some(3.14159 as f32 * 2.5 as f32);
  match (o) { Some(v) => { return v.to_string().len(); }, None => {} }
  return 0;
}`,
			want: 9,
		},
		{
			// The ANNOTATED spelling of the same, which reaches the tag through
			// opt_elem_tag_from_ty rather than the construction walk.
			name: "annotated_option_f32_payload",
			src: `import "std/float";
function main(): i32 {
  var o: Option[f32] = Some(3.14159 as f32 * 2.5 as f32);
  match (o) { Some(v) => { return v.to_string().len(); }, None => {} }
  return 0;
}`,
			want: 9,
		},
		{
			// f32 and f64 elements in ONE tuple, so the row fails if the two
			// collapse in either direction: 9 characters for the f32 and 8 for the
			// f64, encoded as 9*10+8. A compiler that tags both "f64" returns 178.
			name: "tuple_mixes_f32_and_f64_elems",
			src: `import "std/float";
function main(): i32 {
  var t = (3.14159 as f32 * 2.5 as f32, 3.14159 as f64 * 2.5 as f64, 3);
  return t.0.to_string().len() * 10 + t.1.to_string().len();
}`,
			want: 98,
		},
		{
			// A plain f32 LOCAL, which never went through elem_type_tag and was
			// correct all along (expr_scalar_type has the f32-before-f64 arm this
			// change ports). Here so a regression on the working path fails too.
			name: "plain_f32_local_unchanged",
			src: `import "std/float";
function main(): i32 {
  var v = 3.14159 as f32 * 2.5 as f32;
  return v.to_string().len();
}`,
			want: 9,
		},
	}
}

// The three legs share one CLI-driver build: these programs `import
// "std/float"` for `to_string`, and the single-source `*_run.fern` shims parse
// raw source with no module loader, so the import never resolves there. The
// unified fern.fern driver takes a source path plus the stdlib root and
// resolves it for every target.
func f32ElemTagDriver(t *testing.T) (gcc string, dir string, fernBin string, stdlibRoot string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("f32 element-tag tests run only natively (the driver takes argv paths)")
	}
	dir = writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../internal/stdlib")
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
	cmd := exec.Command(fernBin, "-target", target, "-emit", "asm", srcPath, stdlibRoot)
	out, _ := cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("%s: self-host emit for %s exited %d", name, target, code)
	}
	if len(out) == 0 {
		t.Fatalf("%s: self-host compiler emitted 0 bytes for %s", name, target)
	}
	return string(out)
}

// TestSelfHostF32ElemTagX86_64 — the rendered length matches native's on the
// register backend.
func TestSelfHostF32ElemTagX86_64(t *testing.T) {
	gcc, dir, fernBin, stdlibRoot := f32ElemTagDriver(t)

	for _, tc := range f32ElemTagCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := f32ElemTagEmit(t, fernBin, dir, stdlibRoot, tc.name, tc.src, "x86-64-linux")
			progBin := buildBin(t, gcc, dir, "f32elemtag_"+tc.name, asm)
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if exit := cmd.ProcessState.ExitCode(); exit != tc.want {
				t.Errorf("%s rendered length = %d, want %d — a LONGER value is the f32 "+
					"element tagged \"f64\" and dispatching f64.to_string (17 chars where "+
					"f32 renders 9)", tc.name, exit, tc.want)
			}
		})
	}
}

// TestSelfHostF32ElemTagWasm — the wasm leg, and the one that catches the
// consumers this fix had to audit: three of the four read the tag by spelling
// "f64" literally, and a wrong width there is a VALIDATION failure on wasm where
// the register backends store 8 bytes regardless and never notice.
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

// TestSelfHostF32ElemTagArm64 — the arm64 sibling under qemu, emitted by the
// x86 host driver.
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
