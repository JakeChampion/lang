package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A `string` meets a `str` on purpose in `+`, `==` and an argument, and the
// two checkers accept that. A join of an owned arm and a view arm is E031 in
// every form of branch (#14573).
func ownedViewJoinCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct{ name, body, code string }{
		{"concat-string-str", `let s = a + v; let n = s.len();`, ""},
		{"concat-str-string", `let s = v + a; let n = s.len();`, ""},
		{"eq-string-str", `if (a == v) { return 1; }`, ""},
		{"ne-str-string", `if (v != a) { return 1; }`, ""},
		{"eq-inside-and", `if (a == v && c) { return 1; }`, ""},
		{"string-to-str-argument", `let n = take(a);`, ""},
		{"eq-owned-array-view", `if (xs == ys) { return 1; }`, "E041"},
		{"array-of-string-and-str", `let t = [a, v];`, "E034"},
		{"if-string-str", `let s = if (c) { a } else { v };`, "E031"},
		{"if-tuple-string-str", `let t = if (c) { (a, 1) } else { (v, 1) };`, "E031"},
		{"if-owned-array-view", `let t = if (c) { xs } else { ys };`, "E031"},
		{"match-bool-string-str", `let s = match (c) { true => a, false => v };`, "E031"},
		{"match-int-str-string", `let s = match (xs.len()) { 0 => v, _ => a };`, "E031"},
		{"match-int-owned-array-view", `let t = match (xs.len()) { 0 => xs, _ => ys };`, "E031"},
		{"match-int-tuple-string-str", `let t = match (xs.len()) { 0 => (a, 1), _ => (v, 1) };`, "E031"},
		{"match-int-same-strings", `let s = match (xs.len()) { 0 => a, _ => a + "!" };`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "function take(v: str): i32 { return v.len(); }\n" +
				"function work(a: string, v: str, xs: i32[], ys: [i32], c: boolean): i32 { " + tc.body + " return 0; }\n" +
				"function main(): i32 { return 0; }\n"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.code == "" {
					if len(ds) != 0 {
						t.Fatalf("%s: want no diagnostic, got %v", label, ds)
					}
					return
				}
				for _, d := range ds {
					if d.code == tc.code {
						return
					}
				}
				t.Fatalf("%s: want %s, got %v", label, tc.code, ds)
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

func TestSelfHostOwnedViewJoinX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_view_join_checker")
	ownedViewJoinCases(t, bin, runner, dir)
}

func TestSelfHostOwnedViewJoinWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_view_join_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedViewJoinCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}
