package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ownedValueMatchDecls = `struct State { values: i32[] }
struct Wrapper { state: State, selected: Option[i32] }
function consume(own state: State): i32 { return state.values.len(); }
function guard(own state: State): boolean { return state.values.len() > 0; }
`

func ownedValueMatchCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct {
		name, params, result, body string
		reject                     bool
	}{
		{"fresh-local", "tag: i32", "i32", `let state = State { values: [1] }; return match (tag) { 0 => consume(state), _ => consume(state) };`, false},
		{"nested-if", "tag: i32", "i32", `let state = State { values: [1] }; return match (tag) { 0 => consume(state), _ => if (tag > 1) { consume(state) } else { consume(state) } };`, false},
		{"nested-match", "tag: i32", "i32", `let state = State { values: [1] }; return match (tag) { 0 => consume(state), _ => match (tag) { 1 => consume(state), _ => consume(state) } };`, false},
		{"destructured", "own wrapper: Wrapper", "i32", `let Wrapper { state, selected } = wrapper; return match (selected) { None => consume(state), Some(value) => consume(state) };`, false},
		{"after-expression", "tag: i32", "i32", `let state = State { values: [1] }; let n = match (tag) { 0 => consume(state), _ => consume(state) }; return n + state.values.len();`, true},
		{"expression-order", "tag: i32", "i32", `let state = State { values: [1] }; return (match (tag) { 0 => consume(state), _ => consume(state) }) + state.values.len();`, true},
		{"same-arm", "tag: i32", "i32", `let state = State { values: [1] }; return match (tag) { 0 => consume(state) + consume(state), _ => 0 };`, true},
		{"guard-fallthrough", "tag: i32", "i32", `let state = State { values: [1] }; return match (tag) { 0 when guard(state) => 0, _ => consume(state) };`, true},
		{"captured", "tag: i32", "i32", `let state = State { values: [1] }; let later = () => state.values.len(); return match (tag) { 0 => consume(state), _ => consume(state) } + later();`, true},
		{"loop-backedge", "tag: i32", "i32", `let state = State { values: [1] }; let n = 0; for i in [0, 1] { n += match (tag) { 0 => consume(state), _ => consume(state) }; } return n;`, true},
		{"borrowed", "state: State, tag: i32", "i32", `return match (tag) { 0 => consume(state), _ => consume(state) };`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := ownedValueMatchDecls + "function work(" + tc.params + "): " + tc.result + " { " + tc.body + " } function main(): i32 { return 0; }"
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

func TestSelfHostOwnedValueMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_value_match_checker")
	ownedValueMatchCases(t, bin, runner, dir)
}

func TestSelfHostOwnedValueMatchWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_value_match_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedValueMatchCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostOwnedValueMatchRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := ownedValueMatchDecls + `
function work(own wrapper: Wrapper): i32 {
 let Wrapper { state, selected } = wrapper;
 return match (selected) { None => consume(state), Some(value) => if (value > 1) { consume(state) } else { consume(state) } };
}
function main(): i32 {
 let a = work(Wrapper { state: State { values: [args().len(), 2] }, selected: None });
 let b = work(Wrapper { state: State { values: [args().len(), 2, 3] }, selected: Some(1) });
 let c = work(Wrapper { state: State { values: [args().len()] }, selected: Some(2) });
 return if (a == 2 && b == 3 && c == 1) { 0 } else { 1 };
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
