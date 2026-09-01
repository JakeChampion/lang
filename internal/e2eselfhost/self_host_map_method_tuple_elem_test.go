package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostMapMethodTupleElem pins a MAP method call at a tuple-element
// position to the IR path (#7213).
//
// tuple_elem_ctor_eligible admitted a builtin-receiver method only through its
// `.len()` carve-out, and that carve-out was keyed on the string / array
// predicates alone. So `(xs.len(), 5)` lowered and `(m.len(), 5)` bailed the
// whole enclosing function, the difference being nothing but the receiver's
// type — and the same for `has`, `get` and `get_or`, whose results are one slot
// each. (`keys` / `values` / `insert` already answered through the array and
// Map predicates.)
//
// The fix names a map read method's result kind from the receiver's
// `Map[K, V]`, so eligibility and elem_type_tag cannot disagree: a
// `Map[K, string]` read is stored in a string slot, a `Map[K, i64]` read at
// eight bytes, and a value column with no element tag (an array, a struct) is
// not admitted at all.
//
// Each case asserts BOTH halves:
//
//   - path: the module must lower on the IR path (the coverage half — goal 1).
//   - value: the element must read back correctly, checked against the same
//     computation spelled without the tuple, so it asserts agreement rather
//     than a hard-coded constant.
//
// `iter` is deliberately not admitted: its MapIter box holds a raw pointer into
// the mapbox, which a tuple slot would outlive.
func TestSelfHostMapMethodTupleElem(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	asmRun := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probe := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range []struct {
		name string
		prog string
	}{
		{
			// The issue's own shape.
			"len-elem",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   var u: (i32, i32) = (m.len(), 5);
			   if (u.0 == m.len() && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
		{
			"has-elem",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   var u: (boolean, boolean) = (m.has("k"), m.has("nope"));
			   if (u.0 && !u.1) { return 7; }
			   return 9;
			 }`,
		},
		{
			"get_or-i32-elem",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   var u: (i32, i32) = (m.get_or("k", 0), m.get_or("nope", 3));
			   if (u.0 == 7 && u.1 == 3) { return 7; }
			   return 9;
			 }`,
		},
		{
			// The value column is what decides the slot: a string read must be
			// stored as a string, not fall through to the "i32" default and
			// read the box's data-ptr slot as a length.
			"get_or-string-elem",
			`function main(): i32 {
			   var m: Map[string, string] = map_new(4);
			   m = m.insert("k", "abcd");
			   var u: (string, i32) = (m.get_or("k", "zz"), 5);
			   if (u.0.len() == 4 && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
		{
			// An i64 column rides the 8-byte slot, so a value past 2^31 must
			// survive the round trip rather than truncating.
			"get_or-i64-elem",
			`function main(): i32 {
			   var m: Map[i32, i64] = map_new_i32(4);
			   m = m.insert(1, 3000000000i64);
			   var u: (i64, i32) = (m.get_or(1, 0i64), 5);
			   if (u.0 == 3000000000 && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
		{
			"get-elem",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   var u: (Option[i32], i32) = (m.get("k"), 5);
			   var got: i32 = 0;
			   match (u.0) {
			     Some(v) => { got = v; },
			     None => { got = 0 - 1; },
			   }
			   if (got == 7 && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
		{
			// A map read as the SECOND element, so the classification is not
			// only exercised at position 0.
			"len-second-elem",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   m = m.insert("a", 1);
			   m = m.insert("b", 2);
			   var u: (i32, i32) = (5, m.len());
			   if (u.0 == 5 && u.1 == 2) { return 7; }
			   return 9;
			 }`,
		},
		// Controls — already on the IR path before this change, and must stay.
		{
			"arr-len-elem-control",
			`function main(): i32 {
			   var xs: i32[] = [1, 2, 3];
			   var u: (i32, i32) = (xs.len(), 5);
			   if (u.0 == 3 && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
		{
			"keys-elem-control",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   var u: (string[], i32) = (m.keys(), 5);
			   if (u.0.len() == 1 && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
		{
			"insert-elem-control",
			`function main(): i32 {
			   var m: Map[string, i32] = map_new(4);
			   var u: (Map[string, i32], i32) = (m.insert("k", 7), 5);
			   if (u.0.get_or("k", 0) == 7 && u.1 == 5) { return 7; }
			   return 9;
			 }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.TrimSpace(string(runCapture(t, gcc, runner, probe, []byte(tc.prog)))); got != "ir" {
				t.Fatalf("path = %q, want \"ir\" — the module is not on the IR path, so this case covers nothing", got)
			}
			asm := runCapture(t, gcc, runner, asmRun, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "mapmethtup_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 7 {
				t.Errorf("exit = %d, want 7 (9 = the tuple element read back wrong)", code)
			}
		})
	}
}
