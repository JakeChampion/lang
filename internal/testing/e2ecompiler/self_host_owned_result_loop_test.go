package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func ownedResultLoopCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct State { n: i32 }
enum Failure { Bad }
function make(): Result[State, Failure] { return Ok(State { n: 0 }); }
function (own s: State) step(): Result[(State, i32), Failure] { return Ok((State { n: s.n + 1 }, 1)); }
function (s: State) lend(): Result[State, Failure] { return Ok(s); }
struct Borrower { state: State }
function (b: Borrower) step(): Result[State, Failure] { return Ok(b.state); }
function take(own s: State): i32 { return s.n; }
`
	const init = `let state = match (make()) { Ok(value) => value, Err(_) => { return 1; } };`
	const unpack = `let (next, used) = match (state.step()) { Ok(value) => value, Err(_) => { return 2; } };`
	cases := []struct{ name, body, want string }{
		{"match-for", init + `for i in 0..2 { ` + unpack + ` state = next; } return state.n - 2;`, ""},
		{"literal-for", `let state = State { n: 0 }; for i in 0..2 { ` + unpack + ` state = next; } return state.n - 2;`, ""},
		{"match-while", init + `let i = 0; while (i < 2) { ` + unpack + ` state = next; i += used; } return state.n - 2;`, ""},
		{"let-else-for", `let Ok(state) = make() else { return 1; }; for i in 0..2 { let Ok((next, used)) = state.step() else { return 2; }; state = next; } return state.n - 2;`, ""},
		{"let-else-while", `let Ok(state) = make() else { return 1; }; let i = 0; while (i < 2) { let Ok((next, used)) = state.step() else { return 2; }; state = next; i += used; } return state.n - 2;`, ""},
		{"consume-method-payload", `let Ok(state) = make() else { return 1; }; let Ok((next, used)) = state.step() else { return 2; }; let Ok((done, count)) = next.step() else { return 3; }; return done.n - 2;`, ""},
		{"borrowed-method-payload", `let Ok(state) = make() else { return 1; }; let Ok(next) = state.lend() else { return 2; }; let Ok((done, count)) = next.step() else { return 3; }; return done.n;`, "E051"},
		{"same-name-borrowed-method", `let borrower = Borrower { state: State { n: 0 } }; let Ok(next) = borrower.step() else { return 1; }; let Ok((done, count)) = next.step() else { return 2; }; return done.n;`, "E051"},
		{"missing-rebind", init + `for i in 0..2 { ` + unpack + ` } return 0;`, "E051"},
		{"owned-missing-rebind", `let Ok(state) = make() else { return 1; }; for i in 0..2 { let Ok((next, used)) = state.step() else { return 2; }; } return 0;`, "E050"},
		{"conditional-rebind", init + `for i in 0..2 { ` + unpack + ` if (i == 0) { state = next; } } return 0;`, "E051"},
		{"owned-conditional-rebind", `let Ok(state) = make() else { return 1; }; for i in 0..2 { let Ok((next, used)) = state.step() else { return 2; }; if (i == 0) { state = next; } } return 0;`, "E050"},
		{"double-consume-next", `let Ok(state) = make() else { return 1; }; for i in 0..2 { let Ok(pair) = state.step() else { return 2; }; let (next, used) = pair; let ignored = take(next); state = next; } return 0;`, "E051"},
		{"read-before-rebind", `let Ok(state) = make() else { return 1; }; for i in 0..2 { let Ok((next, used)) = state.step() else { return 2; }; let stale = state.n; state = next; } return 0;`, "E050"},
		{"literal-condition-keeps-source-rules", `let Ok(state) = make() else { return 1; }; for i in 0..2 { let Ok((next, used)) = state.step() else { return 2; }; if (true) { state = next; } } return 0;`, "E050"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "function main(): i32 { " + tc.body + " }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.want == "" {
					if len(ds) != 0 {
						t.Fatalf("%s rejected valid state loop: %v", label, ds)
					}
					return
				}
				for _, d := range ds {
					if d.code == tc.want {
						return
					}
				}
				t.Fatalf("%s: want %s, got %v", label, tc.want, ds)
			}
			check("Go", goCheckerDiags(t, dir, src))
			cmd := exec.Command(bin)
			if len(runner) != 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			check("self-host", driverDiags(runCheckerDriver(t, cmd, tc.name)))
		})
	}
}

func TestSelfHostOwnedResultLoopX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_result_checker")
	ownedResultLoopCases(t, bin, runner, dir)
}

func TestSelfHostOwnedResultLoopWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_result_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedResultLoopCases(t, bin, []string{wasmtime, "run"}, dir)
}
