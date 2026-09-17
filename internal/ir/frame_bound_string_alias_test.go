package ir

import "testing"

// On the single-word string ABI computeFreeEligible taints a string local
// passed to a user function unless the counted-retain summary clears the
// position, and the summary had no arm for the plainest thing a callee can
// do with a string parameter: bind it to a local. `var x: string = src`
// refused `src`, and the refusal reached every caller, so the argument was
// never reclaimed — O(1) retention became O(input) (#9549).
//
// The binding retains nothing: the builder's borrowed-alias cancellation
// elides the transfer inc against an exit sweep that never touches the slot
// (TestBorrowedParamAliasTakesNoInc), so the frame ends holding no reference
// it did not start with. frameBoundStringAliases is the summary learning the
// same fact, which it has to do separately because it runs before any
// builder exists.
//
// The caller's `line` being freeEligible is the whole observable: measured on
// the credited case, 400 allocations and 0 frees under FERN_LEAKCHECK before,
// 400 and 400 after.
func TestFrameBoundStringAliasKeepsTheCallersReclaim(t *testing.T) {
	for _, c := range []struct {
		name string
		// callee is a complete function named `tag` taking (src: string,
		// i: i32). Its return type is whatever `ret` says.
		callee string
		ret    string
		// free is whether the CALLER's `line` should be reclaimable.
		free bool
		// why explains the verdict in the failure message.
		why string
	}{
		{
			name: "read through the alias",
			ret:  "i32",
			callee: `var x: string = src;
    return x.len() + i;`,
			free: true,
			why:  "an alias read for a scalar retains nothing past the return",
		},
		{
			name: "alias chain",
			ret:  "i32",
			callee: `var a: string = src;
    var b: string = a;
    return b.len() + i;`,
			free: true,
			why:  "each link names the same buffer under the same ownership",
		},
		{
			name: "alias concatenated",
			ret:  "i32",
			callee: `var x: string = src;
    var j: string = x + "!";
    return j.len() + i;`,
			free: true,
			why:  "__fern_strcat copies both operands into a fresh buffer",
		},
		{
			name: "alias into a returned struct field",
			ret:  "Box",
			callee: `var x: string = src;
    return Box { s: x, n: i };`,
			free: true,
			why:  "a StructLit field is a COUNTED store, so the box owns a reference of its own",
		},
		{
			name: "alias returned whole",
			ret:  "string",
			callee: `var x: string = src;
    if (i % 2 == 0) { return x; }
    return "short";`,
			free: false,
			why:  "the caller receives the alias, so the reference has to be real",
		},
		{
			name: "alias captured by a closure",
			ret:  "i32",
			callee: `var x: string = src;
    var f: (i32) => i32 = (k: i32) => x.len() + k;
    return f(i);`,
			free: false,
			why:  "a capture lives as long as the closure, which can outlive the frame",
		},
		{
			name: "alias handed to a callee that returns it",
			ret:  "string",
			callee: `var x: string = src;
    return keep(x, i);`,
			free: true,
			why: "keep's position is credited by this summary's creditBareReturn, " +
				"and the alias spelling has to reach the same verdict the direct " +
				"one does — TestStringAliasSpellingMatchesTheDirectOne",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			use := "acc = (acc + tag(line, i)) % 101;"
			switch c.ret {
			case "string":
				use = "acc = (acc + tag(line, i).len()) % 101;"
			case "Box":
				use = "acc = (acc + tag(line, i).s.len()) % 101;"
			}
			src := `struct Box { s: string, n: i32 }
function mkstr(a: string): string { return a + "!"; }
function keep(p: string, i: i32): string { if (i % 2 == 0) { return p; } return "z"; }
function tag(src: string, i: i32): ` + c.ret + ` {
    ` + c.callee + `
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a string long enough to defeat the small-string optimisation");
        ` + use + `
        i = i + 1;
    }
    return acc % 7;
}`
			dumps := map[string]string{}
			RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
			defer func() { RcPlanHook = nil }()
			lowerSourceWith(t, src, 8)
			got := hasPlanName(dumps["main"], "freeEligible", "line")
			if got != c.free {
				verb := "is not"
				if got {
					verb = "is"
				}
				t.Errorf("line %s freeEligible in main, want %v — %s; plan:\n%s", verb, c.free, c.why, dumps["main"])
			}
		})
	}
}

