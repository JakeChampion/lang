package e2eselfhost

import "testing"

// A scalar leaf is spliced into its callers by the semantic lowering
// (seminline, #10498), and a tuple it returns is read apart instead of
// allocated. Each probe runs 100 rounds and prints its result times 1000 plus
// the heap allocations the rounds made. divmod's one return and scan's two
// returns joined by a phi allocate nothing once spliced; kept is the same body
// as divmod under @noinline, so its tuple is still a box a round. clamp
// returns a scalar through three blocks and allocates nothing either way.
// span reads two fields of a record parameter into a tuple, and kept_span is
// the same body under @noinline. pick reads the fields of a record a phi
// chooses, which is not a parameter, so it stays a call and its tuple a box.
const semInlineProgram = `struct Range { lo: i32, hi: i32 }
function span(r: Range): (i32, i32) { return (r.lo, r.hi - r.lo); }
@noinline function kept_span(r: Range): (i32, i32) { return (r.lo, r.hi - r.lo); }
function pick(c: boolean, a: Range, b: Range): (i32, i32) {
    let r: Range = a;
    if (c) { r = b; }
    return (r.lo, r.hi);
}
function divmod(a: i32, b: i32): (i32, i32) { return (a / b, a % b); }
function scan(n: i32): (boolean, i32) {
    if (n > 50) { return (true, n - 50); }
    return (false, n);
}
function clamp(v: i32, lo: i32, hi: i32): i32 {
    if (v < lo) { return lo; }
    if (v > hi) { return hi; }
    return v;
}
@noinline function kept(a: i32, b: i32): (i32, i32) { return (a / b, a % b); }
@noinline function divmod_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (q, r) = divmod(i + 7, 3); t = t + q + r; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function scan_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (hit, rest) = scan(i); if (hit) { t = t + rest; } i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function clamp_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + clamp(i, 10, 20); i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function kept_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (q, r) = kept(i + 7, 3); t = t + q + r; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function span_rounds(r: Range): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (lo, width) = span(r); t = t + lo + width + i; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function kept_span_rounds(r: Range): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (lo, width) = kept_span(r); t = t + lo + width + i; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function pick_rounds(a: Range, b: Range): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let (lo, hi) = pick(i % 2 == 0, a, b); t = t + lo + hi; i = i + 1; }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(divmod_rounds()); print("");
    print_int(scan_rounds()); print("");
    print_int(clamp_rounds()); print("");
    print_int(kept_rounds()); print("");
    let r: Range = Range { lo: 3, hi: 10 };
    print_int(span_rounds(r)); print("");
    print_int(kept_span_rounds(r)); print("");
    print_int(pick_rounds(r, Range { lo: 1, hi: 4 })); print("");
    return 0;
}
`

var semInlineProduced = []string{"divmod", "scan", "clamp", "kept", "divmod_rounds", "scan_rounds", "clamp_rounds", "kept_rounds",
	"span", "kept_span", "span_rounds", "kept_span_rounds", "pick", "pick_rounds"}

func semInlineWants(want string) map[string]string {
	return map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want}
}

func TestSelfHostSemanticInline(t *testing.T) {
	runSemanticProgram(t, "seminline", semInlineProgram, semInlineProduced,
		semInlineWants("1950000\n1225000\n1845000\n1950100\n5950000\n5950100\n900100\n"))
}

// FERN_SEM_INLINE= turns the pass off, and every tuple is a box again.
func TestSelfHostSemanticInlineOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "seminline-off", semInlineProgram, semInlineProduced,
		semInlineWants("1950100\n1225100\n1845000\n1950100\n5950100\n5950100\n900100\n"))
}
