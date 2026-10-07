package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructStrFieldReclaimIRX86_64 pins the #4297 A2 slice: a `string`
// FIELD of a reclaimable, non-escaping struct local is reclaimed when the
// struct is dropped. The construction retains a non-fresh string field, and the
// drop releases it rc-aware: free at rc==1, dec at rc>1, skip an immortal
// view/literal at rc<0.
//
// The reclaim is proven by SCALE: a fresh-string-field struct is built and
// dropped every iteration. WITHOUT the field-drop the fresh name box leaks each
// iteration and millions of iterations exhaust the heap (SIGKILL 137); WITH it
// the heap stays flat. A spurious double-free would instead tick
// __rc_underflow_count() -> exit 99. Exit 0 proves the field is reclaimed
// AND balanced (no over-release) over millions of build/drop cycles.
func TestSelfHostStructStrFieldReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (137 = heap exhausted → field not reclaimed; 99 = over-release)", name, code, want)
		}
	}

	// RECLAIM AT SCALE: struct `R { name: string, items: i32[] }` is reclaimable
	// (has an rc-array field), so its exit-sweep drop deep-drops `items` AND now
	// frees `name`. `name` is a FRESH concat (sole-owned rc=1) → freed each iter;
	// `r` never escapes, so it's swept every iteration. 2,000,000 build/drop
	// cycles stay flat (name freed) → exit 0; a leak would SIGKILL (137).
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { let pre: string = "aa"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let r: R = R { name: pre + "x", items: [1, 2, 3] }; if (r.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-reclaim-churn", 0)

	// NON-FRESH (aliased) string field: `name` is bound from a live local `nm`,
	// so the struct co-owns it via the construction rc_inc and the field-drop only
	// DECS the dup — `nm` (swept at scope exit) frees it at rc 0. Balanced: no
	// over-release (underflow 0) over 2,000,000 cycles, and no premature free
	// (r.name reads len 3 while nm is still live). Exit 0. nm is a concat over ids so
	// it and r are built on the heap rather than placed as constants.
	run(t, `struct R { name: string, items: i32[] }
@noinline function ids(s: string): string { return s; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let nm: string = ids("ab") + "c"; let r: R = R { name: nm, items: [1] }; if (r.name.len() != 3) { bad = 1; } if (nm.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-aliased-balanced", 0)

	// FUNCTIONAL-UPDATE base-copy: `r2 = R { ...r1, items: [...] }` copies `name`
	// from r1 (un-overridden), so r2.name ALIASES r1.name. The base-copy retain
	// (rc_inc, gated on the struct being reclaimable) lets r2's field-drop only DEC
	// the dup; without it r2's drop would free r1's name → over-release. Both r1 and
	// r2 are reclaimable non-escaping locals swept each iteration. Balanced across
	// 2,000,000 cycles (underflow 0) with r1.name still valid (len 3) → exit 0;
	// the pre-fix double-free would tick the underflow counter → exit 99. nm is a
	// concat over ids so it and r1 are built on the heap rather than placed as constants.
	run(t, `struct R { name: string, items: i32[] }
@noinline function ids(s: string): string { return s; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let nm: string = ids("ab") + "c"; let r1: R = R { name: nm, items: [1] }; let r2: R = R { ...r1, items: [2, 3] }; if (r2.name.len() != 3) { bad = 1; } if (r1.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-base-copy-balanced", 0)

	// NESTED string-only struct (deep-drop): `B { name: string }` has no rc-array
	// field, but dropping the outer `A` must still release B.name (a fresh concat,
	// rc=1), not just the inner B box. A is reclaimable (its `items` array) and
	// non-escaping, swept each iteration.
	// 1,500,000 cycles stay flat (B.name freed) → exit 0; a leak SIGKILLs (137).
	run(t, `struct B { name: string }
struct A { inner: B, items: i32[] }
function churn(n: i32): i32 { let pre: string = "z"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let a: A = A { inner: B { name: pre + "xy" }, items: [1, 2] }; if (a.inner.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1500000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"nested-string-only-struct-reclaim", 0)

	// STRING[] FIELD: a struct whose string[] field is only ever constructed
	// from element-fresh array literals and read only via .len() has the field
	// deep-freed via __fern_str_arr_free (elements + buffer at rc==1) at the
	// rebind and at scope exit. 4,000,000 build/drop cycles stay balanced — no
	// over-release (underflow 0) and correct values → exit 0. The second element
	// is a concat over ids so it is built on the heap rather than folded to a
	// constant.
	run(t, `struct Diag { code: i32, notes: string[] }
@noinline function ids(s: string): string { return s; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: Diag = Diag { code: i, notes: ["alpha", ids("beta") + "x"] }; if (d.notes.len() != 2) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(4000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-reclaim-churn", 0)

	// NON-admitted: the string[] field value is a bare IDENT (an alias of a
	// live local), so the strarrfld store gate marks the field unsafe and the
	// type keeps the sound leak — xs's element boxes must survive the struct
	// drop (xs is read after). Value correct, underflow 0. xs[0] is a concat over ids
	// so xs is built on the heap rather than placed as a constant.
	run(t, `struct Diag { code: i32, notes: string[] }
@noinline function ids(s: string): string { return s; }
function main(): i32 { let xs: string[] = [ids("a") + "b", "cd"]; let d: Diag = Diag { code: 3, notes: xs }; let s: i32 = d.code + xs[0].len() + xs.len(); if (s != 7) { return 90; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`,
		"strarr-field-aliased-excluded", 0)

	// NON-admitted: an ELEMENT READ (`d.notes[0]`) binds an uncounted alias of
	// an element box, so the read gate excludes the type — the element must
	// survive the struct's exit drop. Value correct, underflow 0. notes[0] is a concat
	// over ids so d is built on the heap rather than placed as a constant.
	run(t, `struct Diag { code: i32, notes: string[] }
@noinline function ids(s: string): string { return s; }
function main(): i32 { let d: Diag = Diag { code: 3, notes: [ids("al") + "pha", "beta"] }; let n0: string = d.notes[0]; let s: i32 = d.code + d.notes.len() + n0.len(); if (s != 10) { return 90; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`,
		"strarr-field-read-excluded", 0)

	// PRODUCER-CALL ELEMENTS, BOUNDED HIGH-WATER: the field is built from calls
	// to `w`, a fresh-string producer, rather than from inline concats. Those
	// elements are owned like a literal's; a leaked element box per round
	// exits 98.
	run(t, `struct Diag { code: i32, notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-producer-elements-flat", 0)

	// A SIBLING TYPE'S IDENTICALLY-NAMED FIELD no longer costs this one its
	// reclaim. `Other.notes` is stored from a borrowed parameter, so it is
	// correctly refused — but the mark used to be the bare field NAME, which
	// disqualified `Diag.notes` too, and every string[] field called `notes`
	// anywhere in the program with it. Marks are keyed "<T>.<field>" now, so
	// Diag is admitted and the churn is flat. `useother` runs once OUTSIDE the
	// measured loop: it is what puts the mark in the set, and its own leak is
	// the sound refusal, not something to measure.
	run(t, `struct Diag { code: i32, notes: string[] }
struct Other { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mkother(notes: string[]): Other { return Other { notes: notes }; }
function useother(pre: string): i32 { let o: Other = mkother([pre]); return o.notes.len(); }
function build(pre: string): i32 { let d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let seed: i32 = useother("q"); if (seed < 0) { return 96; } let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-sibling-name-not-poisoned", 0)

	// A BORROWED-PARAMETER store: the construction takes a reference, so
	// `Esc`'s drop decs a count it owns instead of freeing the caller's array,
	// and the caller's values survive. `Ok.notes`, same field name, is
	// reclaimed on its own merits. A long string is built between the store and
	// the reads so a wrongly freed block is really recycled first.
	// 2 + 43 + 43 + 1 = 89.
	run(t, `struct Esc { notes: string[] }
struct Ok { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkesc(notes: string[]): Esc { return Esc { notes: notes }; }
function build(pre: string): i32 { let live: string[] = [w(pre), w(pre)]; let e: Esc = mkesc(live); let o: Ok = Ok { notes: [w(pre)] }; let junk: string = fill(20); if (junk.len() < 0) { return 0; } return e.notes.len() + live[0].len() + live[1].len() + o.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-borrowed-param-retained", 0)

	// WHOLE-ARRAY PRODUCER CALL as the field value, BOUNDED HIGH-WATER:
	// `Node { deps: deps_of(pre) }`. Every element of that result is a box the
	// callee allocated at rc=1, so the struct owns them exactly as it owns a
	// literal's, and the elements and the buffer are released each round.
	run(t, `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let f: Node = Node { name: w(pre), deps: deps_of(pre), mtime: 1 }; return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-producer-call-store-flat", 0)

	// A LOCAL SHADOWING the producer's name: `deps_of` here is a string[] local,
	// not the registered declaration, so the value reaches the field as a bare
	// ident aliasing a live array. That is now admitted with a retain rather
	// than refused, so the local stays valid because the field holds a counted
	// reference — not because nothing was freed. Read after the struct's drop
	// point with a long string built in between, so a wrongly freed block is
	// really recycled first. 1 + 43 = 44.
	run(t, `struct Sh { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { let deps_of: string[] = [w(pre)]; let o: Sh = Sh { deps: deps_of }; let j: string = fill(20); if (j.len() < 0) { return 0; } return o.deps.len() + deps_of[0].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 44) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-producer-name-shadowed-retained", 0)
}