// A seed that is later REASSIGNED is the opposite case and keeps its own
// grounds: the binding is rebindable, so the *ast.Var lowering emits a real
// transfer inc and countedSeedOccurrences credits it for that reason.
// frameBoundStringAliases excludes such a name, so this pins that the older
// credit still fires rather than being shadowed by the new one.
func TestReassignedStringSeedKeepsItsOwnCredit(t *testing.T) {
	src := `function mkstr(a: string): string { return a + "!"; }
function tag(src: string, i: i32): i32 {
    var cur: string = src;
    cur = cur + "!";
    return cur.len() + i;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a string long enough to defeat the small-string optimisation");
        acc = (acc + tag(line, i)) % 101;
        i = i + 1;
    }
    return acc % 7;
}`
	dumps := map[string]string{}
	RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { RcPlanHook = nil }()
	lowerSourceWith(t, src, 8)
	if !hasPlanName(dumps["main"], "freeEligible", "line") {
		t.Errorf("line is not freeEligible in main — the reassigned seed lost countedSeedOccurrences' credit; plan:\n%s", dumps["main"])
	}
}

// The change is an agreement, not a new permission: a body that binds the
// parameter to a local must reach the same verdict as the same body written
// against the parameter directly. Before it the two disagreed — the direct
// spelling was credited and the alias spelling refused — and the disagreement
// was the leak, because binding to a local is how most Fern is written.
//
// The pair is measured rather than asserted one-sided so a future tightening
// of either spelling moves both or fails here.
//
// One shape is deliberately NOT in the table: returning the alias whole.
// `return src` is credited (this summary passes creditBareReturn) while
// `var x = src; return x` is refused, so the two still disagree there. The
// refusal is the conservative direction — a leak — and lifting it is a
// separate question: when the alias escapes, the builder declines the
// cancellation and emits a real transfer inc, so the returned reference is
// counted and crediting it would probably be sound. "Probably" is not the
// bar for a change whose failure direction is a use-after-free, and the
// conditions that decide it (movedLocals, arraySetConsumed, scrutinee,
// pair-form and TRMC return rewrites) are builder state this summary runs
// too early to see. TestFrameBoundStringAliasKeepsTheCallersReclaim pins the
// refusal so it is a recorded verdict rather than an oversight.
func TestStringAliasSpellingMatchesTheDirectOne(t *testing.T) {
	for _, c := range []struct{ name, ret, direct, alias string }{
		{
			"scalar read", "i32",
			`return src.len() + i;`,
			`var x: string = src;
    return x.len() + i;`,
		},
		{
			"concatenated", "i32",
			`var j: string = src + "!";
    return j.len() + i;`,
			`var x: string = src;
    var j: string = x + "!";
    return j.len() + i;`,
		},
		{
			"into a returned struct field", "Box",
			`return Box { s: src, n: i };`,
			`var x: string = src;
    return Box { s: x, n: i };`,
		},
		{
			"forwarded to a callee that returns it", "string",
			`return keep(src, i);`,
			`var x: string = src;
    return keep(x, i);`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			verdict := func(body string) bool {
				use := "acc = (acc + tag(line, i)) % 101;"
				switch c.ret {
				case "string":
					use = "acc = (acc + tag(line, i).len()) % 101;"
				case "Box":
					use = "acc = (acc + tag(line, i).s.len()) % 101;"
				}
				src := `struct Box { s: string, n: i32 }
function mkstr(a: string): string { return a + "!"; }
function keep(p: string, i: i32): string { if (i % 2 == 0) { return p; } return "z"; }
function tag(src: string, i: i32): ` + c.ret + ` {
    ` + body + `
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a string long enough to defeat the small-string optimisation");
        ` + use + `
        i = i + 1;
    }
    return acc % 7;
}`
				dumps := map[string]string{}
				RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
				defer func() { RcPlanHook = nil }()
				lowerSourceWith(t, src, 8)
				return hasPlanName(dumps["main"], "freeEligible", "line")
			}
			if d, a := verdict(c.direct), verdict(c.alias); d != a {
				t.Errorf("line freeEligible is %v written directly and %v through a local alias — the two spellings name the same buffer under the same ownership and must agree", d, a)
			}
		})
	}
}
