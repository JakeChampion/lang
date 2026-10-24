package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const boolToStringProg = `
function flag(n: i32): boolean {
    return n > 2;
}

function main(): i32 {
    let b = true;
    print(f"<{b}>");
    print(f"{flag(1)}|{flag(3)}");
    print(false);
    let s: string = (1 == 1).to_string();
    print(s);
    return 0;
}
`

const boolToStringWant = "<true>\nfalse|true\nfalse\ntrue\n"

const boolToStringDeclaredProg = `
function (b: boolean) to_string(): string {
    if (b) {
        return "yes";
    }
    return "no";
}

function main(): i32 {
    print(f"{true}/{false}");
    print(false);
    return 0;
}
`

const boolToStringDeclaredWant = "yes/no\nno\n"

var boolToStringCases = []struct{ name, src, want string }{
	{"builtin", boolToStringProg, boolToStringWant},
	{"declared", boolToStringDeclaredProg, boolToStringDeclaredWant},
}

func TestBoolToStringInterp(t *testing.T) {
	for _, tc := range boolToStringCases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := e2eharness.RunInterp(t, tc.src)
			if code != 0 || out != tc.want {
				t.Fatalf("interp exit %d, stdout %q; want 0, %q", code, out, tc.want)
			}
		})
	}
}

func TestBoolToStringSelfHostInterp(t *testing.T) {
	for _, tc := range boolToStringCases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runSelfHostInterp(t, tc.src)
			if code != 0 || out != tc.want {
				t.Fatalf("self-host interp exit %d, stdout %q; want 0, %q", code, out, tc.want)
			}
		})
	}
}

func TestBoolToStringX86_64(t *testing.T) {
	for _, tc := range boolToStringCases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := compileAndRunX86_64(t, tc.src)
			if code != 0 || out != tc.want {
				t.Fatalf("x86-64 exit %d, output %q; want 0, %q", code, out, tc.want)
			}
		})
	}
}

func TestBoolToStringArm64(t *testing.T) {
	for _, tc := range boolToStringCases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := compileAndRunArm64(t, tc.src)
			if code != 0 || out != tc.want {
				t.Fatalf("arm64 exit %d, output %q; want 0, %q", code, out, tc.want)
			}
		})
	}
}

func TestBoolToStringWasm(t *testing.T) {
	for _, tc := range boolToStringCases {
		t.Run(tc.name, func(t *testing.T) {
			core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, tc.src, nil)
			cmd := e2eharness.RunWasmCore(t, core)
			out, _ := cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 0 || string(out) != tc.want {
				t.Fatalf("wasm exit %d, stdout %q; want 0, %q", code, out, tc.want)
			}
		})
	}
}

func runSelfHostInterp(t *testing.T, src string) (string, int) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-interp", p, e2eharness.SelfHostStdlibRoot(t))
	out, err := cmd.Output()
	if cmd.ProcessState == nil {
		t.Fatalf("self-host interp did not run: %v", err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}
