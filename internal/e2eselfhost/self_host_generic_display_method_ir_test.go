package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// genericDisplayMethodIRCases pin the fix for #3893: a generic body that
// calls a trait METHOD returning a `string` on a type-parameter element —
// `xs[i].to_string()` in a `[T: Display]` join — must monomorphise per
// element type so the call dispatches the concrete type's `.to_string()`
// (string identity, a struct field read), NOT the i32 runtime helper.
//
// The x86-64 IR driver already monomorphised via module_with_builtins; the
// wasm IR driver skipped that prep, so `.to_string()` on a string element
// defaulted to `__fern_i32_to_str` and returned a corrupt string. The fix
// monomorphises on the wasm IR path too. These cases cover both element
// types (string identity + a `Tag` struct field) and both backends; each
// returns a small deterministic int (<= 125, wasm exit-code safe),
// oracle-checked against the interpreter.
const genericDisplayMethodIRPrelude = `trait Display { function to_string(self: Self): string; }
impl Display for string { function to_string(self: Self): string { return self; } }
struct Tag { name: string }
impl Display for Tag { function to_string(self: Self): string { return self.name; } }
pub function join_d[T: Display](xs: T[], sep: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < xs.len()) {
        if (i > 0) { out = out + sep; }
        out = out + xs[i].to_string();
        i = i + 1;
    }
    return out;
}
`

var genericDisplayMethodIRCases = []struct {
	name string
	main string
	want int
}{
	// string identity join: "ab-cd-ef" -> 8.
	{"string-join", `var ss: string[] = ["ab", "cd", "ef"]; return join_d(ss, "-").len();`, 8},
	// struct (field) join: "xyz,w" -> 5.
	{"struct-join", `var ts: Tag[] = [Tag { name: "xyz" }, Tag { name: "w" }]; return join_d(ts, ",").len();`, 5},
	// both element types -> two clones -> 8 + 5 = 13.
	{"two-types", `var ss: string[] = ["ab", "cd", "ef"]; var ts: Tag[] = [Tag { name: "xyz" }, Tag { name: "w" }]; return join_d(ss, "-").len() + join_d(ts, ",").len();`, 13},
	// empty separator (the regression that returned doubled length pre-fix).
	{"empty-sep", `var ss: string[] = ["a", "b", "c"]; return join_d(ss, "").len();`, 3},
}

func genericDisplayMethodIRSrc(mainBody string) string {
	return genericDisplayMethodIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostGenericDisplayMethodIRX86_64 pins the x86-64 IR path (already
// correct pre-fix) so the differential oracle stays honest.
func TestSelfHostGenericDisplayMethodIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range genericDisplayMethodIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(genericDisplayMethodIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostGenericDisplayMethodIRWasm is the actual #3893 regression
// guard — these miscompiled on the wasm IR path before the monomorphise fix.
func TestSelfHostGenericDisplayMethodIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-display-method wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genericDisplayMethodIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(genericDisplayMethodIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "gdm_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("generic-display-method wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
