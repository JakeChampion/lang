package ir

import "testing"

// Binding the failure payload does not forfeit the box. An arm's whole use
// of an IoError is a helper turning it into a message, and a helper whose
// parameter the escape oracle clears neither stores it nor hands it back —
// so the pointer is dead by the time the join frees the box, which is the
// property `Err(_)` has syntactically and this one has by proof.
//
// bindingConfinedToArm used to ask for a CONCRETE SCALAR result on top —
// the arg-temp reclaim's requirement, since that path decs a fresh temp
// right after the call, and not a confinement's. A
// helper returning a string failed it, the arm was refused, and the whole
// match declined its release: 32 bytes per I/O call in every utility that
// reports an errno (#9245). The confinement now asks readOnlyCallArg,
// where the escape oracle answers by itself — paramEscapesInFn counts a
// returned parameter as escaping, so a parameter it clears is one the
// callee neither stored nor returned, whatever the result's shape.
//
// `handsBack` sits on the other side of the escape oracle's line and is
// released all the same: its helper returns the payload, so the oracle
// counts the parameter as escaping and the callee owns it by default. The
// call site then retains the binding for the callee's exit release, and
// the returned string takes its own transfer count, so the box the join
// frees holds nothing the result still reads (ownedCallArgRetained). It
// used to be refused on the reasoning that the string and the box were one
// storage; measured, that refusal leaked the box, the variant and the
// string on every failure (`rc_request_path_leaks_test.go`,
// failure-payload-handed-to-a-helper-that-returns-it).
const matchFailurePayloadSrc = `function etext(e: IoError): string {
    match (e) {
        NotFound(_) => { return "no such file"; },
        _ => { return "other"; }
    }
}
function keeps(e: IoError): string {
    match (e) {
        Other(_, msg, _) => { return msg; },
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
		if got := enumBoxReleaseCount(findFunc(p, "handsBack")); got != want {
			t.Errorf("ptrW=%d: handsBack emits %d scrutinee-box releases, want %d — its helper owns the payload and the call retains it, so the box is still the arm's to free; ops:\n%s",
				ptrW, got, want, p)
		}
	}
}
