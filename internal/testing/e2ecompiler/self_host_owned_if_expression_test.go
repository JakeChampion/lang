package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ownedIfDecls = `struct Box { values: u8[] }
function take(own box: Box): Box { return box; }
function (own box: Box) step(): Box { return box; }
function both(own a: Box, own b: Box): Box { return a; }
`

func ownedIfExpressionCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct{ name, body, want string }{
		{"arguments", `let next = if (flag) { take(box) } else { take(box) }; return next;`, ""},
		{"receivers", `let next = if (flag) { box.step() } else { box.step() }; return next;`, ""},
		{"nested", `let next = if (flag) { if (other) { take(box) } else { take(box) } } else { take(box) }; return next;`, ""},
		{"replacement", `box = if (flag) { take(box) } else { take(box) }; return box;`, ""},
		{"condition-read", `let next = if (box.values[0] == 1u8) { take(box) } else { take(box) }; return next;`, ""},
		{"block", `let next = if (flag) { let next = take(box); next } else { take(box) }; return next;`, ""},
		{"later-read", `let next = if (flag) { take(box) } else { take(box) }; return box;`, "E051"},
		{"duplicate", `let next = if (flag) { both(box, box) } else { take(box) }; return next;`, "E051"},
		{"branch-read", `let next = if (flag) { let next = take(box); let n = box.values[0]; next } else { take(box) }; return next;`, "E051"},
		{"later-argument", `let next = both(if (flag) { take(box) } else { take(box) }, box); return next;`, "E051"},
		{"loop-reuse", `for i in 0..2 { let next = if (flag) { take(box) } else { take(box) }; } return Box { values: [] };`, "E051"},
		{"capture", `let view = () => box.values[0]; let next = if (flag) { take(box) } else { take(box) }; return next;`, "E051"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := ownedIfDecls + `function work(flag: boolean, other: boolean): Box { let box = Box { values: [1u8] }; ` + tc.body + ` } function main(): i32 { return 0; }`
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.want == "" && len(ds) == 0 {
					return
				}
				for _, d := range ds {
					if tc.want != "" && d.code == tc.want {
						return
					}
				}
				t.Fatalf("%s: want %q, got %v", label, tc.want, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostOwnedIfExpressionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_if_checker")
	ownedIfExpressionCases(t, bin, runner, dir)
}

func TestSelfHostOwnedIfExpressionWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_if_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedIfExpressionCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostOwnedIfExpressionRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := ownedIfDecls + `function work(flag: boolean): Box {
  let box = Box { values: [7u8] };
  let next = if (flag) { take(box) } else { take(box) };
  return next;
}
function main(): i32 {
  let a = work(true); let b = work(false);
  return a.values[0] as i32 + b.values[0] as i32 - 14;
}`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var command *exec.Cmd
			if target == "wasm32-wasi" {
				command = exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, path, "FERN_STRICT_IR=1"))
			} else {
				command = runX86_64Bin(cli.runner, cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1"))
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", target, err, output)
			}
		})
	}
}
