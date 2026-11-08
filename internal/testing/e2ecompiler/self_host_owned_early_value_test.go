package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ownedEarlyValueDecls = `struct State { values:i32[] }
function take(own value:State):i32 {return value.values.len();}
`

func ownedEarlyValueCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct {
		name, params, result, body string
		reject                     bool
	}{
		{"returned-if", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};if(early) {return if(flag) {take(value)} else {take(value)};} return take(value);`, false},
		{"returned-match", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};if(early) {return match(flag) {true=>take(value),false=>take(value)};} return take(value);`, false},
		{"returned-in-loop", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};for i in [0,1] {if(early) {return if(flag) {take(value)} else {take(value)};}} return take(value);`, false},
		{"later-in-return", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};if(early) {return (if(flag) {take(value)} else {0})+value.values.len();} return take(value);`, true},
		{"duplicate", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};if(early) {return if(flag) {take(value)+take(value)} else {0};} return take(value);`, true},
		{"fallthrough", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};let n=if(flag) {take(value)} else {0};return n+value.values.len();`, true},
		{"loop-backedge", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};for i in [0,1] {let n=if(flag) {take(value)} else {0};} return take(value);`, true},
		{"capture", "early:boolean,flag:boolean", "i32", `let value=State {values:[1]};let later=()=>value.values.len();if(early) {return if(flag) {take(value)} else {0};}return later();`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := ownedEarlyValueDecls + "function work(" + tc.params + "): " + tc.result + " { " + tc.body + " } function main(): i32 { return 0; }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if !tc.reject && len(ds) == 0 {
					return
				}
				if tc.reject {
					for _, d := range ds {
						if d.code == "E050" || d.code == "E051" {
							return
						}
					}
				}
				t.Fatalf("%s: reject=%v, diagnostics=%v", label, tc.reject, ds)
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

func TestSelfHostOwnedEarlyValueX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_early_value_checker")
	ownedEarlyValueCases(t, bin, runner, dir)
}

func TestSelfHostOwnedEarlyValueWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_early_value_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedEarlyValueCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostOwnedEarlyValueRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := ownedEarlyValueDecls + `
function work(early:boolean,flag:boolean):i32 {
 let value=State {values:[args().len(),2,3]};
 if(early) {return if(flag) {take(value)} else {take(value)};}
 return take(value);
}
function main():i32 {return if(work(true,true)==3 && work(true,false)==3 && work(false,true)==3) {0} else {1};}
`

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
