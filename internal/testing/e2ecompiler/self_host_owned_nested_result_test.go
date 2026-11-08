package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ownedNestedDecls = `struct State { n: i32, bytes: u8[] }
enum Failure { Bad }
function make(): Result[State, Failure] { return Ok(State { n: 0, bytes: [7u8] }); }
function (own s: State) next(): Result[(State, Option[i32]), Failure] {
  if (s.n < 0) { return Err(Failure.Bad); }
  if (s.n >= 2) { return Ok((s, None)); }
  return Ok((State { n: s.n + 1, bytes: s.bytes }, Some(s.n)));
}
function (s: State) lend(): Result[(State, Option[i32]), Failure] { return Ok((s, Some(1))); }
function take(own s: State): i32 { return s.n; }
`

func ownedNestedResultCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct{ name, init, body, want string }{
		{"owned-rebind", "", `while (true) { match (state.next()) { Ok((next, Some(_))) => { state = next; }, _ => { return 0; } } }`, ""},
		{"literal-rebind", `let state = State { n: 0, bytes: [] };`, `for i in 0..2 { match (state.next()) { Ok((next, Some(_))) => { state = next; }, _ => { return 1; } } } return state.n;`, ""},
		{"result-rebind", `let Ok(state) = make() else { return 1; };`, `while (true) { match (state.next()) { Ok((next, Some(_))) => { state = next; }, Err(Failure.Bad) => { return 2; }, _ => { return 0; } } }`, ""},
		{"guard-rebind", "", `while (true) { match (state.next()) { Ok((next, Some(_))) when flag => { state = next; }, _ => { return 0; } } }`, ""},
		{"tuple-root", "", `while (true) { match ((state.next(), flag)) { (Ok((next, Some(_))), true) => { state = next; }, _ => { return 0; } } }`, ""},
		{"nested-tuple", "", `while (true) { match ((flag, (state.next(), 1))) { (true, (Ok((next, Some(_))), 1)) => { state = next; }, _ => { return 0; } } }`, ""},
		{"missing-rebind", "", `while (true) { match (state.next()) { Ok((next, Some(_))) => {}, _ => { return 0; } } }`, "E050"},
		{"conditional-rebind", "", `while (true) { match (state.next()) { Ok((next, Some(_))) => { if (flag) { state = next; } }, _ => { return 0; } } }`, "E050"},
		{"later-read", "", `while (true) { match (state.next()) { Ok((next, Some(_))) => { let stale = state.n; state = next; }, _ => { return 0; } } }`, "E050"},
		{"duplicate-payload", "", `while (true) { match (state.next()) { Ok((next, Some(_))) => { let n = take(next); state = next; }, _ => { return 0; } } }`, "E050"},
		{"borrowed-payload", "", `match (state.lend()) { Ok((next, Some(_))) => { return take(next); }, _ => { return 0; } }`, "E051"},
		{"fresh-tuple-component", "", `match ((state.lend(), flag)) { (Ok((next, Some(_))), true) => { return take(next); }, _ => { return 0; } }`, ""},
		{"guard-fallthrough", "", `let other = State { n: 0, bytes: [] }; match (state.next()) { Ok((next, Some(_))) when take(other) > 0 => { return 1; }, _ => { return other.n; } }`, "E051"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := "own state: State, flag: boolean"
			if tc.init != "" {
				params = "flag: boolean"
			}
			source := ownedNestedDecls + "function work(" + params + "): i32 { " + tc.init + tc.body + " } function main(): i32 { return 0; }"
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

func TestSelfHostOwnedNestedResultX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_nested_checker")
	ownedNestedResultCases(t, bin, runner, dir)
}

func TestSelfHostOwnedNestedResultWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_nested_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedNestedResultCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostOwnedNestedResultRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := ownedNestedDecls + `function work(n: i32, flag: boolean): i32 {
  let state = State { n: n, bytes: [7u8] };
  while (true) {
    match ((state.next(), flag)) {
      (Ok((next, Some(_))), true) => { state = next; },
      (Ok((next, None)), true) => { return next.n + next.bytes[0] as i32; },
      _ => { return 0; }
    }
  }
}
function main(): i32 { return work(0, true) + work(-1, true) + work(0, false) - 9; }`
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
