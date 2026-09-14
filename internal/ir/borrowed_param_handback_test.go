package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// `paramCountedRetain` feeds two consumers that ask DIFFERENT questions, and
// conflating them cost the caller its release.
//
//   - countedArgTemp asks whether the caller may dec a fresh TEMP
//     immediately after the call. A bare `return p` is refused there, and
//     deliberately: every hazard the refusal tests name is that path's
//     ("crediting it double-frees the caller's temp").
//   - computeFreeEligible's string-argument taint asks the weaker question —
//     may the caller's own LOCAL keep its scope-exit release? That needs
//     only "the callee retains no UNCOUNTED alias", which a bare `return p`
//     satisfies: `function handout(a: string): string { return a; }` lowers
//     to `local.load; rc.inc; return`, and nothing can cancel that inc,
//     since move-on-return needs an owned rc LOCAL and a parameter is never
//     one.
//
// So there are two summaries. This pins both verdicts on the same function,
// which is what keeps them from collapsing back into one (#9246).
func TestBareReturnSplitsTheTwoRetainSummaries(t *testing.T) {
	src := `function ident(s: string): string {
    if (s.len() == 0) { return s + "x"; }
    return s;
}
function main(): i32 { return ident("ab").len(); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	strict := inferParamCountedRetain(prog, info, nil)["ident"]
	weak := inferParamNoUncountedAlias(prog, info, nil)["ident"]
	if len(strict) != 1 || len(weak) != 1 {
		t.Fatalf("summaries are %v / %v, want one flag each", strict, weak)
	}
	if strict[0] {
		t.Error("paramCountedRetain credits the bare return — the arg-temp reclaim's refusals are pinned on it staying strict")
	}
	if !weak[0] {
		t.Error("paramNoUncountedAlias refuses the bare return — the callee takes the return-transfer inc, so the caller's local may still be released")
	}
}

// The consequence at the caller, in dd's shape: a buffer threaded through a
// helper with a pass-through path keeps its reclaim. While the taint read
// the strict summary, `out` stayed borrow-tainted, the exit sweep's string
// arm (which touches a string local only when it is freeEligible) released
// nothing, and the seed's transfer inc had no partner — one reference per
// call. On `coreutils/dd.fern`'s `out = apply_case(out, tab, s.conv)` that
// was a whole 64 KiB read buffer per record: 40.2 ms against GNU's 3.6 on a
// 64 MiB copy, 37.6 ms of it in system time because every record's buffer
// was a fresh mapping. After, 2.2 ms against 2.7, with `ru_minflt` 158
// against 17,531.
const borrowedParamHandbackSrc = `function ident(s: string): string {
    if (s.len() == 0) { return s + "x"; }
    return s;
}
function conv(data: string): i32 {
    var out: string = data;
    out = ident(out);
    return out.len();
}
function main(): i32 {
    var r: Reader = stdin();
    match (r.read_chunk(8)) {
        Ok(d) => { return conv(d); },
        Err(_) => { return 1; }
    }
}`

func TestBorrowedParamHandbackLeavesTheCallerReclaimable(t *testing.T) {
	dumps := map[string]string{}
	RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { RcPlanHook = nil }()
	p := lowerSourceWith(t, borrowedParamHandbackSrc, 8)
	if !hasPlanName(dumps["conv"], "freeEligible", "out") {
		t.Errorf("out is not freeEligible — the handback taints it, so the seed's transfer inc has nothing to spend it; plan:\n%s", dumps["conv"])
	}
	// The other half of the pairing, so this fails if the callee ever stops
	// taking the inc the credit is premised on.
	if n := countKind(findFunc(p, "ident").Ops, OpRcInc); n != 1 {
		t.Errorf("ident emits %d transfer incs, want 1 — the credit is premised on the callee handing back a counted reference; ops:\n%s", n, p)
	}
}
