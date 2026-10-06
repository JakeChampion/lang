package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestResultConstructorIntegerConversion(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
	}{
		{"ok-narrow", `function f(n: i64): Result[i32, string] { return Ok(n); }`, "E002"},
		{"err-literal-overflow", `function f(): Result[string, u8] { return Err(300); }`, "E047"},
		{"qualified-literal-overflow", `function f(): Result[u8, string] { return Result.Ok(300); }`, "E047"},
		{"nested-literal-overflow", `function f(): Result[Result[u8, string], string] { return Ok(Ok(300)); }`, "E047"},
		{"option-literal-overflow", `function f(): Option[u8] { return Some(300); }`, "E047"},
		{"qualified-option-narrow", `function f(n: i64): Option[i32] { return Option.Some(n); }`, "E002"},
		{"same-name-method", `struct Factory {} impl Factory { function Ok(self: Self, n: i32): Result[u8, string] { return Result.Ok(1u8); } } function f(): Result[u8, string] { let x = Factory {}; return x.Ok(300); }`, ""},
		{"same-name-function", `function Ok(n: i32): Result[u8, string] { return Err("unused"); } function f(): Result[u8, string] { return Ok(300); }`, ""},
		{"unsigned-small-widen", `function f(n: u8): Result[u32, string] { return Ok(n); }`, ""},
		{"unsigned-small-narrow", `function f(n: u32): Result[string, u8] { return Err(n); }`, "E002"},
		{"signedness-wide", `function f(n: u32): Result[i64, string] { return Ok(n); }`, "E002"},
		{"pointer-width", `function f(n: usize): Result[u64, string] { return Ok(n); }`, "E002"},
		{"float-narrow", `function f(n: f64): Result[f32, string] { return Ok(n); }`, "E002"},
		{"option-widen", `function f(n: i32): Option[i64] { return Some(n); }`, "E002"},
		{"binding-narrow", `function f(n: i64): void { let r: Result[i32, string] = Ok(n); }`, "E003"},
		{"argument-narrow", `function take(r: Result[i32, string]): void {} function f(n: i64): void { take(Ok(n)); }`, "E038"},
		{"err-narrow", `function f(n: i64): Result[string, i32] { return Err(n); }`, "E002"},
		{"unsigned-narrow", `function f(n: u64): Result[u8, string] { return Result.Ok(n); }`, "E002"},
		{"ok-widen", `function f(n: i32): Result[i64, string] { return Ok(n); }`, ""},
		{"err-widen", `function f(n: u8): Result[string, u64] { return Result.Err(n); }`, ""},
		{"literal", `function f(): Result[u8, string] { return Ok(3); }`, ""},
		{"literal-overflow", `function f(): Result[u8, string] { return Ok(300); }`, "E047"},
		{"sign-change", `function f(n: i32): Result[u32, string] { return Ok(n); }`, "E002"},
		{"option-narrow", `function f(n: i64): Option[i32] { return Some(n); }`, "E002"},
		{"existing-result", `function f(n: Result[i64, string]): Result[i32, string] { return n; }`, "E002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.source)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(diag.Format("conversion.fern", tc.source, err), "error["+tc.code+"]") {
				t.Fatalf("expected %s, error=%v", tc.code, err)
			}
		})
	}
}
