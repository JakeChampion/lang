package e2ecompiler

import "testing"

// Plain string views can enter an inlined function with an owned result.
// The caller must retain each temporary source until the inlined reads finish.
const inlineStrParamProgram = `@noinline function fresh(s: string): string {
    return s + "-a-payload-long-enough-to-use-owned-heap-storage-0123456789";
}
function sample(s: str): (f64, i32) {
    if (s.len() == 0) { return (0.0, -1); }
    return (s[0] as f64, s.len());
}
function wrapped(s: str): Result[f64, string] {
    let (value, size) = sample(s);
    if (size < 0) { return Err("empty"); }
    return Ok(value);
}
function returned_view(s: str): str { return s; }
@noinline function exercise(): i32 {
    let base: string = fresh("abc");
    let before: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 100) {
        match (wrapped(base)) {
            Ok(value) => { if (value != 97.0) { return 1; } },
            Err(_) => { return 2; }
        }
        i = i + 1;
    }
    if (__heap_alloc_count() != before) { return 5; }
    match (wrapped(slice_unchecked(fresh("xyz"), 1, 4))) {
        Ok(value) => { if (value != 121.0) { return 6; } },
        Err(_) => { return 7; }
    }
    let (empty_value, empty_size) = sample("");
    if (empty_value != 0.0 || empty_size != -1) { return 3; }
    let view: str = returned_view(base);
    let churn: string = fresh("XXXXXXXX");
    if (view[0] != 97 || view.len() != base.len() || churn.len() < 1) { return 8; }
    if (__rc_underflow_count() != 0) { return 9; }
    return 0;
}
function main(): i32 {
    let result: i32 = exercise();
    if (result != 0) { return result; }
    print("ok");
    return 0;
}
`

func TestSelfHostInlineStrParams(t *testing.T) {
	runSemanticProgram(t, "str-params", inlineStrParamProgram,
		[]string{"fresh", "sample", "wrapped", "returned_view", "exercise"},
		semInlineWants("ok\n"), "fresh", "returned_view", "exercise")
}
