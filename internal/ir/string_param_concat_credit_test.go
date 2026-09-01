package ir

import "testing"

// #7914 frontier: a string parameter used as a CONCAT OPERAND retains
// nothing. `__fern_strcat` copies both operands' bytes into a fresh
// allocation (or an SSO-inline value, or the shared empty sentinel) and
// never hands back either operand's pointer, so the occurrence is as
// non-retaining as `p.len()` or a byte index — and it is the commonest
// use a string parameter has.
//
// Its absence did not stop at strings: an encoder helper
// `put(reg, key, flags) -> reg.with(b, reg[b] + key + "|" + flags)`
// refused both string params, computeFreeEligible then tainted the
// caller's `flags` local at the call, and rhsTainted's counted-argument
// check carried that taint into the caller's ARRAY local. The self-host's
// interprocedural borrow fixpoint stranded all ten of its 3 KB
// registries per compile that way.

func TestStringParamConcatOperandIsCounted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"right operand", `return ("k" + p).len();`},
		{"left operand", `return (p + "k").len();`},
		{"chained, both sides", `return (p + "|" + p).len();`},
		{"into an accumulator local", `var acc: string = "";
             var i: i32 = 0;
             while (i < 3) { acc = acc + p; i = i + 1; }
             return acc.len();`},
	}
	for _, c := range cases {
		src := "function eat(p: string): i32 { " + c.body + " }\nfunction main(): i32 { return 0; }"
		got := paramCountedFor(t, src, "eat")
		if len(got) != 1 || !got[0] {
			t.Errorf("%s: paramCountedRetain[eat] = %v, want [true] — concat copies the "+
				"operand's bytes into a fresh buffer and retains nothing", c.name, got)
		}
	}
}

// A string comparison reads the same bytes and yields a bool, which
// cannot alias either side.
func TestStringParamComparedIsCounted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"equality", `if (p == "k") { return 1; } return 0;`},
		{"inequality", `if (p != "k") { return 1; } return 0;`},
		{"ordering", `if (p < "k") { return 1; } return 0;`},
	}
	for _, c := range cases {
		src := "function eat(p: string): i32 { " + c.body + " }\nfunction main(): i32 { return 0; }"
		got := paramCountedFor(t, src, "eat")
		if len(got) != 1 || !got[0] {
			t.Errorf("%s: paramCountedRetain[eat] = %v, want [true] — a comparison reads "+
				"the bytes and yields a bool that aliases neither side", c.name, got)
		}
	}
}

// The direction whose failure mode is a use-after-free: one concat
// occurrence does not launder a retaining one, because everyOccurrenceSafe
// is all-or-nothing.
func TestStringParamConcatAlongsideBareReturnStaysUncredited(t *testing.T) {
	src := `function keep(p: string): string {
    if (p.len() > 3) { return p + "!"; }
    return p;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "keep")
	if len(got) == 1 && got[0] {
		t.Errorf("paramCountedRetain[keep] = %v, but one return hands `p` out bare — "+
			"crediting it lets the caller free a buffer the result points at", got)
	}
}

// The op-level consequence, in the shape that motivated the arm: a
// registry helper whose string parameter is a pure concat operand leaves
// the CALLER's array local reclaimable. The freeing release is
// __fern_drop_arr_str; __fern_rc_dec only decrements, so falling back to
// it strands the whole 3 KB buffer on every generation.
func TestConcatCreditKeepsTheCallersArrayLocalReclaimable(t *testing.T) {
	src := `function put(reg: string[], key: string, flags: string): string[] {
    var b: i32 = key.len() % reg.len();
    return reg.with(b, reg[b] + key + "|" + flags);
}
function build(keys: string[]): string[] {
    var reg: string[] = [];
    var i: i32 = 0;
    while (i < keys.len()) {
        var flags: string = "";
        var f: i32 = 0;
        while (f < 3) { flags = flags + "1"; f = f + 1; }
        reg = put(reg, keys[i], flags);
        i = i + 1;
    }
    return reg;
}
function main(): i32 { return 0; }`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		fn := findFunc(p, "build")
		if n := countCallDirect(fn.Ops, "__fern_drop_arr_str"); n == 0 {
			t.Errorf("ptrW=%d: build emits no __fern_drop_arr_str — the registry local "+
				"fell back to the non-freeing dec; ops:\n%s", ptrW, p)
		}
	}
}
