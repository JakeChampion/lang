package ir

import "testing"

// #7867 slice 4: an array parameter whose element reads provably retain
// nothing is counted-retain, so the caller may release the array it
// passed. The blanket Index refusal predates the per-shape argument;
// the three credited shapes each carry one, and everything else still
// refuses. The runtime consequence of the blanket refusal was #7914's
// projection leak: `filter_gate(check_module(...).diags)` stranded
// 608 B per call because the callee indexed its parameter.

func TestArrayParamScalarElementReadIsCounted(t *testing.T) {
	src := `function sum(f: i32[]): i32 {
    var s: i32 = 0;
    var k: i32 = 0;
    while (k < f.len()) { s = s + f[k]; k = k + 1; }
    return s;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "sum")
	if len(got) != 1 || !got[0] {
		t.Errorf("paramCountedRetain[sum] = %v, want [true] — a scalar element read "+
			"is a value copy and references nothing", got)
	}
}

func TestArrayParamScalarFieldProjectionIsCounted(t *testing.T) {
	src := `struct Diag { msg: string, line: i32 }
function count_even(ds: Diag[]): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < ds.len()) { if (ds[i].line % 2 == 0) { n = n + 1; } i = i + 1; }
    return n;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "count_even")
	if len(got) != 1 || !got[0] {
		t.Errorf("paramCountedRetain[count_even] = %v, want [true] — ds[i].line copies "+
			"a scalar out and the element reference dies inside the expression", got)
	}
}

func TestArrayParamElementPushedIsCounted(t *testing.T) {
	src := `struct Diag { msg: string, line: i32 }
function gate(ds: Diag[]): Diag[] {
    var out: Diag[] = [];
    var i: i32 = 0;
    while (i < ds.len()) {
        if (ds[i].line % 2 == 0) { out = out.append(ds[i]); }
        i = i + 1;
    }
    return out;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "gate")
	if len(got) != 1 || !got[0] {
		t.Errorf("paramCountedRetain[gate] = %v, want [true] — the push emits the "+
			"element retain unconditionally, so the container's reference is counted "+
			"(the slice-1 argument, one read deeper)", got)
	}
}

// The refusals: an element reference that leaves the expression
// uncounted keeps the whole parameter refused, or the caller frees a
// buffer whose element someone still holds.
func TestArrayParamEscapingElementReadsStayUncredited(t *testing.T) {
	cases := []struct{ name, src string }{
		{"pointer field projected out", `struct Diag { msg: string, line: i32 }
function keep(ds: Diag[]): string { return ds[0].msg; }`},
		{"element returned bare", `struct Diag { msg: string, line: i32 }
function keep(ds: Diag[]): Diag { return ds[0]; }`},
		{"element bound to a local", `struct Diag { msg: string, line: i32 }
function keep(ds: Diag[]): i32 { var d: Diag = ds[0]; return d.line; }`},
	}
	for _, c := range cases {
		got := paramCountedFor(t, c.src+"\nfunction main(): i32 { return 0; }", "keep")
		if len(got) == 1 && got[0] {
			t.Errorf("%s: paramCountedRetain[keep] = [true], but the read hands out "+
				"a reference nothing counts — crediting it frees a live element", c.name)
		}
	}
}
