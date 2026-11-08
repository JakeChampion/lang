package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func ownedShadowCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct State { n: i32 }
function (own s: State) step(): State { return State { n: s.n + 1 }; }
function take(own s: State): State { return s; }
function pair(own s: State): (State, i32) { return (s, 1); }
function both(own a: State, own b: State): State { return State { n: a.n + b.n }; }
`
	cases := []struct{ name, body, want string }{
		{"argument", `match (Some(1)) { None => { return next; }, Some(value) => { let next = take(next); return next; } }`, ""},
		{"receiver", `match (Some(1)) { None => { return next; }, Some(value) => { let next = next.step(); return next; } }`, ""},
		{"tuple", `match (Some(1)) { None => { return next; }, Some(value) => { let (next, used) = pair(next); return next; } }`, ""},
		{"if", `if (true) { let next = take(next); return next; } return next;`, ""},
		{"later-shadow", `if (true) { let out = take(next); let next = State { n: 2 }; return next; } return next;`, ""},
		{"outer-read", `if (true) { let next = take(next); let n = next.n; } return next;`, "E051"},
		{"duplicate-argument", `if (true) { let next = both(next, next); return next; } return next;`, "E051"},
		{"break", `for i in 0..2 { let next = take(next); break; } return next;`, "E051"},
		{"continue", `for i in 0..2 { let next = take(next); continue; } return State { n: 0 };`, "E051"},
		{"capture", `let view = () => next.n; if (true) { let next = take(next); return next; } return next;`, "E051"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + `function work(): State { let next = State { n: 1 }; ` + tc.body + ` } function main(): i32 { return work().n; }`
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
			check("Go", goCheckerDiags(t, dir, src))
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			check("self-host", driverDiags(runCheckerDriver(t, cmd, tc.name)))
		})
	}
}

func TestSelfHostOwnedShadowInitializerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_shadow_checker")
	ownedShadowCases(t, bin, runner, dir)
}

func TestSelfHostOwnedShadowInitializerWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_shadow_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedShadowCases(t, bin, []string{wasmtime, "run"}, dir)
}
