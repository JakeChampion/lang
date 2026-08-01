package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Runtime-helper symbol-closure check (issue #2649).
//
// The backend runtime helpers (`__fern_alloc`, `__fern_str_eq`,
// `__fern_str_split`, …) are hand-written assembly whose inter-dependencies
// are tracked out-of-band: a helper body that `call`s another helper creates a
// link-time dependency that nothing in the compiler statically checks. Miss one
// and the result is an *undefined-symbol link failure* — historically a garbage
// exit code, not a clean error (e.g. `__fern_map_set` → `__fern_str_eq` not
// emitted for i32-only map programs).
//
// These tests assert the property the dependency machinery exists to guarantee:
// the emitted runtime is *symbol-closed* — every `call/bl __fern_*` resolves to
// a `__fern_*` defined in the same unit. We prove it the ground-truth way, by
// assembling+linking the emitted asm with `-nostdlib`: an unmet helper
// dependency surfaces as a linker "undefined reference", failing the test.
//
// Native (Go x86-64/arm64) backends propagate deps via their `recordUse` /
// post-scan logic; the self-hosted IR backend uses asmcore's declarative
// `runtime_need_deps` table + `close_needs` transitive closure. Both must keep
// the runtime closed.

// allRuntimeNeedRoots mirrors asm_ir.all_runtime_need_roots() — the closed set
// of runtime-need ROOT names the self-hosted codegen can mark. Keep in sync; a
// new `.need("x")` root added there should be added here so its helper's
// dependency closure is link-checked.
var allRuntimeNeedRoots = []string{
	"alloc_u8", "args", "arr_concat", "arr_i32_index_of", "arr_i32_min_max",
	"arr_i32_product", "arr_i32_sum", "arr_push", "arr_push_owned", "arr_reverse", "arr_slice",
	"arr_str_index_of", "arr_str_join", "chr", "eprint", "heap", "i32_pow",
	"i32_to_string", "maps", "monotonic_ns", "now_ns", "now_unix_ms",
	"print_int", "putchar", "random_bytes", "random_i32", "read_file",
	"read_int", "sleep_ms", "str_bytes", "str_case", "str_chars", "str_cmp",
	"str_concat", "str_eq", "str_from_bytes", "str_lines", "str_print",
	"str_read_line", "str_repeat", "str_replace", "str_reverse", "str_search",
	"str_split", "str_to_i32", "str_trim", "strbuf",
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

// nativeMatrix is a spread of programs that, between them, exercise runtime
// helper clusters with inter-helper dependencies the backend must keep closed:
// string concat (__fern_str_concat → __fern_alloc), string-keyed maps
// (__fern_map_set → __fern_str_eq + __fern_arr_push — the exact edge whose
// omission for i32-only maps motivated #2649), i32-keyed maps, and dynamic
// arrays (push/grow → __fern_alloc). Map ops require `import "core/map"`.
var nativeMatrix = map[string]string{
	"str_concat": `function main(): i32 { var a: string = "ab"; var b: string = a + a; return b.len(); }`,
	"map_str": `import "core/map";
function main(): i32 { var m: Map[string, i32] = map_new(4); m = m.insert("a", 1); m = m.insert("b", 2); return m.get_or("a", 0) + m.get_or("b", 0); }`,
	"map_i32": `import "core/map";
function main(): i32 { var m: Map[i32, i32] = map_new(4); m = m.insert(1, 10); m = m.insert(2, 20); return m.get_or(1, 0) + m.get_or(2, 0); }`,
	"array_grow": `function main(): i32 { var xs: i32[] = []; var i: i32 = 0; while (i < 8) { xs = xs.append(i); i = i + 1; } return xs.len(); }`,
}

func TestNativeRuntimeHelperClosureX86_64(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	for name, src := range nativeMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			srcPath := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			prog, _, err := modload.Load(srcPath)
			if err != nil {
				t.Fatalf("modload: %v", err)
			}
			if err := constfold.Fold(prog); err != nil {
				t.Fatalf("constfold: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			asm, err := x86_64.Emit(prog, info)
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			assertAsmLinks(t, gcc, dir, "nclos", asm)
		})
	}
}

func TestNativeRuntimeHelperClosureArm64(t *testing.T) {
	gcc, _ := arm64Tooling(t)
	for name, src := range nativeMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			srcPath := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			prog, _, err := modload.Load(srcPath)
			if err != nil {
				t.Fatalf("modload: %v", err)
			}
			if err := constfold.Fold(prog); err != nil {
				t.Fatalf("constfold: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			asm, err := arm64codegen.Emit(prog, info)
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			assertAsmLinks(t, gcc, dir, "nclos_arm64", asm)
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
	gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun_closure")

	runDriver := func(t *testing.T, prog string, args ...string) string {
		t.Helper()
		cmd := exec.Command(driverBin, args...)
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
