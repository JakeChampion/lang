package e2e

import "testing"

// TestErrDefer exercises `errdefer EXPR;` — cleanup that runs only on an
// ERROR exit: the `?` operator propagating a None/Err, or a `return` of a
// failure variant (None / Err) from an Option/Result-returning function. A
// plain success return or fall-off the end must NOT run it.
//
// Side effects are observed through a Cell so the whole contract is encoded
// in the process exit code (no stdout diffing). Each case is run on every
// backend — the IR layer is target-agnostic, so arm64 / x86-64 / wasm must
// all agree (and they agree with the interpreter, which the differential
// harness checks).
func TestErrDefer(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// Plain success return: errdefer does NOT fire.
		{"success_no_fire", `function f(out: Cell[i32], x: i32): Result[i32, i32] {
    errdefer out.set(9);
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (f(a, 5)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 0},
		// Explicit `return Err(...)`: errdefer fires.
		{"err_return_fires", `function f(out: Cell[i32], x: i32): Result[i32, i32] {
    errdefer out.set(9);
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (f(a, -1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 9},
		// `?` propagating an Err: errdefer fires.
		{"try_propagation_fires", `function inner(x: i32): Result[i32, i32] {
    if (x < 0) { return Err(2); }
    return Ok(x);
}
function outer(out: Cell[i32], x: i32): Result[i32, i32] {
    errdefer out.set(9);
    var y: i32 = inner(x)?;
    return Ok(y + 1);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (outer(a, -1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 9},
		// `?` succeeding: errdefer does NOT fire.
		{"try_success_no_fire", `function inner(x: i32): Result[i32, i32] {
    if (x < 0) { return Err(2); }
    return Ok(x);
}
function outer(out: Cell[i32], x: i32): Result[i32, i32] {
    errdefer out.set(9);
    var y: i32 = inner(x)?;
    return Ok(y + 1);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (outer(a, 5)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 0},
		// Option: `return None` fires the errdefer; `return Some` does not.
		{"option_none_fires", `function opt(out: Cell[i32], x: i32): Option[i32] {
    errdefer out.set(5);
    if (x < 0) { return None; }
    return Some(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (opt(a, -1)) { Some(v) => {}, None => {} }
    return a.get();
}`, 5},
		{"option_some_no_fire", `function opt(out: Cell[i32], x: i32): Option[i32] {
    errdefer out.set(5);
    if (x < 0) { return None; }
    return Some(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (opt(a, 7)) { Some(v) => {}, None => {} }
    return a.get();
}`, 0},
		// defer + errdefer together. defer runs on every exit; errdefer only
		// on the error exit, and after the defer (so the read-modify-writes
		// compose in a fixed order). Success: 0 -> +1 (defer) = 1.
		{"defer_and_errdefer_success", `function h(out: Cell[i32], x: i32): Result[i32, i32] {
    defer out.set(out.get() + 1);
    errdefer out.set(out.get() + 10);
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (h(a, 5)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 1},
		// Error: 0 -> +1 (defer) -> +10 (errdefer) = 11.
		{"defer_and_errdefer_error", `function h(out: Cell[i32], x: i32): Result[i32, i32] {
    defer out.set(out.get() + 1);
    errdefer out.set(out.get() + 10);
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (h(a, -1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 11},
		// Conditionally-reached errdefer: only fires when the registering
		// statement actually ran (the per-defer active flag).
		{"conditional_registered_fires", `function cond(out: Cell[i32], reg: boolean, x: i32): Result[i32, i32] {
    if (reg) { errdefer out.set(7); }
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (cond(a, true, -1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 7},
		{"conditional_unregistered_no_fire", `function cond(out: Cell[i32], reg: boolean, x: i32): Result[i32, i32] {
    if (reg) { errdefer out.set(7); }
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (cond(a, false, -1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 0},
		// Multiple errdefers fire in LIFO order on error. Each appends its id
		// digit; reading back the final value pins the order: e1 then e2
		// registered, so on error e2 runs first, then e1 -> 0*10..; we encode
		// order by out = out*10 + id so LIFO gives 21.
		{"lifo_order", `function m(out: Cell[i32], x: i32): Result[i32, i32] {
    errdefer out.set(out.get() * 10 + 1);
    errdefer out.set(out.get() * 10 + 2);
    if (x < 0) { return Err(1); }
    return Ok(x);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (m(a, -1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 21},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != c.want {
					t.Errorf("interp exit = %d, want %d", code, c.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != c.want {
					t.Errorf("arm64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86_64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
		})
	}
}
