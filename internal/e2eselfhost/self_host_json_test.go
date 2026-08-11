package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// jsonCases are `import "std/json"` programs compiled through the
// multi-module bundling loader (asm_load_run.fern), checked by exit
// code. Exercises json_parse (objects, arrays, nesting), json_encode,
// and the typed extractors — including json_get_f64, which since
// #5420 routes through std/string's parse_float receiver method, the
// cross-module method-reference shape the old standalone (no-loader)
// driver could never link. Exit codes cross-checked vs the Go backend.
var jsonCases = []struct {
	name string
	main string
	exit int
}{
	{"encode-number", `var v: JsonValue = JNumber("42"); return json.json_encode(v).len();`, 2},
	{"encode-string", `var v: JsonValue = JString("hi"); return json.json_encode(v).len();`, 4},
	{"parse-object-ok", `match (json.json_parse("{\"a\":1}")) { Some(v) => { return 7; }, None => { return 0; } }`, 7},
	{"parse-bad", `match (json.json_parse("{bad")) { Some(v) => { return 1; }, None => { return 9; } }`, 9},
	{"get-i32", `match (json.json_parse("{\"n\":42}")) { Some(v) => { match (json.json_get_i32(v, "n")) { Some(x) => { return x; }, None => { return 0; } } }, None => { return 0; } } return 0;`, 42},
	{"parse-array", `match (json.json_parse("[1,2,3]")) { Some(v) => { return 7; }, None => { return 0; } }`, 7},
	{"nested-object", `match (json.json_parse("{\"x\":{\"y\":9}}")) { Some(v) => { match (json.json_get(v, "x")) { Some(inner) => { match (json.json_get_i32(inner, "y")) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }, None => { return 0; } } return 0;`, 9},
	{"get-f64-exp", `match (json.json_parse("{\"x\": 2.5e1}")) { Some(v) => { match (json.json_get_f64(v, "x")) { Some(x) => { if (x > 24.999 && x < 25.001) { return 25; } return 1; }, None => { return 2; } } }, None => { return 3; } } return 4;`, 25},
	{"get-f64-neg-frac", `match (json.json_parse("{\"x\": -0.5}")) { Some(v) => { match (json.json_get_f64(v, "x")) { Some(x) => { if (x > 0.0 - 0.501 && x < 0.0 - 0.499) { return 6; } return 1; }, None => { return 2; } } }, None => { return 3; } } return 4;`, 6},
}

// jsonProgram wraps a main body into a loader-shaped program: the
// entry imports std/json and calls it qualified, the way modload
// routes stdlib access. JsonValue/JNumber stay bare — the builtin
// enum is auto-injected on every pipeline.
func jsonProgram(mainBody string) string {
	return "import \"std/json\";\nfunction main(): i32 { " + mainBody + " }\n"
}

// writeJsonLoaderProject assembles the self-host bundling-loader
// project (base asm project + the loader's extra modules) and builds
// the mmc driver.
func writeJsonLoaderProject(t *testing.T, gcc string) (dir, mmc, stdlibRoot string) {
	t.Helper()
	dir = writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc = buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return dir, mmc, stdlibRoot
}

// TestSelfHostJsonX86_64 compiles the jsonCases programs with the
// self-hosted x86-64 compiler through the bundling loader and checks
// exit codes.
func TestSelfHostJsonX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmc, stdlibRoot := writeJsonLoaderProject(t, gcc)

	for _, tc := range jsonCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(jsonProgram(tc.main)), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v (%d bytes)", err, len(asm))
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

// TestSelfHostJsonArm64 — CI-gated arm64 counterpart: same loader
// driver (an x86 host binary) with `-target arm64-linux`, output assembled
// with the arm64 toolchain and run under qemu.
func TestSelfHostJsonArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmc, stdlibRoot := writeJsonLoaderProject(t, x86gcc)

	for _, tc := range jsonCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(jsonProgram(tc.main)), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, err := exec.Command(mmc, mainPath, stdlibRoot, "-target", "arm64-linux").Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v (%d bytes)", err, len(asm))
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
