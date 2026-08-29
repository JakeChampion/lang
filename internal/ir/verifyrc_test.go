package ir

import (
	"strings"
	"testing"
)

// Slot numbers used by the fixtures below. The donor is the local whose
// box a reuse site takes; uniq holds the is_unique result; tok holds the
// allocation token.
const (
	donor  = int32(1)
	other  = int32(2)
	uniq   = int32(3)
	tok    = int32(4)
	hdrLen = int32(8)
)

// selfHostReuse builds the shape the self-host compiler's irlower actually
// emits, read off a `-dump-fn` of conformance/cases/general_reuse_struct:
//
//	load_local D; call __fern_rc_is_unique/1; tee_local u
//	if   load_local D; store_local t
//	else const_i32 0;  store_local t
//	end
//	load_local t; const_i32 n; call __fern_alloc_reuse/2
//
// Three things differ from native and none of them changes the invariant:
// the gate and release are CALLS rather than dedicated ops, the flag is TEED
// rather than stored and loaded back, and the decline arm holds no release at
// all — this family releases the donor earlier, under a separate condition.
func selfHostReuse(gate, token int32) *Func {
	return &Func{Name: "selfhost", Ops: []Op{
		{Kind: OpLoadLocal, I32: gate},
		{Kind: OpCallDirect, Runtime: true, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpTeeLocal, I32: uniq},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: token},
		{Kind: OpStoreLocal, I32: tok},
		{Kind: OpElse},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: tok},
		{Kind: OpEnd},
		{Kind: OpLoadLocal, I32: tok},
		{Kind: OpConstI32, I32: 3},
		{Kind: OpCallDirect, Runtime: true, Str: "__fern_alloc_reuse", I32: 2},
	}}
}

// nativeReuse builds emitReuseToken's shape: an OpRcIsUnique gate whose flag
// is stored and loaded back (the same slot gates the old-field release
// further down), a header-subtracting token take, an OpRcDec decline, the
// donor-slot zeroing, and a three-argument __alloc_reuse. Each of the three
// donor roles is a separate parameter so a test can make one disagree.
func nativeReuse(gate, token, dec int32) *Func {
	return &Func{Name: "native", Ops: []Op{
		{Kind: OpLoadLocal, I32: gate},
		{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpStoreLocal, I32: uniq},
		{Kind: OpLoadLocal, I32: uniq},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: token},
		{Kind: OpConstI32, I32: hdrLen},
		{Kind: OpSub},
		{Kind: OpStoreLocal, I32: tok},
		{Kind: OpElse},
		{Kind: OpLoadLocal, I32: dec},
		{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
		{Kind: OpDrop},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: tok},
		{Kind: OpEnd},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: gate},
		{Kind: OpLoadLocal, I32: tok},
		{Kind: OpConstI32, I32: 24},
		{Kind: OpConstI32, I32: 24},
		{Kind: OpCallDirect, Runtime: true, Str: "__alloc_reuse", I32: 3},
	}}
}

func TestVerifyRcAcceptsBothCompilersReuseShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    *Func
	}{
		{"native", nativeReuse(donor, donor, donor)},
		{"self-host", selfHostReuse(donor, donor)},
	} {
		problems, cov := verifyRc(tc.f)
		if len(problems) != 0 {
			t.Errorf("%s: a well-formed reuse site must verify clean, got %v", tc.name, problems)
		}
		if cov.Sites != 1 || cov.Checked != 1 {
			t.Errorf("%s: want 1 site checked, got %d of %d (skips %v)",
				tc.name, cov.Checked, cov.Sites, cov.Skipped)
		}
	}
}

func TestVerifyRcCatchesTokenTakenFromAnUntestedDonor(t *testing.T) {
	// The 2026-08-29 first-match hazard: the pairing resolved a donor
	// other than the one whose uniqueness was established, so the site
	// writes over a box that may still have other owners.
	for _, tc := range []struct {
		name string
		f    *Func
	}{
		{"native", nativeReuse(donor, other, donor)},
		{"self-host", selfHostReuse(donor, other)},
	} {
		problems, cov := verifyRc(tc.f)
		if cov.Checked != 1 {
			t.Fatalf("%s: the site must be modelled, not skipped (skips %v)", tc.name, cov.Skipped)
		}
		if len(problems) != 1 {
			t.Fatalf("%s: want one problem, got %d: %v", tc.name, len(problems), problems)
		}
		if !strings.Contains(problems[0].Msg, "never proved unique") {
			t.Errorf("%s: problem must name the unproven box, got %q", tc.name, problems[0].Msg)
		}
	}
}

