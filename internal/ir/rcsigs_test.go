package ir

import (
	"sort"
	"testing"
)

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
		if len(got.Args) != 1 {
			t.Errorf("%s: want one counted argument, got %d", tc.name, len(got.Args))
			continue
		}
		if got.Args[0].Index != tc.operand || got.Args[0].Effect != tc.effect {
			t.Errorf("%s: got operand %d effect %v, want %d %v",
				tc.name, got.Args[0].Index, got.Args[0].Effect, tc.operand, tc.effect)
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
		// The one member whose name does not start __drop_, which is
		// how it came to be missing from the list. `elide.go` rewrites
		// some calls to it into the generic `__fern_closure_drop`, so
		// the identical release was understood under one spelling and
		// invisible under the other.
		"__closure_drop___closure_lambda_1",
	} {
		got, ok := RcHelperSig(name)
		if !ok {
			t.Errorf("%s: a generated drop must be recognised", name)
			continue
		}
		if len(got.Args) != 1 || got.Args[0].Index != 0 || got.Args[0].Effect != RcRelease {
			t.Errorf("%s: want one counted argument 0 released, got %+v", name, got.Args)
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
		// The bare prefix names no closure, and the generic runtime
		// helper is a different name that rcRuntimeSigs already holds.
		"__closure_drop_",
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

// Every callee the IR does not define has to carry a reference-count
// classification, not just the runtime helpers.
//
// `providedSigs` is the verifier's record of exactly those callees, and
// it covers the BUILTINS as well — `strbuf_append`, the Map methods,
// the platform surface. A builtin that moves a count and is absent from
// this file reads to `internal/ssa` as an opaque callee that borrows,
// which is the unsafe direction: a callee that really consumes comes
// back Borrowed and nothing says so.
//
// This is the sibling of TestRcSigsCoverEveryRuntimeHelper in
// internal/codegen/wasmbin, which does the same against the wasm
// registry. Two registries, one classification, and a new name in
// either fails until someone decides which bucket it belongs in.
func TestEveryProvidedCalleeHasAnRcClassification(t *testing.T) {
	var missing []string
	for name := range providedSigs {
		if !RcHelperClassified(name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d callee(s) in providedSigs have no reference-count classification — "+
			"add each to rcRuntimeSigs, rcBuiltinSigs, rcInertBuiltins, rcInert or "+
			"rcUnmodelled, or give it a __fern_ rename the alias rule can follow: %v",
			len(missing), missing)
	}
	if len(providedSigs) == 0 {
		t.Fatal("providedSigs is empty — nothing was checked")
	}
	t.Logf("%d provided callees, %d unclassified", len(providedSigs), len(missing))
}

// The rename rule is what keeps the builtin half small, so it has to
// actually fire — and only on an exact match.
func TestBuiltinRuntimeAliasFollowsTheRenameAndOnlyOnAnExactMatch(t *testing.T) {
	for _, tc := range []struct{ builtin, want string }{
		{"strbuf_append", ""}, // classified directly; no __fern_strbuf_append helper
		{"read_file", "__fern_read_file"},
		{"exit", "__fern_exit"},
		{"__alloc", "__fern_alloc"},
		{"__memchr", "__fern_memchr"},
	} {
		got, ok := builtinRuntimeAlias(tc.builtin)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: must not resolve, got %q", tc.builtin, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: got %q (%v), want %q", tc.builtin, got, ok, tc.want)
		}
	}
	// A name that already IS a runtime helper must not be re-aliased
	// into __fern___fern_x.
	if _, ok := builtinRuntimeAlias("__fern_rc_dec"); ok {
		t.Error("a runtime helper must not resolve through the builtin rename rule")
	}
	// string_from_bytes_unchecked lowers to __fern_string_from_bytes —
	// the names do not correspond, so the rule must decline rather than
	// guess.
	if _, ok := builtinRuntimeAlias("string_from_bytes_unchecked"); ok {
		t.Error("the rule must decline a builtin whose runtime target has a different name")
	}
}

// The builtins that take a unit, and the receiver that does not.
func TestBuiltinContainerMutatorsConsumeWhatTheyStore(t *testing.T) {
	sig, ok := RcHelperSig("__method_Map_set")
	if !ok {
		t.Fatal("__method_Map_set: no signature")
	}
	if len(sig.Args) != 2 {
		t.Fatalf("__method_Map_set consumes its key AND its value, got %d counted args", len(sig.Args))
	}
	for _, i := range []int{1, 2} {
		a, ok := sig.Arg(i)
		if !ok || a.Effect != RcRelease {
			t.Errorf("__method_Map_set argument %d must be consumed, got %+v (%v)", i, a, ok)
		}
	}
	// The receiver is borrowed: `m = m.set(k, v)` reassigns, so the
	// caller's own overwrite releases the old handle.
	if _, ok := sig.Arg(0); ok {
		t.Error("__method_Map_set's receiver is borrowed; the caller's reassignment releases it")
	}

	for _, tc := range []struct {
		name  string
		index int
	}{
		{"__method_Array_push", 1},
		{"__method_Array_set", 2},
	} {
		sig, ok := RcHelperSig(tc.name)
		if !ok {
			t.Errorf("%s: no signature", tc.name)
			continue
		}
		a, found := sig.Arg(tc.index)
		if !found || a.Effect != RcRelease {
			t.Errorf("%s: argument %d must be consumed, got %+v", tc.name, tc.index, sig.Args)
		}
		if _, ok := sig.Arg(0); ok {
			t.Errorf("%s: the receiver is borrowed", tc.name)
		}
	}
}
