package e2eselfhost

import "testing"

// tostringFreshRetCases pin a helper whose return is a scalar `.to_string()` —
// `function util_num(i: i32): string { return i.to_string(); }` — entering the
// whole-program fresh-ret registry, so `var sv = util_num(i)` reclaims the box
// the callee moved out.
//
// Isolated by varying only the callee body; the AST lowering leaked on the
// first two before the fix:
//
//	return i.to_string();                32 B/round
//	var t = i.to_string(); return t;     32
//	return "x" + i.to_string();           0
//	return "abc";                         0
//
// Only a provably scalar receiver admits: a struct `to_string` may hand back
// an alias of a live field.
var tostringFreshRetCases = []struct {
	name string
	src  string
	want int
}{
	// The gate. 32 before the fix on all three backends; native flat.
	{"freshret-tostring-direct-bind", `function util_num(i: i32): string {
    return i.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The same helper as a CONCAT OPERAND rather than a direct bind — the shape
	// the leak was first spotted in.
	{"freshret-tostring-concat-operand", `function util_num(i: i32): string {
    return i.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = "v" + util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The callee binds the conversion to a LOCAL first and returns it. That path
	// runs through str_local_is_fresh_ret -> strloc_declared_fresh, which is why
	// the params thread has to reach the local-declaration test too.
	{"freshret-tostring-via-local", `function util_num(i: i32): string {
    var t: string = i.to_string();
    return t;
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// Non-vacuity control on the other side: a callee returning a CONCAT was
	// already registered and must stay at 0.
	{"freshret-concat-return-unchanged", `function util_num(i: i32): string {
    return "x" + i.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// REFUSAL control. The receiver is a STRUCT with a user `to_string`, whose
	// return is a live field — an alias, not a handover. tostring_recv_is_scalar_param
	// answers false on a non-scalar declared type, so the helper stays out of the
	// registry and the caller must not release. Over-release shows as 99.
	{"freshret-struct-tostring-refused", `struct Tag { s: string }
function (t: Tag) to_string(): string {
    return t.s;
}
function util_tag(t: Tag): string {
    return t.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var tg: Tag = Tag { s: "abc" };
        var sv: string = util_tag(tg);
        acc = (acc + sv.len() + tg.s.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 85},
	// REFUSAL control. `to_string` on a STRING receiver is the identity-ish
	// builtin, not the decimal-text producer: the result may be the receiver's
	// own box. A string-typed param is not a scalar, so this stays refused.
	{"freshret-string-tostring-refused", `function util_s(s: string): string {
    return s.to_string();
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var base: string = "v" + i.to_string();
        var sv: string = util_s(base);
        acc = (acc + sv.len() + base.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 45},
}

const tostringFreshRetImports = "import \"std/i32\";\nimport \"std/string\";\n"

const tostringFreshRetFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostTostringFreshRet(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range tostringFreshRetCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tostringFreshRetImports+tc.src+"\n", target); code != tc.want {
					t.Errorf(tostringFreshRetFailFmt+"\n%s", tc.name, code, tc.want, stderr)
				}
			})
		}
	}
}