func TestVerifyRcCatchesDeclineReleasingAnotherLocal(t *testing.T) {
	// The mirror failure: the gate and the token agree, but the shared
	// arm releases something else, so the tested donor leaks whenever the
	// site declines.
	problems, cov := verifyRc(nativeReuse(donor, donor, other))
	if cov.Checked != 1 {
		t.Fatalf("the site must be modelled, not skipped (skips %v)", cov.Skipped)
	}
	if len(problems) != 1 {
		t.Fatalf("want one problem, got %d: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Msg, "leaks on the decline path") {
		t.Errorf("problem must name the leak, got %q", problems[0].Msg)
	}
}

func TestVerifyRcAcceptsADeclineArmWithNoRelease(t *testing.T) {
	// The self-host's struct-reuse family releases the donor earlier under
	// a separate condition, so its decline arm holds only the null token.
	// An absent release is not a defect — there is no third name to
	// disagree — and treating it as one would skip every real site the
	// self-host emits, which is exactly what the corpus sweep caught.
	f := selfHostReuse(donor, donor)
	for _, op := range f.Ops {
		if isRcDecOp(op) {
			t.Fatalf("the self-host fixture must have no decline release; it has one")
		}
	}
	problems, cov := verifyRc(f)
	if len(problems) != 0 || cov.Checked != 1 {
		t.Errorf("want the site checked and clean, got %d checked with %v", cov.Checked, problems)
	}
}

func TestVerifyRcSkipsUnrecognisedShapesRatherThanReporting(t *testing.T) {
	// A verifier that reports a false problem gets switched off, so every
	// shape the pass cannot model must cost coverage and nothing else.
	// The expected reason is pinned per case, so a case cannot pass by
	// being skipped for some unrelated cause further up the recogniser.
	cases := map[string]struct {
		build  func() *Func
		reason string
	}{
		"the is_unique op is gone": {
			build: func() *Func {
				f := nativeReuse(donor, donor, donor)
				f.Ops[1] = Op{Kind: OpDrop}
				return f
			},
			reason: "no uniqueness gate before the token select",
		},
		"the select has no else arm": {
			build: func() *Func {
				f := selfHostReuse(donor, donor)
				f.Ops = append(f.Ops[:6:6], f.Ops[9:]...)
				return f
			},
			reason: "token select has no decline arm",
		},
		"the token argument is a constant": {
			build: func() *Func {
				f := selfHostReuse(donor, donor)
				f.Ops[10] = Op{Kind: OpConstI32, I32: 0}
				return f
			},
			reason: "token argument is not a local load",
		},
		"the reuse arm stores the token twice": {
			build: func() *Func {
				f := selfHostReuse(donor, donor)
				extra := []Op{{Kind: OpLoadLocal, I32: other}, {Kind: OpStoreLocal, I32: tok}}
				f.Ops = append(f.Ops[:6:6], append(extra, f.Ops[6:]...)...)
				return f
			},
			reason: "reuse arm does not derive the token from one local",
		},
		"the decline arm releases twice": {
			build: func() *Func {
				f := nativeReuse(donor, donor, donor)
				extra := []Op{
					{Kind: OpLoadLocal, I32: other},
					{Kind: OpRcDec, Str: "__fern_rc_dec", I32: 1},
					{Kind: OpDrop},
				}
				f.Ops = append(f.Ops[:10:10], append(extra, f.Ops[10:]...)...)
				return f
			},
			reason: "decline arm releases more than one local",
		},
	}
	for name, tc := range cases {
		problems, cov := verifyRc(tc.build())
		if len(problems) != 0 {
			t.Errorf("%s: an unmodelled shape must be skipped, not reported: %v", name, problems)
		}
		if cov.Sites != 1 || cov.Checked != 0 {
			t.Errorf("%s: want the site counted and skipped, got %d checked of %d",
				name, cov.Checked, cov.Sites)
		}
		if got := cov.Skipped[tc.reason]; got != 1 || len(cov.Skipped) != 1 {
			t.Errorf("%s: want the one skip recorded as %q, got %v", name, tc.reason, cov.Skipped)
		}
	}
}

func TestVerifyRcIgnoresFunctionsWithNoReuseSite(t *testing.T) {
	f := &Func{Name: "plain", Ops: []Op{
		{Kind: OpLoadLocal, I32: donor},
		{Kind: OpRcInc, Str: "__fern_rc_inc", I32: 1},
		{Kind: OpDrop},
		{Kind: OpReturnVoid},
	}}
	problems, cov := verifyRc(f)
	if len(problems) != 0 || cov.Sites != 0 {
		t.Errorf("a function with no reuse site must be untouched, got %v / %d sites", problems, cov.Sites)
	}
}
