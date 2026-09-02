package arm64ssa

import "testing"

// A call site saves the values the allocator says are live across it. Which of
// those the callee can actually disturb is a property of the callee, and for
// this file's own runtime helpers it is knowable: the rc primitives touch x0 and
// x1 and nothing else. helperClobbers derives that from the emitted body, so the
// derivation is what these tests pin — a set that is too small does not fail
// loudly, it corrupts a caller's live value.

// The fixed point has to close over branch targets, tail calls included:
// __fern_closure_drop ends in `b __fern_box_free` / `b __fern_rc_dec`, so
// whatever those two disturb, a call to closure_drop disturbs.
func TestHelperClobbersCoverTailCallTargets(t *testing.T) {
	sets := helperClobbers()
	drop, ok := sets[fnLabel("__fern_closure_drop")]
	if !ok {
		t.Fatal("__fern_closure_drop has no clobber set")
	}
	for _, callee := range []string{"__fern_box_free", "__fern_rc_dec"} {
		for r, c := range sets[fnLabel(callee)] {
			if c && !drop[r] {
				t.Errorf("closure_drop tail-calls %s, which clobbers %s, but its own set omits it",
					callee, armX[r])
			}
		}
	}
}

// A helper that branches into compiled code cannot be reasoned about from its
// own body: the callee is an ordinary function using the whole register file.
// read_file calls __fern_utf8_valid, which is written in Fern (internal/fernrt)
// and lifted into the module like any other function.
func TestHelperCallingCompiledCodeClobbersEverything(t *testing.T) {
	asm := renderHelper(runtimeHelperEmitters["read_file"])
	var compiled string
	for _, target := range helperBranchTargets(asm) {
		if _, known := helperClobbers()[target]; !known {
			compiled = target
			break
		}
	}
	if compiled == "" {
		t.Fatal("read_file no longer branches into compiled code — pick another helper that does")
	}
	for r, c := range helperClobbers()[fnLabel("read_file")] {
		if !c {
			t.Errorf("read_file reaches %s yet its set spares %s", compiled, armX[r])
		}
	}
}

// The rc primitives are the ones the narrowing is for: their bodies name x0 and
// x1 (x2/x3 in the over-release counter) and nothing else, so a value homed
// above those survives a call to one untouched.
func TestRcPrimitivesClobberOnlyTheirScratch(t *testing.T) {
	for _, name := range []string{"__fern_rc_inc", "__fern_rc_is_unique"} {
		for r, c := range helperClobbers()[fnLabel(name)] {
			if c && r > 1 {
				t.Errorf("%s is recorded as clobbering %s", name, armX[r])
			}
		}
	}
}

// A register-held branch target has no body to read, so the helper holding one
// must fall back to the full set rather than to whatever its own text mentions.
func TestIndirectBranchClobbersEverything(t *testing.T) {
	if !branchesIndirectly("\tblr x9\n") || !branchesIndirectly("\tbr x16\n") {
		t.Error("an indirect branch is not recognised as one")
	}
	if branchesIndirectly("\tbl fn_x\n\tb .Lend\n") {
		t.Error("a direct branch is read as indirect")
	}
}
