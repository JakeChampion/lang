package e2ecompiler

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type builtinErrorAdviceCase struct{ name, source, message string }

func builtinErrorAdviceCases() []builtinErrorAdviceCase {
	var cases []builtinErrorAdviceCase
	for _, recv := range []string{"IoError", "StreamFault"} {
		for _, method := range []string{"message", "to_string", "mesage", "does_not_exist"} {
			message := fmt.Sprintf("no method %q on %s", method, recv)
			switch method {
			case "message", "to_string":
				message += " — add `import \"core/error\"`"
				if method == "to_string" {
					message += " (an f-string like `f\"{x}\"` desugars to `x.to_string()`)"
				}
			case "mesage":
				message += " — did you mean \"message\", which needs `import \"core/error\"`?"
			default:
				message += ", and no stdlib module declares one"
			}
			cases = append(cases, builtinErrorAdviceCase{recv + "/" + method,
				fmt.Sprintf("function probe(e: %s): string { return e.%s(); }\nfunction main(): i32 { return 0; }", recv, method), message})
		}
	}
	cases = append(cases, builtinErrorAdviceCase{"read-file",
		`function main(): i32 { match (read_file("/x")) { Ok(_) => {}, Err(e) => { print(e.message()); } } return 0; }`,
		"no method \"message\" on IoError — add `import \"core/error\"`"})
	return cases
}

func testBuiltinErrorImportAdvice(t *testing.T, bin string, runner []string) {
	t.Helper()
	for _, tc := range builtinErrorAdviceCases() {
		t.Run(tc.name, func(t *testing.T) {
			goDiags := goCheckerDiags(t, t.TempDir(), tc.source)
			if len(goDiags) != 1 || goDiags[0].code != "E043" || goDiags[0].msg != tc.message {
				t.Fatalf("Go diagnostics = %v, want E043 %q", goDiags, tc.message)
			}
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(tc.source)
			ds := driverDiags(runCheckerDriver(t, command, tc.name))
			if len(ds) != 1 || ds[0].code != "E043" || ds[0].msg != tc.message {
				t.Fatalf("self-host diagnostics = %v, want E043 %q", ds, tc.message)
			}
		})
	}
}

func TestSelfHostBuiltinErrorImportAdviceX86_64(t *testing.T) {
	testBuiltinErrorImportAdvice(t, hostDriverBin(t, "drivers/checker_codes_run.fern"), nil)
}

func TestSelfHostBuiltinErrorImportAdviceWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatal("wasmtime is required for import advice controls")
	}
	bin := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_codes_run.fern", e2eharness.TargetWasm32Wasi)
	testBuiltinErrorImportAdvice(t, bin, []string{wasmtime, "run"})
}

const builtinErrorImportRuntime = `function main(): i32 {
 let names = args();
 let e: IoError = Other("read", names[0], 5);
 if (e.message() != names[0] || e.to_string() != names[0]) { return 1; }
 let interrupted: IoError = Interrupted;
 if (interrupted.message() != "interrupted" || interrupted.to_string() != "interrupted") { return 2; }
 let fault: StreamFault = StreamTimeout;
 if (fault.message() != "the body arrived too slowly" || fault.to_string() != "the body arrived too slowly") { return 3; }
 return 0;
}
`

func TestSelfHostBuiltinErrorImportRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, imported := range []string{`import "core/error";`, `import "core/error.fern" as faults;`, `import "core/cmp";`, `import "std/io";`} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(imported+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, imported+"\n"+builtinErrorImportRuntime, target, "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
