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

// nativeReuse builds the op stream the native lowering emits for a reuse
// site (emitReuseToken): an OpRcIsUnique gate, a header-subtracting token
// take, an OpRcDec decline, the donor-slot zeroing, and a three-argument
// __alloc_reuse. Each of the three donor roles is a separate parameter so
// a test can make one of them disagree.
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

// selfHostReuse builds the shape the self-host compiler's irlower emits:
// the gate and the release are CALLS rather than dedicated ops, the token
// is the donor pointer with no header arithmetic, the decline arm stores
// the null token before releasing, there is no donor-slot zeroing, and
// __fern_alloc_reuse takes two arguments. None of that changes the
// invariant, so the same checker must model it.
func selfHostReuse(gate, token, dec int32) *Func {
	return &Func{Name: "selfhost", Ops: []Op{
		{Kind: OpLoadLocal, I32: gate},
		{Kind: OpCallDirect, Runtime: true, Str: "__fern_rc_is_unique", I32: 1},
		{Kind: OpStoreLocal, I32: uniq},
		{Kind: OpLoadLocal, I32: uniq},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: token},
		{Kind: OpStoreLocal, I32: tok},
		{Kind: OpElse},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpStoreLocal, I32: tok},
		{Kind: OpLoadLocal, I32: dec},
		{Kind: OpCallDirect, Runtime: true, Str: "__fern_rc_dec", I32: 1},
		{Kind: OpDrop},
		{Kind: OpEnd},
		{Kind: OpLoadLocal, I32: tok},
		{Kind: OpConstI32, I32: 5},
		{Kind: OpCallDirect, Runtime: true, Str: "__fern_alloc_reuse", I32: 2},
	}}
}

func TestVerifyRcAcceptsBothCompilersReuseShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    *Func
	}{
		{"native", nativeReuse(donor, donor, donor)},
		{"self-host", selfHostReuse(donor, donor, donor)},
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
		{"self-host", selfHostReuse(donor, other, donor)},
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
				f := selfHostReuse(donor, donor, donor)
				f.Ops = append(f.Ops[:7:7], f.Ops[13:]...)
				return f
			},
			reason: "token select has no decline arm",
		},
		"the token argument is a constant": {
			build: func() *Func {
				f := nativeReuse(donor, donor, donor)
				f.Ops[18] = Op{Kind: OpConstI32, I32: 0}
				return f
			},
			reason: "token argument is not a local load",
		},
		"the reuse arm stores the token twice": {
			build: func() *Func {
				f := selfHostReuse(donor, donor, donor)
				extra := []Op{{Kind: OpLoadLocal, I32: other}, {Kind: OpStoreLocal, I32: tok}}
				f.Ops = append(f.Ops[:7:7], append(extra, f.Ops[7:]...)...)
				return f
			},
			reason: "reuse arm does not derive the token from one local",
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
