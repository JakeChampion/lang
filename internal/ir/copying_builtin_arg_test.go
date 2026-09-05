package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A string handed to a copying builtin — strbuf_append, print, the
// byte scanners — is memcpy'd or written out and never retained, so
// the parameter must be credited (#7867 slice 2). Before the credit,
// `EmitState.write`-shaped callees left every caller's fresh concat
// temp permanently unreclaimed, and a bound local passed to
// strbuf_append lost its scope-exit drop via the same taint.
func TestCopyingBuiltinArgIsCounted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"strbuf_append", `strbuf_append(p); return 0;`},
		{"count_byte", `return __count_byte(p, 97);`},
		{"memchr", `return __memchr(p, 97, 0);`},
		{"print", `print(p); return 0;`},
	}
	for _, c := range cases {
		src := "function eat(p: string): i32 { " + c.body + " }\nfunction main(): i32 { return 0; }"
		got := paramCountedFor(t, src, "eat")
		if len(got) != 1 || !got[0] {
			t.Errorf("%s: paramCountedRetain[eat] = %v, want [true] — the builtin copies "+
				"the bytes out and retains nothing", c.name, got)
		}
	}
}

// One copying use does not launder a retaining one: everyOccurrenceSafe
// is all-or-nothing, so a parameter that is ALSO stored keeps the
// refusal — crediting it would let the caller free a live buffer.
func TestCopyingUseComposesWithThePushCredit(t *testing.T) {
	// Originally this pinned the refusal: the append store was treated as
	// uncounted retention, and the copying-builtin credit must not launder
	// it. The #7914 push-element credit made the append a COUNTED
	// occurrence (emitArrayPush's unconditional element retain), so both
	// occurrences are now legitimately safe and the two credits compose —
	// measured balanced end-to-end by the
	// copying_builtin_composes_with_push_credit corpus case. The refusal
	// this used to watch (an occurrence nothing counts) lives on in
	// TestStringParamPushedThenReturnedBareStaysUncredited.
	src := `function keep(p: string): string[] {
    var n: i32 = __count_byte(p, 97);
    var out: string[] = [];
    out = out.append(p);
    if (n > 0) { return out; }
    return out;
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "keep")
	if len(got) != 1 || !got[0] {
		t.Errorf("paramCountedRetain[keep] = %v, want [true] — the copying read and "+
			"the counted push store are each safe occurrences", got)
	}
}

// The audit interlock: every table member must be inert per the rc
// signature registry — a member that moves a count fails here rather
// than in a corpus run. The registry deliberately does not model the
// RESULT axis, which is why the table is hand-audited rather than
// derived from it; this test pins the half the registry does model.
func TestCopyingBuiltinArgsAreInertPerTheRegistry(t *testing.T) {
	for name := range copyingBuiltinArgs {
		if rcInertBuiltins[name] {
			continue
		}
		if alias, ok := builtinRuntimeAlias(name); ok && rcInert[alias] {
			continue
		}
		t.Errorf("%s is in copyingBuiltinArgs but the rc signature registry does not "+
			"record it inert — read the runtime body and classify it there first", name)
	}
}

// The exclusions that would be unsound: the POSITIONS whose value the call
// can hand back, and the count-moving container mutators. Their absence is
// the table's whole safety argument.
//
// A Map read is why this is stated per position rather than per callee: its
// KEY is hashed and compared and nothing else, while its receiver and
// get_or's fallback are exactly what the call returns. Listing the callee
// wholesale would credit all three; leaving it out entirely cost the key's
// own scope-exit release (#8277).
func TestCopyingBuiltinArgsExcludeAliasingResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  int
		why  string
	}{
		{"__method_Map_get", 0, "the receiver, whose interior the result aliases"},
		{"__method_Map_get_or", 0, "the receiver, whose interior the result aliases"},
		{"__method_Map_get_or", 2, "the fallback, which IS the result on a miss"},
		{"__method_Map_has", 0, "the receiver"},
		{"__method_Map_delete", 0, "the receiver"},
		{"__method_Map_keys", 0, "the receiver, whose keys the result copies out"},
		{"__method_Map_values", 0, "the receiver, whose interior the result aliases"},
		{"__method_MapIter_key", 0, "the iterator, whose interior the result aliases"},
		{"__method_MapIter_value", 0, "the iterator, whose interior the result aliases"},
		{"__method_Map_set", 1, "the key, which the map retains"},
		{"__method_Map_set", 2, "the value, which the map retains"},
		{"__method_Array_push", 1, "the element, which the array retains"},
		{"__method_Array_set", 2, "the element, which the array retains"},
		{"__heap_release_to", 0, "the callee invalidates memory wholesale"},
	} {
		if copyingBuiltinArg(tc.name, tc.arg) {
			t.Errorf("copyingBuiltinArg(%s, %d) is true, but that argument is %s — "+
				"inert on its arguments is not enough when the call can hand the value back",
				tc.name, tc.arg, tc.why)
		}
	}
}

// The half the exclusions above do not state: a Map read's KEY is credited,
// which is what keeps the caller's own release of an aliased key (#8277).
func TestCopyingBuiltinArgsCreditTheMapReadKey(t *testing.T) {
	for _, name := range []string{
		"__method_Map_get", "__method_Map_get_or",
		"__method_Map_has", "__method_Map_delete",
	} {
		if !copyingBuiltinArg(name, 1) {
			t.Errorf("copyingBuiltinArg(%s, 1) is false — a Map read hashes and compares "+
				"its key and retains nothing of it, so the caller must keep its release", name)
		}
	}
}

// The op-level consequence for the bound-local half: a local whose only
// escaping use is a copying builtin keeps its FREEING scope-exit drop.
// __fern_rc_dec only decrements; the dec-only form is what an unbounded
// leak looks like in the IR.
func TestBoundLocalPassedToCopyingBuiltinIsFreed(t *testing.T) {
	src := `function shout(pfx: string, body: string): i32 {
    var msg: string = pfx + body;
    strbuf_append(msg);
    return msg.len();
}
function main(): i32 { return 0; }`
	p := lowerSourceWith(t, src, 8)
	fn := findFunc(p, "shout")
	if n := countCallDirect(fn.Ops, "__fern_str_dec"); n == 0 {
		t.Errorf("shout never calls __fern_str_dec — the concat it bound is dec'd but "+
			"never freed, one buffer leaked per call; ops:\n%s", p)
	}
}

// Free-off lowering is unaffected: the credit only removes a taint that
// gates reclamation, and reclamation is compiled out here.
func TestCopyingBuiltinCreditIsInertWithFreeOff(t *testing.T) {
	defer func(prev bool) { ast.RcFreeEnabled = prev }(ast.RcFreeEnabled)
	ast.RcFreeEnabled = false
	src := `function shout(pfx: string, body: string): i32 {
    var msg: string = pfx + body;
    strbuf_append(msg);
    return msg.len();
}
function main(): i32 { return 0; }`
	p := lowerSourceWith(t, src, 8)
	fn := findFunc(p, "shout")
	if n := countCallDirect(fn.Ops, "__fern_str_dec"); n != 0 {
		t.Errorf("shout emitted %d frees with reclamation off; ops:\n%s", n, p)
	}
}
