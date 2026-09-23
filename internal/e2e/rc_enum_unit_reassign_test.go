package e2e

import "testing"

// A local initialised from a qualified unit variant (`Pick.First`) owns
// the static sentinel, so a payload box assigned over it later is released
// like any other owned enum (#10103). Each shape runs six trips.
func TestEnumLocalReassignedFromAUnitVariant(t *testing.T) {
	const loop = `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        t = t + trip(i);
        i = i + 1;
    }
    return t;
}`
	cases := []struct {
		name, src string
		want      int
	}{
		{"scalar_payload", `enum Pick { First, Second(i32) }
function trip(i: i32): i32 {
    var c: Pick = Pick.First;
    c = Pick.Second(i);
    return match (c) { First => 100, Second(n) => n };
}`, 15},
		{"passed_to_a_call", `enum Pick { First, Second(i32) }
function score(p: Pick): i32 { return match (p) { First => 100, Second(n) => n }; }
function trip(i: i32): i32 {
    var c: Pick = Pick.First;
    c = Pick.Second(i + 1);
    return score(c);
}`, 21},
		{"string_payload", `enum Named { Anon, Called(string) }
function trip(i: i32): i32 {
    var n: Named = Named.Anon;
    n = Named.Called("ab" + "c");
    return match (n) { Anon => 100, Called(s) => s.len() };
}`, 18},
		{"returned", `enum Named { Anon, Called(string) }
function mk(i: i32): Named {
    var r: Named = Named.Anon;
    if (i % 2 == 0) { r = Named.Called("n" + "m"); }
    return r;
}
function trip(i: i32): i32 {
    var r: Named = mk(i);
    return match (r) { Anon => 1, Called(s) => s.len() };
}`, 9},
		{"generic_option", `function trip(i: i32): i32 {
    var o: Option[string] = Option.None;
    o = Some("x" + "yz");
    return match (o) { None => 100, Some(s) => s.len() };
}`, 18},
	}
	for _, c := range cases {
		src := c.src + loop
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, src, c.want, runSanitizeX86_64)
		})
		t.Run("arm64-sanitize/"+c.name, func(t *testing.T) {
			checkSanitizedBalanced(t, src, c.want, runSanitizeArm64)
		})
		t.Run("wasm/"+c.name, func(t *testing.T) {
			if got := runWasm(t, src); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
