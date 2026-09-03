package e2e

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The rc corpus's LEAK leg (#7790).
//
// rc_correctness_test.go runs every corpus case for its exit code, which
// catches an over-release: `__rc_underflow_count()` is folded into the
// result, so a stray dec shows up as a non-zero exit. Its own header
// records what that cannot see:
//
//	Drop handlers that only LEAK (closures, maps, generic enums, deep
//	nesting) are still exercised here — leaks don't bump the underflow
//	counter, so they read 0 too.
//
// So the corpus already runs the shapes that leak and is structurally
// blind to the leaking. `FERN_LEAKCHECK=1` has reported the live-byte
// balance at exit since #5362 and nothing consumed it; this leg does.
//
// # Why a pinned baseline rather than a flat zero
//
// 40 of 216 cases leak on x86-64 today and 47 on arm64 — the map and
// closure drop paths do not fully reclaim, which is the same list the
// corpus header names. A flat zero assertion could not land without
// fixing all of that first, and deleting the leg until then is how the
// direction stays unwatched for another year.
//
// So each leaking case is pinned at its exact byte count and everything
// else must be zero. What that buys, which nothing had before:
//
//   - the 176 (x86-64) / 169 (arm64) clean cases are now GATED. A change
//     that starts leaking in any of them fails here.
//   - a new corpus case that leaks fails, because absent from the table
//     means zero. Joining the leaking set is a deliberate act.
//   - a pinned case that leaks MORE fails.
//   - a pinned case that leaks LESS fails too, asking for the number to
//     be banked, so a fix cannot silently leave the table stale. Same
//     discipline as the complexity ratchet in internal/lint.
//
// Deliberately NOT a total byte budget: one number over the whole corpus
// hides a new leak behind a fixed one, and the point of the leg is that
// leaks stop being invisible.
//
// The two backends are pinned separately because they genuinely differ,
// and the difference is itself a finding rather than noise — see
// docs/rc-log/2026-08-29-arm64-string-map-leak-divergence.md.
var rcCorpusLeakBaselineX86_64 = map[string]int64{
	"cell_string_read_aliased": 32,
	// Deliberate refusal pins, not regressions: a fresh temp handed to a
	// REFUSED parameter has no owner left to free it — the residual class
	// #7867 tracks. The pushed-then-returned-bare case watches the #7914
	// push credit's refusal half; the own-string case is a pre-existing
	// gap in the own machinery (identical bytes with the credit removed),
	// single-word only.
	"copying_builtin_own_param_not_double_freed":     128,
	"string_pushed_then_returned_bare_stays_refused": 320,
	"closure_array_capture_churn":                    4752,
	"closure_call_arg_handed_back_is_not_reclaimed":  1920,
	"closure_captures_arr_of_struct_churn_free":      14256,
	"closure_captures_struct_churn_free":             6336,
	"closure_churn_free":                             1584,
	"closure_escapes_return":                         16,
	"closure_capture_passed_to_owned_param":          64,
	"map_delete_tuple_churn_free":                    304000,
	"map_iter_escape_churn_free":                     32000,
	"map_iter_string_kv_retain_churn_free":           19200,
	"matchexpr_alias_array_no_free":                  1600,
	"option_of_array":                                32,
	"pair_form_enum_temp_as_argument":                288,
	"pair_form_payload_borrowing_call":               144,
	"stdlib_json_cursor_idiom":                       1488,
	"stdlib_json_roundtrip":                          640,
	"string_closure_capture_aliased":                 16,
	// A closure LOCAL handed to a callee keeps its pair, and the exit
	// sweep's per-closure thunk is downgraded to the pair-only release
	// (ElideClosurePair). Routing it through the pair's drop-fn pointer
	// instead freed a Scope's closure field under the self-host checker
	// (docs/rc-log/2026-09-02-persistent-collections-residual-leaks.md),
	// so the shape is pinned rather than fixed: pair + env per call.
	"closure_local_passed_to_callee_released": 384,
	"string_closure_capture_churn_free":       3200,
	"tuple_return_scalar_cursor_recursion":    320,
	// The hand-back half of the guarded arg-temp release: the callee
	// returned the temp unchanged, so the guard declined the drop and the
	// result's own reference keeps rhsTainted's conservative call-result
	// taint. 256 B before the release landed.
	"consumed_array_arg_temp_released_and_guarded": 128,
}

