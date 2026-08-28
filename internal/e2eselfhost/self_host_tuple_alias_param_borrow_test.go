package e2eselfhost

import (
	"testing"
)

// --- Tuple alias_param: the TUPB payload tier forgives a non-escaping alias --
//
// The tuple flavor of the aliased-param borrow carve-out
// (self_host_aliased_param_borrow_test.go). The box tier learned to forgive a
// callee's non-escaping `var q = p` bind; the TUPB payload tier — which a
// caller's TUPRCS deep free must consult, because the box flag alone cannot
// see a handed-out element — still fed its escape walker a hardwired-empty
// alias list, so `var x: (i32, i32[]) = src` read as a bare-ident payload
// escape and every caller's keep-sweep was refused: a constant leak per
// caller, measured 2/0 on both alias_param matrix cells.
//
// tuple_param_alias_sites collects the forgivable binds under the payload
// tier's registry-independence rule (empty registry, so the interproc
// fixpoint cannot oscillate) and with one proof the box tier does not need:
// the alias must be payload-safe IN ITS OWN RIGHT, not just body_unsafe_for
// safe. `var x = src; var e = x.1;` extracts an element one alias hop
// removed — the indirect form of the sanitizer-confirmed use-after-free the
// tuple_mixed__elemret__payload_refused matrix row pins — and the last two
// rows here hold both refusals in place.
//
// Wants confirmed against the native x86-64 backend: answers match on every
// row (native's counts differ — it frees the negatives via dup-at-extract).
// Exit 99 is reserved for __rc_underflow_count().

type tupleAliasParamCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func tupleAliasParamCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The control: the param read directly, no alias — the borrowed_arg shape
			// that was already clean, establishing the other rows are about the ALIAS.
			name: "params_read_directly",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    return (src.0 + src.1.len() + i - i) % 101;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 43, allocs: 2, frees: 2,
		},
		{
			// THE FIX ROW (fnscope): the callee only aliases its param, so the TUPB
			// payload tier now forgives the bind and main's keep takes its TUPRCS
			// deep free. Was 2/0 (constant keep-sweep leak) before the port.
			name: "param_aliased_to_local",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var t: i32 = 0;
    var x: (i32, i32[]) = src;
    t = (t + x.0 + x.1.len()) % 101;
    return t;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 43, allocs: 2, frees: 2,
		},
		{
			// The block-scoped sibling — the alias forgiveness must survive the
			// recursion into nested bodies (the trap rctuple_esc_stmt_alias's own
			// header records). Was 2/0 before the port.
			name: "param_alias_block_scoped",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) {
        var x: (i32, i32[]) = src;
        t = (t + x.0 + x.1.len()) % 101;
        t = t + 1;
    }
    return t;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 75, allocs: 2, frees: 2,
		},
		{
			// THE SOUNDNESS ROW. The caller reads its own tuple back after the call
			// with three fresh allocations in between, so a box the callee freed
			// would be reused before the read. A changed exit here means the
			// forgiveness let the callee release a box its caller still owns.
			name: "caller_reads_back_after_churn",
			src: `function consume(src: (i32, i32[])): i32 {
    var x: (i32, i32[]) = src;
    return x.0 + x.1.len();
}
function round(i: i32): i32 {
    var keep: (i32, i32[]) = (5, [6, 7, 8]);
    var n: i32 = consume(keep);
    var j1: i32[] = [1, 2];
    var j2: i32[] = [3, 4];
    var j3: i32[] = [5, 6];
    return n + keep.0 * 3 + keep.1.len() + keep.1[0]
        + j1.len() - j1.len() + j2.len() - j2.len() + j3.len() - j3.len();
}
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 58, allocs: 100, frees: 100,
		},
		{
			// NEGATIVE: the alias escapes by returning the WHOLE tuple, so the param
			// really is handed out and TUPB must stay refused — the caller keeps
			// leaking, the safe direction. 2/2 here means the payload tier admitted
			// an escaping alias.
			name: "escaping_alias_keeps_refused",
			src: `function grab(src: (i32, i32[]), i: i32): (i32, i32[]) {
    var x: (i32, i32[]) = src;
    return x;
}
function round(src: (i32, i32[]), i: i32): i32 {
    var r: (i32, i32[]) = grab(src, i);
    return (r.0 + r.1.len() + i - i) % 101;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 43, allocs: 2, frees: 0,
		},
		{
			// NEGATIVE, the UAF fence: an ELEMENT extracted through the alias
			// (`var e = x.1`) hands out a payload reference one alias hop removed —
			// the indirect form of the grant the elemret matrix row pins as a
			// sanitizer-confirmed use-after-free. The alias-site collector runs the
			// payload scan on the alias itself, which is what keeps this refused.
			name: "alias_elem_extract_refused",
			src: `function round(src: (i32, i32[]), i: i32): i32 {
    var x: (i32, i32[]) = src;
    var e: i32[] = x.1;
    return (e.len() + i - i) % 101;
}
function main(): i32 {
    var keep: (i32, i32[]) = (5, [6, 7]);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = (acc + keep.0 + keep.1.len()) % 83;
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 41, allocs: 2, frees: 0,
		},
	}
}

// TestSelfHostTupleAliasParamBorrowX86_64 is the leak-accounting leg.
func TestSelfHostTupleAliasParamBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleAliasParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tapb_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; a CHANGED value on the "+
					"churn row means the callee freed a box its caller still owns)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER on a fix row means the aliased "+
					"param's payload tier stopped granting and the keep-sweep leak is back; "+
					"MORE on a negative row means the tier admitted an escaping alias",
					tc.name, summary, tc.frees)
			}
		})
	}
}
