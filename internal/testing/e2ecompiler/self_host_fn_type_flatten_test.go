package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var fnTypeCrossModuleCases = []struct {
	name string
	lib  string
	main string
	exit int
}{
	{
		name: "xmod-option-zeroarg-payload",
		lib:  "pub function three(): i32 { return 3; }",
		main: "import \"lib\";\nfunction main(): i32 {\n    let o: Option[() => i32] = Some(lib.three);\n    match (o) { Some(f) => { return f() + 4; }, None => { return 0; } }\n    return 9;\n}",
		exit: 7,
	},
	{
		name: "xmod-fn-type-struct-positions",
		lib:  "pub struct P { a: i32 }\npub function mk(): P { return P { a: 3 }; }\npub function take(p: P): i32 { return p.a * 2; }",
		main: "import \"lib\";\nfunction main(): i32 {\n    let f: () => lib.P = lib.mk;\n    let g: (lib.P) => i32 = lib.take;\n    return g(f());\n}",
		exit: 6,
	},
	{
		name: "xmod-fn-type-own-param",
		lib:  "struct A { n: i32 }\nfunction apply(f: (own A[]) => A[]): i32 { return f([A { n: 1 }]).len() + 6; }\nfunction keep(own acc: A[]): A[] { return acc.append(A { n: 2 }); }\npub function count(): i32 { return apply(keep); }",
		main: "import \"lib\";\nfunction main(): i32 {\n    return lib.count();\n}",
		exit: 8,
	},
	{
		name: "xmod-result-err-zeroarg",
		lib:  "pub function five(): i32 { return 5; }",
		main: "import \"lib\";\nfunction main(): i32 {\n    let r: Result[string, () => i32] = Err(lib.five);\n    match (r) { Ok(s) => { return 1; }, Err(f) => { return f(); } }\n    return 9;\n}",
		exit: 5,
	},
}

func TestSelfHostFnTypeFlattenX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range tupleFnZeroArgCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.src)
			got := compileLinkRunX86_64(t, gcc, runner, fernBin, stdlibRoot, proj, "main.fern")
			if got != tc.exit {
				t.Errorf("%s = %d, want %d — the flattened annotation must still name a fn type", tc.name, got, tc.exit)
			}
		})
	}

	for _, tc := range fnTypeCrossModuleCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "lib.fern"), tc.lib)
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.main)
			got := compileLinkRunX86_64(t, gcc, runner, fernBin, stdlibRoot, proj, "main.fern")
			if got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

func TestSelfHostFnTypeFlattenWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the fn-type flatten wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range tupleFnZeroArgCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.src)
			if got := compileRunWasm(t, runner, fernBin, stdlibRoot, proj, "main.fern"); got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}

	for _, tc := range fnTypeCrossModuleCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "lib.fern"), tc.lib)
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.main)
			if got := compileRunWasm(t, runner, fernBin, stdlibRoot, proj, "main.fern"); got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

func mustWriteFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func compileLinkRunX86_64(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, proj, entry string) int {
	t.Helper()
	asmPath := filepath.Join(proj, "out.s")
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, filepath.Join(proj, entry), stdlibRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v (%s)", err, out)
	}
	binPath := filepath.Join(proj, "out.bin")
	if out, err := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); err != nil {
		t.Fatalf("link: %v (%s)", err, out)
	}
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(binPath)
	} else {
		run = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_ = run.Run()
	return run.ProcessState.ExitCode()
}

func compileRunWasm(t *testing.T, runner []string, fernBin, stdlibRoot, proj, entry string) int {
	t.Helper()
	watPath := filepath.Join(proj, "out.wat")
	var stderr strings.Builder
	cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", watPath, filepath.Join(proj, entry), stdlibRoot)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile: %v (%s)", err, stderr.String())
	}
	run := exec.Command("wasmtime", "run", watPath)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally")
	}
	return run.ProcessState.ExitCode()
}