var rcCorpusLeakBaselineArm64 = map[string]int64{
	// See the x86-64 twin — the pushed-then-returned-bare pin is the same
	// deliberate refusal class; the own-string case is clean here (the
	// two-word ABI reclaims it).
	"string_pushed_then_returned_bare_stays_refused": 448,
	// Pre-existing arm64 gaps the x86-64 twin does not have (both 0
	// there). The #7914 concat credit is inert on this backend — it lifts
	// computeFreeEligible's single-word string taint, which the two-word
	// ABI never applies — and the overwrite-depth fix took the second row
	// from 3072 to this.
	"concat_operand_param_frees_the_caller_array":    2688,
	"concat_operand_param_rewrites_registry_buckets": 2688,
	"accumulator_seeded_from_array_element":          12800,
	"closure_array_capture_churn":                    4752,
	"closure_call_arg_handed_back_is_not_reclaimed":  1920,
	"closure_captures_arr_of_struct_churn_free":      14256,
	"closure_captures_struct_churn_free":             6336,
	"closure_churn_free":                             1584,
	"closure_escapes_return":                         16,
	"closure_capture_passed_to_owned_param":          80,
	"map_delete_tuple_churn_free":                    328000,
	"map_enum_values_churn_free":                     32000,
	"map_generic_enum_values_churn_free":             16000,
	"map_get_push_overwrite":                         16,
	"map_i32_array_values":                           32,
	"map_inline_string_kv_retain_no_crash":           16000,
	"map_iter_escape_churn_free":                     32000,
	"map_iter_string_kv_retain_churn_free":           19200,
	"map_string_array_values":                        16,
	"map_string_keys_churn_free":                     3200,
	"map_string_value_overwrite_pre_drop_churn":      16000,
	"map_string_values_churn_free":                   3200,
	"matchexpr_alias_array_no_free":                  1600,
	"option_of_array":                                32,
	"pair_form_enum_temp_as_argument":                288,
	"pair_form_payload_borrowing_call":               144,
	"stdlib_json_cursor_idiom":                       1776,
	"stdlib_json_roundtrip":                          768,
	"stdlib_query_parse_roundtrip":                   64,
	"string_closure_capture_aliased":                 48,
	// See the x86-64 twin.
	"closure_local_passed_to_callee_released": 384,
	"string_closure_capture_churn_free":       6400,
	"tuple_return_scalar_cursor_recursion":    320,
	// See the x86-64 twin — the same guarded hand-back, byte for byte.
	"consumed_array_arg_temp_released_and_guarded": 128,
	// Another pre-existing arm64 gap the x86-64 twin does not have (0
	// there): the mixed-return case's fresh-path strings are not reclaimed
	// under the two-word ABI. The shape leaks 1264 B here without the
	// returned-alias credit and 880 with it, so this pins what the credit
	// leaves rather than anything it introduced.
	"mixed_return_param_projection_is_owned": 880,
}

// checkCorpusLeaks runs every corpus case under the leak detector and
// holds each one at its baseline.
func checkCorpusLeaks(t *testing.T, backend string, baseline map[string]int64,
	run func(*testing.T, string) (string, string, int)) {
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			_, stderr, code := run(t, c.src)
			m := leakCheckLineRe.FindStringSubmatch(stderr)
			if m == nil {
				t.Fatalf("%s: no leakcheck report on stderr (exit %d): %q — "+
					"the detector must print exactly one line at the exit seam", c.name, code, stderr)
			}
			live, err := strconv.ParseInt(m[3], 10, 64)
			if err != nil {
				t.Fatalf("%s: unparseable live_bytes %q: %v", c.name, m[3], err)
			}
			want := baseline[c.name]
			switch {
			case live == want:
			case want == 0:
				t.Errorf("%s (%s): leaks %d bytes at exit, want 0 — this case reclaimed everything "+
					"before this change. Fix the drop path; do not add it to the baseline table",
					c.name, backend, live)
			case live > want:
				t.Errorf("%s (%s): leaks %d bytes at exit, up from the pinned %d — "+
					"this case already leaked and now leaks more", c.name, backend, live, want)
			default:
				t.Errorf("%s (%s): leaks %d bytes at exit, down from the pinned %d — "+
					"a leak got fixed. Bank it: set the entry to %d, or delete it if it is now 0",
					c.name, backend, live, want, live)
			}
		})
	}
}

func TestX86_64RcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "x86-64", rcCorpusLeakBaselineX86_64, runLeakCheckX86_64)
}

func TestArm64RcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "arm64", rcCorpusLeakBaselineArm64, runLeakCheckArm64)
}

// TestRcCorpusLeakBaselinesNameRealCases keeps the two tables from
// rotting: an entry naming a case the corpus no longer has would sit
// there forever, unreachable and unfalsifiable.
func TestRcCorpusLeakBaselinesNameRealCases(t *testing.T) {
	have := map[string]bool{}
	for _, c := range rcCorpus {
		have[c.name] = true
	}
	for _, tbl := range []struct {
		name string
		m    map[string]int64
	}{
		{"rcCorpusLeakBaselineX86_64", rcCorpusLeakBaselineX86_64},
		{"rcCorpusLeakBaselineArm64", rcCorpusLeakBaselineArm64},
	} {
		var stale []string
		for name := range tbl.m {
			if !have[name] {
				stale = append(stale, name)
			}
		}
		sort.Strings(stale)
		if len(stale) > 0 {
			t.Errorf("%s names %d case(s) no longer in rcCorpus: %s — delete the entries",
				tbl.name, len(stale), strings.Join(stale, ", "))
		}
	}
}
