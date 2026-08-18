package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructStrFieldReclaimIRX86_64 pins the #4297 A2 slice: a `string`
// FIELD of a reclaimable, non-escaping struct local is now reclaimed when the
// struct is dropped. The struct-lit construction retains (rc_inc) a non-fresh
// string field — gated on the per-lit ownership precompute (field_ownerships /
// str_producer_ownership) so the classifying read stays out of lower_expr's hot
// path — and the k_str arm of the per-type __struct_drop frees it (rc-aware:
// free at rc==1, dec at rc>1, skip an immortal view/literal at rc<0).
//
// The reclaim is proven by SCALE: a fresh-string-field struct is built and
// dropped every iteration. WITHOUT the field-drop the fresh name box leaks each
// iteration and millions of iterations exhaust the heap (SIGKILL 137); WITH it
// the heap stays flat. A spurious double-free would instead tick
// __rc_underflow() -> exit 99. Exit 0 proves the field is reclaimed
// AND balanced (no over-release) over millions of build/drop cycles.
func TestSelfHostStructStrFieldReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
function churn(n: i32): i32 { var pre: string = "aa"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var r: R = R { name: pre + "x", items: [1, 2, 3] }; if (r.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-str-field-reclaim-churn", 0)

	// NON-FRESH (aliased) string field: `name` is bound from a live local `nm`,
	// so the struct co-owns it via the construction rc_inc and the field-drop only
	// DECS the dup — `nm` (swept at scope exit) frees it at rc 0. Balanced: no
	// over-release (underflow 0) over 2,000,000 cycles, and no premature free
	// (r.name reads len 3 while nm is still live). Exit 0.
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var nm: string = "abc"; var r: R = R { name: nm, items: [1] }; if (r.name.len() != 3) { bad = 1; } if (nm.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-str-field-aliased-balanced", 0)

	// FUNCTIONAL-UPDATE base-copy: `r2 = R { ...r1, items: [...] }` copies `name`
	// from r1 (un-overridden), so r2.name ALIASES r1.name. The base-copy retain
	// (rc_inc, gated on the struct being reclaimable) lets r2's field-drop only DEC
	// the dup; without it r2's drop would free r1's name → over-release. Both r1 and
	// r2 are reclaimable non-escaping locals swept each iteration. Balanced across
	// 2,000,000 cycles (underflow 0) with r1.name still valid (len 3) → exit 0;
	// the pre-fix double-free would tick the underflow counter → exit 99.
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var nm: string = "abc"; var r1: R = R { name: nm, items: [1] }; var r2: R = R { ...r1, items: [2, 3] }; if (r2.name.len() != 3) { bad = 1; } if (r1.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-str-field-base-copy-balanced", 0)

	// NESTED string-only struct (deep-drop): `B { name: string }` has no rc-array
	// field, so before #4297 A2's nddo_reach extension it was NOT deep-drop-worthy —
	// dropping the outer `A` shallow-freed the inner B box and LEAKED B.name. Now B
	// is deep-drop-worthy, so A's drop (when B is uniquely owned — a fresh literal
	// here) runs $__struct_drop_B, whose k_str arm frees B.name (a fresh concat, rc=1).
	// A is reclaimable (its `items` array) and non-escaping, swept each iteration.
	// 1,500,000 cycles stay flat (B.name freed) → exit 0; a leak SIGKILLs (137).
	run(t, `struct B { name: string }
struct A { inner: B, items: i32[] }
function churn(n: i32): i32 { var pre: string = "z"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var a: A = A { inner: B { name: pre + "xy" }, items: [1, 2] }; if (a.inner.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1500000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"nested-string-only-struct-reclaim", 0)

	// STRING[] FIELD (the k_str_arr slice): a struct whose string[] field is
	// only ever constructed from element-fresh array literals and read only
	// via .len() is admitted by the strarrfld scan ("strfldok:arr:<T>"), so
	// the consume-rebind __field_reclaim and the exit __struct_drop now
	// deep-free the field via __fern_str_arr_free (elements + buffer at
	// rc==1). 4,000,000 build/drop cycles stay balanced — no over-release
	// (underflow 0) and correct values → exit 0.
	run(t, `struct Diag { code: i32, notes: string[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var d: Diag = Diag { code: i, notes: ["alpha", "beta" + "x"] }; if (d.notes.len() != 2) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(4000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-reclaim-churn", 0)

	// NON-admitted: the string[] field value is a bare IDENT (an alias of a
	// live local), so the strarrfld store gate marks the field unsafe and the
	// type keeps the sound leak — xs's element boxes must survive the struct
	// drop (xs is read after). Value correct, underflow 0.
	run(t, `struct Diag { code: i32, notes: string[] }
function main(): i32 { var xs: string[] = ["ab", "cd"]; var d: Diag = Diag { code: 3, notes: xs }; var s: i32 = d.code + xs[0].len() + xs.len(); if (s != 7) { return 90; } if (__rc_underflow() != 0) { return 99; } return 0; }`,
		"strarr-field-aliased-excluded", 0)

	// NON-admitted: an ELEMENT READ (`d.notes[0]`) binds an uncounted alias of
	// an element box, so the read gate excludes the type — the element must
	// survive the struct's exit drop. Value correct, underflow 0.
	run(t, `struct Diag { code: i32, notes: string[] }
function main(): i32 { var d: Diag = Diag { code: 3, notes: ["alpha", "beta"] }; var n0: string = d.notes[0]; var s: i32 = d.code + d.notes.len() + n0.len(); if (s != 10) { return 90; } if (__rc_underflow() != 0) { return 99; } return 0; }`,
		"strarr-field-read-excluded", 0)

	// PRODUCER-CALL ELEMENTS, BOUNDED HIGH-WATER: the field is built from calls
	// to `w`, a whole-program-proven fresh-string producer, rather than from
	// inline concats. The store gate now asks strarr_value_is_fresh — the same
	// question the "SARR:" local credit and the "STRARR:" producer admission
	// ask — so the type is admitted; the registry-blind sibling it replaced
	// refused any call and every element box leaked per round → 98.
	run(t, `struct Diag { code: i32, notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { var d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w0: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
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
function useother(pre: string): i32 { var o: Other = mkother([pre]); return o.notes.len(); }
function build(pre: string): i32 { var d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var seed: i32 = useother("q"); if (seed < 0) { return 96; } var w0: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-sibling-name-not-poisoned", 0)

	// The refusals type-keying must NOT weaken, checked by VALUE under
	// recycling pressure rather than by bytes. `Esc.notes` is stored from a
	// borrowed parameter — the caller's array must outlive the struct's drop —
	// while `Ok.notes`, same field name, is admitted. A long string is built
	// between the store and the reads so a wrongly freed block is really
	// recycled first. 2 + 43 + 43 + 1 = 89, underflow 0.
	run(t, `struct Esc { notes: string[] }
struct Ok { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function fill(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkesc(notes: string[]): Esc { return Esc { notes: notes }; }
function build(pre: string): i32 { var live: string[] = [w(pre), w(pre)]; var e: Esc = mkesc(live); var o: Ok = Ok { notes: [w(pre)] }; var junk: string = fill(20); if (junk.len() < 0) { return 0; } return e.notes.len() + live[0].len() + live[1].len() + o.notes.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-borrowed-param-still-excluded", 0)

	// WHOLE-ARRAY PRODUCER CALL as the field value, BOUNDED HIGH-WATER: the
	// store gate accepted only an array LITERAL, so `Node { deps: deps_of(pre) }`
	// was refused and every element box plus the buffer leaked per round — the
	// same omission the "SARR:" local credit had for its initialiser.
	// fn_returns_fresh_strarr is the "STRARR:" registry's own admission rule,
	// named so the field gate asks the question the registry already answers:
	// every element of that result is a box the callee allocated at rc=1, so
	// the struct owns them exactly as it owns a literal's.
	run(t, `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { var f: Node = Node { name: w(pre), deps: deps_of(pre), mtime: 1 }; return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { var w0: i32 = churn(5000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(5000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-producer-call-store-flat", 0)

	// A LOCAL SHADOWING the producer's name refuses the store: `deps_of` here
	// is a string[] local, not the registered declaration, so the value is a
	// bare ident aliasing a live array and the type keeps the sound leak. The
	// local is read after the struct's drop point, with a long string built in
	// between so a wrongly freed block is really recycled first. 1 + 43 = 44.
	run(t, `struct Sh { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { var deps_of: string[] = [w(pre)]; var o: Sh = Sh { deps: deps_of }; var j: string = fill(20); if (j.len() < 0) { return 0; } return o.deps.len() + deps_of[0].len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 44) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-producer-name-shadowed-excluded", 0)
}
