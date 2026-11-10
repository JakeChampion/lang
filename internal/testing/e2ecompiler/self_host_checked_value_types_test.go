package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCheckedValueTypes(t *testing.T) {
	const key = `@value struct Key { bytes: u8[] } function (own k: Key) erase(): void {} `
	cases := []struct {
		name, source string
		code         string
	}{
		{"borrowed cleanup", key + `function f(k: Key): i32 { k.erase(); return k.bytes.len(); }`, ""},
		{"aggregate loan", key + `struct Identity { key: Key } function consume(own i: Identity): i32 { return i.key.bytes.len(); } function f(k: Key): i32 { return consume(Identity { key: k }) + k.bytes.len(); }`, ""},
		{"nested immutable", `struct Data { items: Option[u8[]] } @value struct Key { data: Data } function (own k: Key) erase(): void {} function f(k: Key): void { k.erase(); }`, ""},
		{"recursive immutable", `@value struct Tree { children: Tree[], data: u8[] } function (own t: Tree) erase(): void {} function f(t: Tree): void { t.erase(); }`, ""},
		{"descriptor", `@value struct Bad { socket: Socket }`, "E051"},
		{"nested descriptor", `struct Part { socket: Socket } @value struct Bad { parts: Part[] }`, "E051"},
		{"cell", `@value struct Bad { data: Cell[i32] }`, "E051"},
		{"callback", `@value struct Bad { data: () => i32 }`, "E051"},
		{"view", `@value struct Bad { data: [u8] }`, "E051"},
		{"unbounded parameter", `@value struct Bad[T] { data: T }`, "E051"},
		{"inferred handle", `struct Handle { fd: i32 } function (own h: Handle) close(): void {} @value struct Bad { handle: Handle }`, "E051"},
		{"obligation", `@must_consume struct Part { bytes: u8[] } @value struct Bad { part: Part }`, "E051"},
		{"enum obligation", `@must_consume enum Part { SomeData(u8[]), Empty } @value struct Bad { part: Part }`, "E051"},
		{"union cell", `struct A { data: Cell[i32] } struct B { n: i32 } type Choice = A | B; @value struct Bad { data: Choice }`, "E051"},
		{"unmarked cleanup", `struct Key { bytes: u8[] } function (own k: Key) erase(): void {} function f(k: Key): void { k.erase(); }`, "E051"},
		{"explicit own stays moved", key + `function f(own k: Key): i32 { k.erase(); return k.bytes.len(); }`, "E050"},
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
			bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "checked_value_types", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Stdin = strings.NewReader(tc.source)
					for label, ds := range map[string][]driverDiag{
						"Go":   goCheckerDiags(t, dir, tc.source),
						"Fern": driverDiags(runCheckerDriver(t, cmd, tc.name)),
					} {
						if (tc.code != "") != (len(ds) > 0) {
							t.Fatalf("%s: unexpected diagnostics: %v", label, ds)
						}
						for _, d := range ds {
							if d.code != tc.code {
								t.Fatalf("%s: expected %s, got %v", label, tc.code, ds)
							}
						}
					}
				})
			}
		})
	}
}
