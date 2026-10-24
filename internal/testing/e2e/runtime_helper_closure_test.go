package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var allRuntimeNeedRoots = []string{
	"alloc_u8", "args", "arr_own_elems", "arr_push", "arr_push_owned", "arr_slice",
	"arr_str_join", "eprint", "heap",
	"i32_to_string", "monotonic_ns", "now_ns", "now_unix_ms",
	"putchar", "random_bytes", "random_i32", "read_file",
	"read_file_bytes", "sleep_ms", "str_bytes", "str_case", "str_cmp",
	"str_concat", "str_eq", "str_from_bytes", "str_lines", "str_print",
	"str_read_line", "str_repeat", "str_replace",
	"str_grow", "str_own", "str_split", "str_trim",
}

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
