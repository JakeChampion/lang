package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStdlibImportX86_64 exercises the import-driven loader's
// stdlib resolution: asm_load_run.fern, given a stdlib root as its
// second argument, resolves `std/…` / `core/…` imports under it
// (`<root>/std/foo.fern`) and loads them transitively — the same
// machinery it already used for local `./…` imports. This is the step
// that lets a self-host-compiled program actually pull in real stdlib
// modules.
//
// Native only: the driver reads module files by host path from argv, so
// a qemu runner couldn't resolve the same paths (mirrors the local
// file-loading test).
func TestSelfHostStdlibImportX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "treeshake.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	// The real repo stdlib (absolute path; the import "std/foo" resolves
	// to <root>/std/foo.fern).
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []struct {
		name string
		main string
		exit int
	}{
		// Leaf module (std/math has no imports of its own): range(0,10)
		// sums to 45; minus 3 = 42.
		{"std-math-leaf", "import \"std/math\";\nfunction main(): i32 {\n var r: i32[] = math.range(0, 10);\n var sum: i32 = 0;\n for x in r { sum = sum + x; }\n return sum - 3;\n}\n", 42},
		// Transitive: core/cmp imports std/sort imports std/string. min(1)+max(9)+32 = 42.
		{"std-sort-transitive", "import \"core/cmp\";\nfunction main(): i32 {\n var a: i32[] = [5, 3, 8, 1, 9, 2];\n var s: i32[] = cmp.sort(a);\n return s[0] + s[5] + 32;\n}\n", 42},
		// std/json imports core/map, whose open-addressing source pokes
		// raw memory through the low-level intrinsics (__alloc / __load_* /
		// __store_* / __ptr_width / __memset) and the RC intrinsics. Before
		// the self-host provided those symbols the bundle failed to link.
		// The program itself uses the native Map[K,V] runtime (10+32=42).
		{"std-json-intrinsics", "import \"std/json\";\nfunction main(): i32 {\n var m: Map[string,i32] = map_new(8);\n m = m.insert(\"a\", 10);\n m = m.insert(\"b\", 32);\n var r: i32 = 0;\n match (m.get(\"a\")) { Some(v) => { r = r + v; }, None => { } }\n match (m.get(\"b\")) { Some(v) => { r = r + v; }, None => { } }\n return r;\n}\n", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil {
				t.Fatalf("loader compile: %v", err)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
