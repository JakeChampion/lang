package checker

import "testing"

func TestPartialEnumPayloadInference(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"inferred-lambda-result", `function main(): i32 { let f = () => { return Ok(3); }; match(f()) { Ok(v) => { return v; }, Err(_) => { return 0; } } }`},
		{"inferred-lambda-result-join", `function exercise(flag: boolean): void { let f = () => { if (flag) { return Ok(3); } return Err("failure"); }; match(f()) { Ok(v) => { assert(v == 3); }, Err(e) => { assert(e == "failure"); } } }`},
		{"direct-extracted", `function make(): i64 { match (Ok(3)) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } } function main(): i32 { assert(make() == 3i64); return 0; }`},
		{"same-parameter", `enum Choice[T, E] { Pair(T, T), Reject(E) } function main(): i32 { let o = Pair(1, 4294967297i64); match(o) { Pair(a, b) => { assert(a == 1i64); assert(b == 4294967297i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"same-parameter-reversed", `enum Choice[T, E] { Pair(T, T), Reject(E) } function main(): i32 { let o = Pair(4294967297i64, 1); match(o) { Pair(a, b) => { assert(a == 4294967297i64); assert(b == 1i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"closure-context", `function main(): i32 { let o = Ok(2147483647 + 1); let f = (): i64 => { match(o) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } }; assert(f() == 2147483648i64); return 0; }`},
		{"qualified-collision", `enum Left[T, E] { Pick(T), Reject(E) } enum Right[T, E] { Pick(T), Reject(E) } function make(): Right[i64, string] { let o = Right.Pick(2147483647 + 1); return o; } function main(): i32 { match(make()) { Pick(v) => { assert(v == 2147483648i64); }, Reject(_) => { return 1; } } return 0; }`},
		{"ok-local", `function main(): i32 { let o = Ok(3); match (o) { Ok(v) => { return v + 1; }, Err(e) => { return 0; } } }`},
		{"ok-direct", `function main(): i32 { match (Ok(3)) { Ok(v) => { return v + 1; }, Err(e) => { return 0; } } }`},
		{"err-local", `function main(): i32 { let o = Err("x"); match (o) { Ok(v) => { return 0; }, Err(e) => { return e.len(); } } }`},
		{"err-direct", `function main(): i32 { match (Err("x")) { Ok(v) => { return 0; }, Err(e) => { return e.len(); } } }`},
		{"ok-lambda-direct", `function main(): i32 { let f = (): i32 => { match (Ok(3)) { Ok(v) => { return v + 1; }, Err(_) => { return 0; } } }; return f(); }`},
		{"err-lambda-direct", `function main(): i32 { let f = (): i32 => { match (Err("x")) { Ok(_) => { return 0; }, Err(e) => { return e.len(); } } }; return f(); }`},
		{"user-enum", `enum Choice[T, E] { Pick(T), Reject(E) } function main(): i32 { let p = Pick(3); match (p) { Pick(n) => { return n + 1; }, Reject(e) => { return 0; } } }`},
		{"later-context", `function f(): Result[i32, string] { let o = Ok(3); return o; }`},
		{"narrow-literal-context", `function f(): Result[u8, string] { let o = Ok(3); return o; }`},
		{"arithmetic-literal-context", `function f(): Result[i64, string] { let o = Ok(2147483647 + 1); return o; }`},
		{"wide-literal-default", `function main(): i32 { let o = Ok(4294967297); match (o) { Ok(v) => { assert(v == 4294967297i64); }, Err(_) => { return 1; } } return 0; }`},
		{"extracted-literal-context", `function f(): i64 { let o = Ok(3); match (o) { Ok(v) => { return v; }, Err(_) => { return 0i64; } } }`},
		{"later-error-context", `function f(): Result[i32, string] { let o = Err("x"); return o; }`},
		{"direct-widening", `function f(n: i32): Result[i64, string] { return Ok(n); }`},
		{"branch-join", `function main(): i32 { let o = if (true) { Ok(3) } else { Err("x") }; match (o) { Ok(v) => { return v + 1; }, Err(e) => { return e.len(); } } }`},
		{"generic-join", `function first[T](a: T, b: T): T { return a; } function main(): i32 { let o = first(Ok(3), Err("x")); match (o) { Ok(v) => { return v + 1; }, Err(e) => { return e.len(); } } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, tc.source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPartialEnumInferenceRejectsKnownConflicts(t *testing.T) {
	for _, source := range []string{
		`function exercise(flag: boolean): void { let f = () => { if (flag) { return Ok(3); } return Ok("failure"); }; }`,
		`function make(): u8 { match (Ok(300)) { Ok(v) => { return v; }, Err(_) => { return 0u8; } } } function main(): i32 { return 0; }`,
		`function a(o: Result[i32, string]): void {} function b(o: Result[i64, string]): void {} function main(): i32 { let o = Ok(3); a(o); b(o); return 0; }`,
		`function f(): u8 { let o = Ok(300); match (o) { Ok(v) => { return v; }, Err(_) => { return 0u8; } } }`,
		`function f(): Result[u8, string] { let o = Ok(300); return o; }`,
		`function f(): Result[u32, string] { let o = Ok(-3); return o; }`,
		`function f(): Result[i32, string] { let o = Ok(4294967297); return o; }`,
		`function f(): Result[string, boolean] { let o = Ok(3); return o; }`,
		`function f(): Result[i32, boolean] { let o = Err("x"); return o; }`,
		`function main(): i32 { let o = if (true) { Ok(3) } else { Ok("x") }; return 0; }`,
		`function main(): i32 { let o = Ok("x"); match (o) { Ok(v) => { return v + 1; }, Err(e) => { return 0; } } }`,
		`function main(): i32 { let o = Err(3); match (o) { Ok(v) => { return 0; }, Err(e) => { return e.len(); } } }`,
	} {
		if err := checkSource(t, source); err == nil {
			t.Fatalf("accepted conflicting payload type: %s", source)
		}
	}
}
