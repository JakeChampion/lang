package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Runtime-helper symbol-closure check (issue #2649).
//
// The backend runtime helpers (`__fern_alloc`, `__fern_str_eq`,
// `__fern_str_split`, …) are hand-written assembly whose inter-dependencies
// are tracked out-of-band: a helper body that `call`s another helper creates a
// link-time dependency that nothing in the compiler statically checks. Miss one
// and the result is an *undefined-symbol link failure* — historically a garbage
// exit code, not a clean error.
//
// These tests assert the property the dependency machinery exists to guarantee:
// the emitted runtime is *symbol-closed* — every `call/bl __fern_*` resolves to
// a `__fern_*` defined in the same unit. We prove it the ground-truth way, by
// assembling+linking the emitted asm with `-nostdlib`: an unmet helper
// dependency surfaces as a linker "undefined reference", failing the test.
//
// The compiler tracks them with asmcore's declarative `runtime_need_deps`
// table and `close_needs` transitive closure.

// allRuntimeNeedRoots mirrors asm_ir.all_runtime_need_roots() — the closed set
// of runtime-need ROOT names the self-hosted codegen can mark. Keep in sync; a
// new `.need("x")` root added there should be added here so its helper's
// dependency closure is link-checked.
var allRuntimeNeedRoots = []string{
	"alloc_u8", "args", "arr_own_elems", "arr_push", "arr_push_owned", "arr_slice",
	"arr_str_join", "eprint", "heap",
	"i32_to_string", "monotonic_ns", "now_ns", "now_unix_ms",
	"putchar", "random_bytes", "random_i32", "read_file",
	"read_file_bytes", "sleep_ms", "str_bytes", "str_case", "str_cmp",
	"str_concat", "str_eq", "str_from_bytes", "str_lines", "str_print",
	"str_read_line", "str_repeat", "str_replace",
	"str_grow", "str_own", "str_split", "str_trim", "strbuf",
}

// assertAsmLinks writes asm to <dir>/<name>.s and links it as a static,
// freestanding ELF. A missing runtime-helper dependency dangles as an
// "undefined reference" and fails the link — i.e. the asm is not symbol-closed.
func assertAsmLinks(t *testing.T, gcc, dir, name, asm string) {
	t.Helper()
	asmPath := filepath.Join(dir, name+".s")
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s: %v", asmPath, err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Errorf("runtime not symbol-closed for %q (missing helper dependency?):\n%v\n%s", name, err, out)
	}
}

// closureMatrix is a spread of programs that, between them, exercise runtime
// helper clusters with inter-helper dependencies the compiler must keep closed:
// string concat (__fern_str_concat → __fern_alloc), string- and i32-keyed
// maps (core/map's bodies over the raw-memory helpers), and dynamic arrays
// (push/grow → __fern_alloc). Each program's exit code is its answer,
// so a link that succeeds against the wrong helper is caught as well.
var closureMatrix = []struct {
	name string
	src  string
	want int
}{
	{"str_concat", `function main(): i32 { let a: string = "ab"; let b: string = a + a; return b.len(); }`, 4},
	{"map_str", `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(4); m = m.insert("a", 1); m = m.insert("b", 2); return m.get_or("a", 0) + m.get_or("b", 0); }`, 3},
	{"map_i32", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = map_new(4); m = m.insert(1, 10); m = m.insert(2, 20); return m.get_or(1, 0) + m.get_or(2, 0); }`, 30},
	{"array_grow", `function main(): i32 { let xs: i32[] = []; let i: i32 = 0; while (i < 8) { xs = xs.append(i); i = i + 1; } return xs.len(); }`, 8},
}

// The whole-program path: the compiler links in-process and refuses an
// undefined symbol, so a helper dependency it fails to emit is a compile
// failure here.
func TestRuntimeHelperClosureX86_64(t *testing.T) {
	for _, tc := range closureMatrix {
		t.Run(tc.name, func(t *testing.T) {
			if out, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("exit = %d, want %d\n%s", code, tc.want, out)
			}
		})
	}
}

func TestRuntimeHelperClosureArm64(t *testing.T) {
	for _, tc := range closureMatrix {
		t.Run(tc.name, func(t *testing.T) {
			if out, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("exit = %d, want %d\n%s", code, tc.want, out)
			}
		})
	}
}

// TestSelfHostIRRuntimeHelperClosure drives the self-hosted IR backend's entry
// unit with each runtime-need root forced via -ir-extra-need (modelling the
// cross-module need-aggregation path), then link-checks the emitted runtime.
// This validates asmcore's runtime_need_deps + close_needs transitive closure
// per helper: if a root's helper body calls another helper the closure misses,
// the link dangles. (str_lines → str_split and strbuf → heap are the edges this
// most directly guards.)
func TestSelfHostIRRuntimeHelperClosure(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "airun_closure")

	runDriver := func(t *testing.T, prog string, args ...string) string {
		t.Helper()
		cmd := runX86_64Bin(runner, driverBin, args...)
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver failed (args %v): %v", args, err)
		}
		return string(out)
	}

	const trivial = "function main(): i32 { return 0; }"
	for _, root := range allRuntimeNeedRoots {
		root := root
		t.Run(root, func(t *testing.T) {
			asm := runDriver(t, trivial, "-ir-unit", "entry", "-ir-extra-need", root)
			assertAsmLinks(t, gcc, dir, "irclos_"+root, asm)
		})
	}
}
