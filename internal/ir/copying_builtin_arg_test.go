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
		{"writer-write", `var w: Writer = stdout(); var e: Option[IoError] = w.write(p); return 0;`},
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

// The exclusions that would be unsound: inert builtins whose RESULT
// aliases the receiver's interior, and the count-moving container
// mutators. Their absence is the table's whole safety argument.
func TestCopyingBuiltinArgsExcludeAliasingResults(t *testing.T) {
	for _, name := range []string{
		"__method_Map_get", "__method_Map_get_or", "__method_Map_keys",
		"__method_Map_values", "__method_MapIter_key", "__method_MapIter_value",
		"__method_Map_set", "__method_Array_push", "__method_Array_set",
		"__heap_release_to",
	} {
		if _, ok := copyingBuiltinArgs[name]; ok {
			t.Errorf("%s must not be in copyingBuiltinArgs — inert on its arguments is "+
				"not enough when the result aliases the receiver (or the callee moves counts)", name)
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

// The caller-side half for the method form: a string accumulator declared
// inside a match arm and handed to `Writer.write` stays OWNED, so its
// self-append takes the in-place `__fern_str_append` path and its scope-exit
// release frees. `__method_Writer_write` writes the bytes to the fd and
// retains nothing; without its table entry the accumulator was
// borrow-tainted, every `out = out + piece` copied the whole prefix afresh,
// and the superseded copy was decremented without being freed (#8394).
func TestAccumulatorWrittenToWriterStaysInPlace(t *testing.T) {
	src := `function main(): i32 {
    var w: Writer = stdout();
    match (Some("abcdefgh\n")) {
        Some(chunk) => {
            var out: string = "";
            var i: i32 = 0;
            while (i < 4) {
                out = out + slice_unchecked(chunk, 0, chunk.len());
                i = i + 1;
            }
            match (w.write(out)) { Some(_) => { return 1; }, None => {} }
        },
        None => { return 2; }
    }
    return 0;
}`
	dumps := map[string]string{}
	RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { RcPlanHook = nil }()
	p := lowerSourceWith(t, src, 8)
	if !hasPlanName(dumps["main"], "freeEligible", "out") {
		t.Errorf("out is not freeEligible — the Writer.write argument taints it; plan:\n%s", dumps["main"])
	}
	fn := findFunc(p, "main")
	if n := countCallDirect(fn.Ops, "__fern_str_append"); n != 1 {
		t.Errorf("main calls __fern_str_append %d times, want 1 — the self-append fell back to the copying concat; ops:\n%s", n, p)
	}
}
