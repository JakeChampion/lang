package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostModloadX86_64 exercises the import-driven self-host driver
// (asm_modload_run.fern): instead of the `///MODULE`-marker stdin bundle
// and the synthetic builtin-type table, it reads an ENTRY file off argv,
// follows its `import "./x"` graph to sibling files on disk, and loads the
// built-in TYPES from a real builtins.fern module.
//
// Three program trees prove the contract:
//   - "real-builtins": a 2-module program using the built-in IoError enum
//     (declared nowhere in the program) — real multi-file loading + the
//     vendored builtins module.
//   - "custom-builtins": a builtins.fern declaring an enum (Color) that
//     the synthetic injection has NEVER heard of, used bare in the
//     program. It can only compile if the driver actually read+merged
//     builtins.fern — the decisive proof that real modules supply the
//     built-in types.
//   - "no-builtins": the same IoError program with NO builtins.fern in the
//     tree, confirming module_with_builtins' idempotent injection still
//     fills the types in (the legacy path is untouched).
func TestSelfHostModloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// Build the driver as an x86 host binary via the native toolchain.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	builtinsSrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}

	helperSrc := "pub function add(x: i32, y: i32): i32 { return x + y; }\n"

	// A 2-module program: main imports ./helper and uses the built-in
	// IoError enum (declared nowhere locally). classify(NotFound) = 1,
	// add(1, 41) = 42.
	ioErrMain := "" +
		"import \"./helper\";\n" +
		"function classify(e: IoError): i32 {\n" +
		"    match (e) {\n" +
		"        NotFound(m) => { return 1; },\n" +
		"        Other(m) => { return 2; },\n" +
		"        _ => { return 0; },\n" +
		"    }\n" +
		"}\n" +
		"function main(): i32 {\n" +
		"    var e: IoError = NotFound(\"x\");\n" +
		"    return helper.add(classify(e), 41);\n" +
		"}\n"

	cases := []struct {
		name     string
		files    map[string]string
		entryRel string // entry file relative to the program dir (default main.fern)
		wantExit int
	}{
		{
			name: "real-builtins",
			files: map[string]string{
				"helper.fern":   helperSrc,
				"builtins.fern": string(builtinsSrc),
				"main.fern":     ioErrMain,
			},
			wantExit: 42,
		},
		{
			name: "custom-builtins",
			files: map[string]string{
				// An enum the synthetic injection has never heard of — so
				// the program only compiles if builtins.fern was loaded.
				"builtins.fern": "enum Color { Red, Green, Blue }\n",
				"main.fern": "" +
					"function main(): i32 {\n" +
					"    var c: Color = Green;\n" +
					"    match (c) {\n" +
					"        Red => { return 10; },\n" +
					"        Green => { return 42; },\n" +
					"        Blue => { return 30; },\n" +
					"    }\n" +
					"    return 0;\n" +
					"}\n",
			},
			wantExit: 42,
		},
		{
			name: "no-builtins",
			files: map[string]string{
				// No builtins.fern: module_with_builtins' idempotent
				// injection must still supply IoError.
				"helper.fern": helperSrc,
				"main.fern":   ioErrMain,
			},
			wantExit: 42,
		},
		{
			name: "std-subpath",
			files: map[string]string{
				// A `std/`-prefixed import resolves to its sub-directory
				// file (<dir>/std/mathx.fern) while the namespace stays the
				// basename (mathx.triple → mathx__triple). triple(14) = 42.
				"builtins.fern":  string(builtinsSrc),
				"std/mathx.fern": "pub function triple(x: i32): i32 { return x * 3; }\n",
				"main.fern": "" +
					"import \"std/mathx\";\n" +
					"function main(): i32 { return mathx.triple(14); }\n",
			},
			wantExit: 42,
		},
		{
			// Manifest path dep: a fern.toml declares `dbl = { path =
			// "../dbl" }`, so a bare `import "dbl"` resolves — via the new
			// fern_toml reader in modloader — to ../dbl/lib.fern (the dep's
			// lib module, its own manifest setting lib=api.fern). dbl(21)=42.
			// Files are written into a `prog/` subdir so the ../dbl path is
			// meaningful; the driver entry is prog/main.fern.
			name: "manifest-path-dep",
			files: map[string]string{
				"builtins.fern":  string(builtinsSrc),
				"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\ndbl = { path = \"../dbl\" }\n",
				"app/main.fern": "import \"dbl\";\nfunction main(): i32 { return dbl.dbl(21); }\n",
				"dbl/fern.toml":  "[package]\nname = \"dbl\"\nlib = \"api.fern\"\n",
				"dbl/api.fern":   "pub function dbl(x: i32): i32 { return x * 2; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			for name, src := range tc.files {
				dst := filepath.Join(progDir, name)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatalf("mkdir for %s: %v", name, err)
				}
				if err := os.WriteFile(dst, []byte(src), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			entryRel := tc.entryRel
			if entryRel == "" {
				entryRel = "main.fern"
			}
			entry := filepath.Join(progDir, filepath.FromSlash(entryRel))
			progAsm := runDriverFile(t, runner, driverBin, entry)
			if len(progAsm) == 0 {
				t.Fatalf("driver emitted 0 bytes for %s", tc.name)
			}
			progBin := buildBin(t, gcc, progDir, "prog", string(progAsm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_, _ = cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: program exited %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}

// TestSelfHostModloadIRProbeX86_64 exercises the import-driven driver's
// `-ir-probe` flag (asm_modload_run.fern), which reports the IR-eligibility
// frontier (asm_ir.eligibility_report) AFTER real import resolution + bundling.
//
// This is the import-AWARE counterpart to asm_ir_run's stdin probe
// (TestSelfHostIREligibilityProbe). The stdin probe parses one source with no
// modload, so a function that calls an IMPORTED helper is measured with its
// callee ABSENT — calls_only_known sees an unknown call and reports an
// artificial `BAIL call` even though the function lowers cleanly once the
// import is present. The modload probe loads the `import` graph off disk first,
// so `f` (which calls `helper.dbl`, mangled `helper__dbl`) is measured with the
// real callee in the module and shows `ir`. The decisive assertion is exactly
// that cross-module flip: a call to an imported function does NOT bail here.
func TestSelfHostModloadIRProbeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	progDir := t.TempDir()
	files := map[string]string{
		"helper.fern": "pub function dbl(n: i32): i32 { return n * 2; }\n",
		// `f` calls the IMPORTED helper.dbl. Without modload (the stdin probe)
		// helper__dbl is absent, so f bails `call`; with modload it is present,
		// so f lowers (`f: ir`).
		"main.fern": "" +
			"import \"./helper\";\n" +
			"function f(n: i32): i32 { return helper.dbl(n) + 1; }\n" +
			"function main(): i32 { return f(20); }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(progDir, "main.fern")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, entry, "-ir-probe")
	} else {
		args := append(append([]string{}, runner[1:]...), driverBin, entry, "-ir-probe")
		cmd = exec.Command(runner[0], args...)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run -ir-probe driver: %v", err)
	}
	report := string(out)
	for _, want := range []string{"f: ir", "main: ir", "module: IR"} {
		if !strings.Contains(report, want) {
			t.Errorf("import-aware probe report missing %q\n--- report ---\n%s", want, report)
		}
	}
}
