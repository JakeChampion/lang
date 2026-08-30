package ir

import "testing"

func TestRcHelperSigReadsTheRuntimeTable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		operand int
		effect  RcEffect
	}{
		{"__fern_rc_inc", 0, RcRetain},
		{"__fern_str_inc", 0, RcRetain},
		{"__fern_rc_dec", 0, RcRelease},
		{"__fern_arr_dec", 0, RcRelease},
		{"__fern_box_free", 0, RcRelease},
		{"__free", 0, RcRelease},
		{"__fern_rc_is_unique", 0, RcInspect},
		{"__fern_arr_cow_inplace", 0, RcMove},
		{"__fern_str_append", 0, RcMove},
	} {
		got, ok := RcHelperSig(tc.name)
		if !ok {
			t.Errorf("%s: no signature", tc.name)
			continue
		}
		if got.Operand != tc.operand || got.Effect != tc.effect {
			t.Errorf("%s: got operand %d effect %v, want %d %v",
				tc.name, got.Operand, got.Effect, tc.operand, tc.effect)
		}
	}
}

func TestRcHelperSigMatchesTheGeneratedDropFamilies(t *testing.T) {
	// One name per family lowering can emit, spelled as it appears in a
	// lowered corpus program.
	for _, name := range []string{
		"__drop_struct_Point",
		"__drop_struct_flat_Point",
		"__drop_enum_JsonValue",
		"__drop_tuple_str_i32",
		"__drop_dyn_Shape",
		"__drop_arr_struct_Point",
		"__drop_arr_tuple_str_i32",
		"__drop_arr_enum_JsonValue",
		"__drop_arr_dyn_Shape",
		"__drop_arr_arr_4",
		"__drop_arr_arr_str",
		"__drop_arr_of___drop_enum_JsonValue",
		"__drop_arr_closure",
		"__drop_closure_value",
		"__drop_map_str_keys",
		"__map_drop_values",
	} {
		got, ok := RcHelperSig(name)
		if !ok {
			t.Errorf("%s: a generated drop must be recognised", name)
			continue
		}
		if got.Operand != 0 || got.Effect != RcRelease {
			t.Errorf("%s: got operand %d effect %v, want 0 RcRelease",
				name, got.Operand, got.Effect)
		}
	}
}

// Every name here was found in the corpus or the stdlib while the
// helper set was being measured, and every one of them is matched by
// some plausible substring rule over "dec" / "drop" / "free" / "inc".
// Calling any of them a release would be a wrong answer about a real
// program, so each is pinned as a name the table must NOT claim.
func TestRcHelperSigRejectsNamesASubstringRuleWouldCatch(t *testing.T) {
	for _, name := range []string{
		// std/async.fern's own function: three parameters, releases
		// none of them. The reason __drop_ is not a prefix on its own.
		"__drop_losers",
		// User functions from the corpus.
		"mk_free", "map_inc", "hex_decode", "url_decode", "is_non_decreasing",
		// A user `Drop` impl can carry any name userDropFnName
		// resolves to, and a user method called foo_drop mangles to
		// the same suffix. Both are defined functions, so their
		// ownership is the interprocedural fixpoint's answer.
		"__method_string_drop", "__method_Point_foo_drop",
		// Helpers that do move counts in a shape one operand effect
		// cannot express. They must report nothing rather than a
		// guess.
		"__fern_arr_push_grow", "__fern_arr_push_grow_move_ptr", "__alloc_reuse",
		// Classified as moving no count at all.
		"__str_concat", "__fern_str_copy", "__fern_alloc_rc1",
	} {
		if sig, ok := RcHelperSig(name); ok {
			t.Errorf("%s: must have no signature, got %+v", name, sig)
		}
	}
}

// A prefix with nothing after it is not a generated drop: the suffix is
// the type it drops.
func TestRcHelperSigRejectsABareFamilyPrefix(t *testing.T) {
	for _, name := range []string{"__drop_struct_", "__drop_enum_", "__drop_arr_of_"} {
		if _, ok := RcHelperSig(name); ok {
			t.Errorf("%s: a bare prefix names no type and is not a drop", name)
		}
	}
}

func TestRcReleasesAndRcRetainsSplitTheTable(t *testing.T) {
	// A move gives up the operand's unit, so it releases.
	for _, name := range []string{"__fern_rc_dec", "__fern_arr_cow_inplace", "__drop_struct_Point"} {
		if _, ok := RcReleases(name); !ok {
			t.Errorf("%s: must count as a release", name)
		}
		if _, ok := RcRetains(name); ok {
			t.Errorf("%s: must not count as a retain", name)
		}
	}
	for _, name := range []string{"__fern_rc_inc", "__fern_str_inc"} {
		if _, ok := RcRetains(name); !ok {
			t.Errorf("%s: must count as a retain", name)
		}
		if _, ok := RcReleases(name); ok {
			t.Errorf("%s: must not count as a release", name)
		}
	}
	// Inspection transfers nothing in either direction.
	if _, ok := RcReleases("__fern_rc_is_unique"); ok {
		t.Error("__fern_rc_is_unique reads the count; it releases nothing")
	}
	if _, ok := RcRetains("__fern_rc_is_unique"); ok {
		t.Error("__fern_rc_is_unique reads the count; it retains nothing")
	}
}
