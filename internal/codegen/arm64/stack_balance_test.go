package arm64

// Operand-stack balance on the EMITTED assembly.
//
// The arm64 backend maps the IR's operand stack onto the machine `sp`:
// one `str x0, [sp, #-16]!` per push, one `ldr xN, [sp], #16` per pop, an
// `add sp, sp, #N` per drop. Nothing re-synchronises `sp` at a label, so
// the emitted code has to be exactly balanced — every operand a function
// pushes has to leave by a pop or a drop of the same width.
//
// The function epilogue's `mov sp, x29` hides a violation of that: it
// restores `sp` from the frame pointer, so leaked operand slots die with
// the frame and the program still returns the right answer. #7303 lived
// there for exactly that reason — a two-word `string` parked its low word
// on the stack and only its register half was discarded, and every
// exit-code assertion in the tree passed anyway. So the invariant has to
// be checked on the assembly text rather than through behaviour.
//
// `ir.Verify`'s stack half does not reach this either: it is wasm
// validation, where the operand stack is polymorphic after a `br` and
// `end` truncates it back to the frame height. A residual slot on a path
// that ends in a branch is legal there and fatal here.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// balanceReport is what walking one emitted function found.
type balanceReport struct {
	fn string
	// retDepth is the operand-stack depth, in bytes above the
	// post-prologue sp, at the function's return label.
	retDepth int
	// conflicts records labels reached along two paths carrying
	// different operand-stack depths.
	conflicts []string
}

func (r balanceReport) balanced() bool { return r.retDepth == 0 && len(r.conflicts) == 0 }

// spImm parses the `#N` immediate of an sp adjustment.
func spImm(t *testing.T, insn, field string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.Trim(field, "#]!"))
	if err != nil {
		t.Fatalf("unmodelled sp adjustment %q: %v", insn, err)
	}
	return n
}

// checkBalance walks every `__fn_*` body in asm and reports its
// operand-stack depth at the return label.
//
// The walk is an abstract interpretation over the emitted text: it
// carries a depth through straight-line code, records the depth each
// branch delivers to its target label, and adopts the recorded depth when
// it reaches that label. Code after an unconditional branch whose label
// carries no recorded depth is unreachable and is skipped rather than
// guessed at — the same treatment wasm validation gives it.
//
// Any sp-touching instruction the walk does not model fails the test
// loudly. A balance checker that silently ignores an unrecognised form
// would report a leak as balanced, which is the failure mode this whole
// test exists to prevent.
func checkBalance(t *testing.T, asm string) []balanceReport {
	t.Helper()
	var out []balanceReport
	lines := strings.Split(asm, "\n")
	for i := 0; i < len(lines); i++ {
		name := strings.TrimSuffix(lines[i], ":")
		if !strings.HasPrefix(name, "__fn_") || name == lines[i] {
			continue
		}
		end := i + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), ".size") {
			end++
		}
		out = append(out, walkFuncBalance(t, name, lines[i+1:end]))
		i = end
	}
	return out
}

func walkFuncBalance(t *testing.T, name string, body []string) balanceReport {
	t.Helper()
	rep := balanceReport{fn: name}
	depths := map[string]int{}
	depth, reachable, inPrologue := 0, true, true
	retLabel := ""

	deliver := func(label string) {
		if !reachable {
			return
		}
		if d, seen := depths[label]; seen {
			if d != depth {
				rep.conflicts = append(rep.conflicts,
					fmt.Sprintf("%s reached at depth %d and %d", label, d, depth))
			}
			return
		}
		depths[label] = depth
	}

	for _, raw := range body {
		insn := strings.TrimSpace(raw)
		if insn == "" || strings.HasPrefix(insn, ".") && strings.HasSuffix(insn, ":") == false && !strings.HasPrefix(insn, ".L") {
			continue // directive
		}
		if strings.HasSuffix(insn, ":") {
			label := strings.TrimSuffix(insn, ":")
			if strings.HasPrefix(label, ".Lret_") {
				retLabel = label
			}
			deliver(label) // the fall-through edge into the label
			if d, seen := depths[label]; seen {
				depth, reachable = d, true
			}
			continue
		}
		f := strings.Fields(strings.ReplaceAll(insn, ",", " "))
		if len(f) == 0 {
			continue
		}
		switch {
		// Prologue: fp/lr save, frame pointer, locals allocation. The
		// locals frame is the base the operand stack grows from, so it
		// is not part of the depth.
		case inPrologue && insn == "stp x29 x30 [sp #-16]!", inPrologue && insn == "stp x29, x30, [sp, #-16]!":
		case inPrologue && insn == "mov x29, sp":
		case inPrologue && f[0] == "sub" && f[1] == "sp" && f[2] == "sp":
			inPrologue = false
		// Epilogue: `mov sp, x29` unwinds the locals frame AND anything
		// the operand stack still holds — the masking this test exists
		// to see past. Stop the walk there.
		case insn == "mov sp, x29":
			reachable = false
		case f[0] == "str" && f[1] == "x0" && f[2] == "[sp" && strings.HasSuffix(insn, "]!"):
			// Pre-indexed push: the immediate is negative, sp moves
			// down, the operand stack grows.
			depth -= spImm(t, insn, f[3])
			inPrologue = false
		case f[0] == "ldr" && f[2] == "[sp]":
			depth -= spImm(t, insn, f[3])
			inPrologue = false
		case f[0] == "add" && f[1] == "sp" && f[2] == "sp":
			depth -= spImm(t, insn, f[3])
			inPrologue = false
		case f[0] == "sub" && f[1] == "sp" && f[2] == "sp":
			depth += spImm(t, insn, f[3])
		case f[0] == "b" || strings.HasPrefix(f[0], "b."):
			deliver(f[len(f)-1])
			if f[0] == "b" {
				reachable = false
			}
			inPrologue = false
		case f[0] == "cbz", f[0] == "cbnz", f[0] == "tbz", f[0] == "tbnz":
			deliver(f[len(f)-1])
			inPrologue = false
		case f[0] == "bl", f[0] == "ret", f[0] == "br", f[0] == "blr":
			inPrologue = false
		default:
			// Anything else must not touch sp except as a base
			// register in an offset addressing mode.
			for _, tok := range f[1:] {
				if tok == "sp" || tok == "sp]" || tok == "sp]!" {
					t.Fatalf("%s: unmodelled sp-touching instruction %q", name, insn)
				}
			}
			inPrologue = false
		}
	}
	if retLabel == "" {
		t.Fatalf("%s: no return label in the emitted body", name)
	}
	rep.retDepth = depths[retLabel]
	return rep
}

