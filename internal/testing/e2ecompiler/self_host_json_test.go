package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var jsonCases = []struct {
	name string
	main string
	exit int
}{
	{"encode-number", `let v: json.JsonValue = JNumber("42"); return json.json_encode(v).len();`, 2},
	{"encode-string", `let v: json.JsonValue = JString("hi"); return json.json_encode(v).len();`, 4},
	{"parse-object-ok", `match (json.parse("{\"a\":1}")) { Ok(v) => { return 7; }, Err(_) => { return 0; } }`, 7},
	{"parse-bad", `match (json.parse("{bad")) { Ok(v) => { return 1; }, Err(_) => { return 9; } }`, 9},
	{"get-i32", `match (json.parse("{\"n\":42}")) { Ok(v) => { match (json.json_get_i32(v, "n")) { Some(x) => { return x; }, None => { return 0; } } }, Err(_) => { return 0; } } return 0;`, 42},
	{"parse-array", `match (json.parse("[1,2,3]")) { Ok(v) => { return 7; }, Err(_) => { return 0; } }`, 7},
	{"nested-object", `match (json.parse("{\"x\":{\"y\":9}}")) { Ok(v) => { match (json.json_get(v, "x")) { Some(inner) => { match (json.json_get_i32(inner, "y")) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }, Err(_) => { return 0; } } return 0;`, 9},
	{"get-f64-exp", `match (json.parse("{\"x\": 2.5e1}")) { Ok(v) => { match (json.json_get_f64(v, "x")) { Some(x) => { if (x > 24.999 && x < 25.001) { return 25; } return 1; }, None => { return 2; } } }, Err(_) => { return 3; } } return 4;`, 25},
	{"get-f64-neg-frac", `match (json.parse("{\"x\": -0.5}")) { Ok(v) => { match (json.json_get_f64(v, "x")) { Some(x) => { if (x > 0.0 - 0.501 && x < 0.0 - 0.499) { return 6; } return 1; }, None => { return 2; } } }, Err(_) => { return 3; } } return 4;`, 6},
}

func jsonProgram(mainBody string) string {
	return "import \"std/json\";\nfunction main(): i32 { " + mainBody + " }\n"
}

func writeJsonLoaderProject(t *testing.T, gcc string) (dir, mmc, stdlibRoot string) {
	t.Helper()
	dir = writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc = buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return dir, mmc, stdlibRoot
}

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
