package ir

import "testing"

// Binding the failure payload does not forfeit the box. An arm's whole use
// of an IoError is a helper turning it into a message, and a helper whose
// parameter the escape oracle clears neither stores it nor hands it back —
// so the pointer is dead by the time the join frees the box, which is the
// property `Err(_)` has syntactically and this one has by proof.
//
// bindingConfinedToArm once asked for a CONCRETE SCALAR result on top of
// the escape oracle — the arg-temp reclaim's requirement, since that path
// decs a fresh temp right after the call, and not a confinement's. A
// helper returning a string failed it, the arm was refused, and the whole
// match declined its release: 32 bytes per I/O call in every utility that
// reports an errno (#9245). The confinement now asks readOnlyCallArg,
// where the escape oracle answers by itself — paramEscapesInFn counts a
// returned parameter as escaping, so a parameter it clears is one the
// callee neither stored nor returned, whatever the result's shape.
//
// `handsBack` is the other side of that line and must still be refused:
// its helper returns the payload, so the returned string and the box the
// join would free are the same storage.
const matchFailurePayloadSrc = `function etext(e: IoError): string {
    match (e) {
        NotFound(_) => { return "no such file"; },
        _ => { return "other"; }
    }
}
function keeps(e: IoError): string {
    match (e) {
        Other(_, msg) => { return msg; },
        _ => { return "other"; }
    }
}
function reads(w: Writer, s: string): i32 {
    match (w.write_some(s)) {
        Err(e) => { eprint(etext(e)); return 1; },
        Ok(_) => {}
    }
    return 0;
}
function ignores(w: Writer, s: string): i32 {
    match (w.write_some(s)) {
        Err(_) => { return 1; },
        Ok(_) => {}
    }
    return 0;
}
function handsBack(w: Writer, s: string): i32 {
    match (w.write_some(s)) {
        Err(e) => { eprint(keeps(e)); return 1; },
        Ok(_) => {}
    }
    return 0;
}
function main(): i32 { return 0; }`

func TestMatchFailurePayloadHandedToHelperStillFreesBox(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, matchFailurePayloadSrc, ptrW)
		// `ignores` is the reference: `Err(_)` extracts nothing, so its
		// count is what a confined arm is worth — one release at the join
		// and one on the arm's own `return`.
		want := enumBoxReleaseCount(findFunc(p, "ignores"))
		if want == 0 {
			t.Fatalf("ptrW=%d: the Err(_) reference emits no release at all, so this test proves nothing; ops:\n%s", ptrW, p)
		}
		if got := enumBoxReleaseCount(findFunc(p, "reads")); got != want {
			t.Errorf("ptrW=%d: reads emits %d scrutinee-box releases, want %d — binding the payload and handing it to a non-escaping helper forfeited the box; ops:\n%s",
				ptrW, got, want, p)
		}
		if got := enumBoxReleaseCount(findFunc(p, "handsBack")); got != 0 {
			t.Errorf("ptrW=%d: handsBack emits %d scrutinee-box releases, want 0 — its helper RETURNS the payload, so the returned string and the freed box are the same storage",
				ptrW, got)
		}
	}
}