// twoWordDiscardPrograms are shapes where a two-word `string` operand is
// materialised and then discarded unread. Each one leaked 16 bytes of
// operand stack per discard before #7303.
var twoWordDiscardPrograms = map[string]string{
	// The #7303 repro: the guard makes each arm evaluate the pattern's
	// bindings, and neither arm reads `label`.
	"match_arm_binds_unread_string": `
struct Named { id: i32, label: string }
function tag(n: Named): i32 {
	return match (n) {
		Named { id, label } when id > 0 => id,
		Named { id, label } => 0i32,
	};
}
function main(): i32 { return tag(Named { id: 5, label: "hello" }) + tag(Named { id: 0, label: "x" }); }`,

	// The same discard without a match: a string local nothing reads.
	"dead_string_local": `
function f(n: i32): i32 {
	var s: string = "unread";
	return n + 1;
}
function main(): i32 { return f(4); }`,

	// A string-returning call whose result is thrown away.
	"discarded_string_call_result": `
function name(n: i32): string { return "x"; }
function f(n: i32): i32 {
	var s: string = name(n);
	return n + 1;
}
function main(): i32 { return f(4); }`,

	// A string field read from a struct and dropped, with no match at all.
	"unread_struct_string_field": `
struct Named { id: i32, label: string }
function tag(n: Named): i32 {
	var s: string = n.label;
	return n.id;
}
function main(): i32 { return tag(Named { id: 5, label: "hello" }); }`,

	// Two unread string bindings per arm, so a half-discard leaks twice
	// as much and the two arms disagree by twice as much at the join.
	"match_arm_binds_two_unread_strings": `
struct Pair { l: string, r: string }
function f(p: Pair): i32 {
	return match (p) {
		Pair { l, r } when true => 3i32,
		Pair { l, r } => 4i32,
	};
}
function main(): i32 { return f(Pair { l: "a", r: "b" }); }`,

	// Three arms, two guards: every guarded arm re-materialises the
	// bindings, so the join label is reached at three different depths
	// when the discard only takes half.
	"guarded_match_chain_unread_strings": `
struct P { a: string, b: string, n: i32 }
function q(p: P): i32 {
	return match (p) {
		P { a, b, n } when n > 3 => n,
		P { a, b, n } when n > 1 => 0i32,
		P { a, b, n } => 9i32,
	};
}
function main(): i32 { return q(P { a: "x", b: "y", n: 5 }); }`,
}

// TestOperandStackBalancedAcrossTwoWordDiscard is the #7303 gate. It fails
// on a half-discarded two-word operand, which no exit-code assertion can
// see while `mov sp, x29` absorbs the leak.
func TestOperandStackBalancedAcrossTwoWordDiscard(t *testing.T) {
	for name, src := range twoWordDiscardPrograms {
		t.Run(name, func(t *testing.T) {
			for _, peep := range []bool{false, true} {
				asm := compile(t, src, Options{NoPeephole: !peep})
				// Precondition: the program has to actually reach a
				// two-word discard, or the balance assertion below
				// passes without testing anything. A two-word discard
				// is the only thing that frees two operand slots at
				// once.
				if !strings.Contains(asm, fmt.Sprintf("add sp, sp, #%d", 2*slotBytes)) {
					t.Fatalf("peephole=%v: no two-slot discard in the emitted assembly — "+
						"the program no longer exercises a discarded two-word operand", peep)
				}
				reports := checkBalance(t, asm)
				if len(reports) == 0 {
					t.Fatal("no __fn_ bodies found in the emitted assembly")
				}
				for _, r := range reports {
					if r.balanced() {
						continue
					}
					t.Errorf("peephole=%v: %s leaves the operand stack unbalanced: "+
						"%d bytes still pushed at %s's return label%s",
						peep, r.fn, r.retDepth, r.fn, formatConflicts(r.conflicts))
				}
			}
		})
	}
}

func formatConflicts(cs []string) string {
	if len(cs) == 0 {
		return ""
	}
	return "; label depth conflicts: " + strings.Join(cs, ", ")
}

// TestOperandStackBalancedAcrossFeatureMatrix holds the same invariant
// over the backend's existing language-surface spread, so a future
// half-discard anywhere in that surface fails here rather than in a
// differential seed.
func TestOperandStackBalancedAcrossFeatureMatrix(t *testing.T) {
	for name, src := range featureMatrix {
		t.Run(name, func(t *testing.T) {
			for _, r := range checkBalance(t, compile(t, src, Options{})) {
				if !r.balanced() {
					t.Errorf("%s leaves %d bytes on the operand stack at its return label%s",
						r.fn, r.retDepth, formatConflicts(r.conflicts))
				}
			}
		})
	}
}
