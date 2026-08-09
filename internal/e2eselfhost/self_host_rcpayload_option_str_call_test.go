package e2eselfhost

import "testing"

// A STRING-payload `Option`/`Result` local bound from a CALL is reclaimed
// (#6360, the last row of the call-init class).
//
// #6416 closed the scalar payload, #6451 the rc payload consumed by a match,
// #6448 the no-match local, #6463 the string `Err`. What none of them reached
// is the string in the SUCCESS position — `Option[string]` from a producer —
// which stayed at `frees=0` and scaled exactly ×2 per doubling (25600 at 100
// rounds, 51200 at 200).
//
// WHY THIS ROW NEEDED A FRESHNESS PROOF AND THE ARRAY ROW DID NOT. The direct
// path admits a string payload only when the constructor's ARGUMENT is fresh
// (`str_local_binding_is_fresh`) — a bare ident would alias a live box that the
// free then kills. A call init cannot see the argument at all, so admission
// leans on the producer-level verdict instead: the OPTFRESH registry's `"f"`
// flag, which `body_has_nonfresh_opt_success_payload` computes over every
// return. It is seeded into `reclaimable_names` under its own `OPTFRESHF:`
// prefix because the older `OPTFRESH:` seeding keeps only the bare name.
//
// The array rows needed no such gate, and that asymmetry is measured rather
// than assumed: an aliased ARRAY payload through a producer is already balanced
// (the constructor takes a counted reference — verified in #6451's own probe,
// `allocs=51 frees=51` with the source array intact). Strings are the shape
// where that does not hold, which is why the direct path gates them and why
// this row is the one that needed the registry.
//
// The negative case below is the load-bearing one: an aliased string must NOT
// be freed. It asserts agreement with native rather than zero bytes, because
// the correct behaviour there is to LEAK — both compilers decline to free it.

// rcPayloadOptionStrCallSrc: `Option[string]` from a producer whose every
// return constructs a fresh payload, so the `"f"` flag is earned.
const rcPayloadOptionStrCallSrc = `import "core/int";

function make(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("ab" + "cd");
}

function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[string] = make(k);
            match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// rcPayloadOptionStrLitSrc is the same shape with a LITERAL payload. Not
// redundant: the concat form allocates temporaries that other machinery frees,
// which is exactly what once masked this leak as "partial reclaim" (800 of
// 2000) and sent the diagnosis after a second, non-existent defect. The literal
// form has no such traffic, so its `frees` count is unambiguous.
const rcPayloadOptionStrLitSrc = `import "core/int";

function make(i: i32): Option[string] {
    if (i < 0) { return None; }
    return Some("abcd");
}

function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var k: i32 = 0;
        while (k < 4) {
            var v: Option[string] = make(k);
            match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// rcPayloadStrAliasSrc: the producer's payload is a PARAMETER, so the string in
// the box aliases a live one. `body_has_nonfresh_opt_success_payload` must
// withhold the `"f"` flag and the local must go unreclaimed. Freeing here would
// be a use-after-free on `shared`, which the return value would expose.
const rcPayloadStrAliasSrc = `import "core/int";

function mk(s: string): Option[string] {
    if (s.len() == 0) { return None; }
    return Some(s);
}

function main(): i32 {
    var shared: string = "hello" + "!";
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 50) {
        var v: Option[string] = mk(shared);
        match (v) { Some(a) => { acc = acc + a.len(); }, None => { acc = acc + 1; } }
        r = r + 1;
    }
    return (acc % 5) + shared.len();
}
`

func TestSelfHostRcPayloadOptionStrFromCallX86_64(t *testing.T) {
	_, frees, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadOptionStrCallSrc, 4)
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — an Option[string] bound from a "+
			"producer must be reclaimed; this leaked 25600 with frees=0 before "+
			"#6360's last row, doubling with the loop count", summary, live)
	}
	if frees == 0 {
		t.Errorf("%s: frees=0 — the box is never released at all", summary)
	}
}

func TestSelfHostRcPayloadOptionStrLiteralFromCallX86_64(t *testing.T) {
	_, frees, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadOptionStrLitSrc, 4)
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — the literal-payload form allocates "+
			"no temporaries, so this count is the unambiguous one", summary, live)
	}
	if frees == 0 {
		t.Errorf("%s: frees=0", summary)
	}
}

// The negative. An aliased string payload must be REFUSED, so the correct
// outcome is a leak — asserted as agreement with native, which declines to free
// it too. A zero here would mean the freshness gate had been bypassed and a
// live string freed.
func TestSelfHostRcPayloadStrAliasNotFreedX86_64(t *testing.T) {
	_, frees, live, summary := runRcPayloadOptionCallProbe(t, rcPayloadStrAliasSrc, 6)
	if live == 0 {
		t.Errorf("%s: live_bytes=0 — an ALIASED string payload was freed. The "+
			"producer returns Some(param), so the box does not own the string; "+
			"freeing it is a use-after-free on the caller's value. Admission "+
			"must withhold the OPTFRESH \"f\" flag for this shape", summary)
	}
	if frees > 2 {
		t.Errorf("%s: frees=%d — more releases than the handful the harness "+
			"itself makes; the aliased payload is being reclaimed when it must "+
			"not be", summary, frees)
	}
	// The exit code (6) is the real use-after-free detector: it reads
	// shared.len() after the loop, so a freed buffer shows up as a wrong answer
	// rather than as a byte count. runRcPayloadOptionCallProbe already asserts it.
}
