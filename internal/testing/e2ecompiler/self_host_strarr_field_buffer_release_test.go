package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// strArrFieldBufferReleaseCases pin the release of a struct's `string[]` field
// BUFFER on every backend, whether or not its elements are freed with it (found
// while measuring the conformance case alloc_flat_fresh_array_arg).
//
// A field whose elements are read through the struct, so an element alias may
// survive, still has its buffer released; only the element walk is withheld. A
// field stored as an uncounted alias (a field read, an `.append` on one) must
// keep its buffer, which another owner still holds.
//
// The probes use SSO-short elements on purpose: the buffer is then the only heap
// object per round, and each case returns the MEASURED bytes per round as its
// exit code, so a regression reports its own size instead of a bare "not zero".
var strArrFieldBufferReleaseCases = []struct {
	name string
	src  string
	want int
}{
	// SCOPE EXIT. `f` dies at the end of `round`, so Node's drop releases the
	// deps buffer. `f.deps[0].len()` reads an element through the struct; the
	// buffer still has to go back every round.
	{"strarr-field-drop-buffer-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function mk(): string[] { let o: string[] = []; o = o.append("aa"); o = o.append("bb"); o = o.append("cc"); return o; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { let f: Node = node(pre, mk(), n); return f.deps.len() + f.deps[0].len() + f.name.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let w: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 2000;
}`, 0},
	// LOOP REBIND. The same field, released when `let r: Row = Row { … }` is
	// re-declared each iteration: the superseded box's buffer must go back,
	// not only the last one at scope exit.
	{"strarr-field-rebind-buffer-flat", `struct Row { tag: string, cells: string[] }
function mk(): string[] { let o: string[] = []; o = o.append("aa"); o = o.append("bb"); o = o.append("cc"); return o; }
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let r: Row = Row { tag: "t", cells: mk() };
        acc = (acc + r.cells.len() + r.cells[0].len()) % 251;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 2000;
}`, 0},
	// The ADMITTED path must not be displaced by the shallow arm: no element
	// read, so "strfldok:arr:Node" holds and __fern_str_arr_free deep-frees the
	// elements AND the buffer. Wide elements, bounded high-water — a shallow arm
	// that shadowed the deep one would strand three element boxes per round and
	// exceed the 4 KB budget over the second 2000-iteration churn.
	{"strarr-field-admitted-deep-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let o: string[] = []; let i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { let f: Node = node(w(pre), mk(pre), n); return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let w0: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (w0 != x) { return 97; }
    return 0;
}`, 0},
	// The shallow arm must stay SHALLOW on a non-admitted field: the elements
	// are read through the struct and through the live local, so freeing them
	// would corrupt both. Wide elements, values exact, over-release detector at
	// zero.
	{"strarr-field-nonadmitted-elements-safe", `struct Doc { title: string, lines: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let o: string[] = []; let i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function doc(title: string, lines: string[]): Doc { return Doc { title: title, lines: lines }; }
function round(pre: string): i32 { let d: Doc = doc(w(pre), mk(pre)); let junk: string[] = mk(pre); if (junk.len() < 0) { return 0; } return d.lines.len() + d.lines[0].len() + d.lines[2].len() + d.title.len(); }
function main(): i32 { let pre: string = "ab"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 132) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// A functional-update BASE copy hands the new box every array field
	// pointer with no retain, so `b` and the `sg` built from it hold one
	// `ys` buffer between them at rc 1. `sg` is dropped at inner's exit; the
	// deep arm would free the buffer and its element boxes while `b` still
	// reads them, so every backend must leave `ys` alone (#8119). The junk
	// allocations recycle the freed blocks so a wrong answer, not luck, is
	// what an over-release reports.
	{"strarr-field-base-copy-co-owner", `struct Reg { rows: string[] }
struct Sigs { a: Reg, xs: string[], ys: string[] }
function reg_of(rows: string[]): Reg { return Reg { rows: rows }; }
function with_dyn(sg: Sigs, extra: string): Sigs { return Sigs { ...sg, a: reg_of(sg.a.rows.append(extra)) }; }
function inner(base: Sigs): i32 {
    let sg: Sigs = Sigs { ...with_dyn(base, "q"), xs: ["z"] };
    return sg.a.rows.len() + sg.ys.len();
}
function outer(b: Sigs): i32 {
    let n: i32 = inner(b);
    let junk: string[] = [];
    let j: i32 = 0;
    while (j < 8) { junk = junk.append("w" + "j"); j = j + 1; }
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < b.ys.len()) { t = t + ("k:" + b.ys[i]).len(); i = i + 1; }
    return n + t + junk.len();
}
function main(): i32 {
    let s: Sigs = Sigs { a: reg_of(["r1"]), xs: ["x1", "x2"], ys: ["y1", "y2", "y3"] };
    let r: i32 = outer(s);
    if (__rc_underflow_count() != 0) { return 99; }
    if (r != 25) { return 97; }
    return 0;
}`, 0},
	// A struct field naming a still-live CALLER array (#8210). `names` is built
	// from element strings the `items` structs own, so nothing in the buffer is
	// the registry's to free; `reg_of` then stores that same buffer in a field,
	// and both names die at probe's exit — the local first, which leaves the
	// field holding the last reference. An ungated deep arm walks it there and
	// frees every element while `items` still reads them, and the size-class
	// freelist hands the blocks straight back: the corrupted read is what the
	// wasm-hosted compiler emitted as a function name of two binary bytes, the
	// bytes of a recycled array header. Distinct per-item labels built at run
	// time, so nothing folds to an immortal literal and the churn recycles.
	{"strarr-field-caller-array-co-owner", `struct Item { name: string }
struct Reg { rows: string[], head: i32[] }
function digit(d: i32): string {
    if (d == 0) { return "0"; } if (d == 1) { return "1"; } if (d == 2) { return "2"; }
    if (d == 3) { return "3"; } if (d == 4) { return "4"; } if (d == 5) { return "5"; }
    if (d == 6) { return "6"; } if (d == 7) { return "7"; } if (d == 8) { return "8"; }
    return "9";
}
function label(i: i32): string { return "row" + digit(i); }
function reg_of(rows: string[]): Reg { let head: i32[] = []; let i: i32 = 0; while (i < rows.len()) { head = head.append(i); i = i + 1; } return Reg { rows: rows, head: head }; }
function probe(items: Item[], want: string): i32 {
    let names: string[] = [];
    let i: i32 = 0;
    while (i < items.len()) { names = names.append(items[i].name); i = i + 1; }
    let r: Reg = reg_of(names);
    let hits: i32 = 0;
    let j: i32 = 0;
    while (j < r.rows.len()) { if (r.rows[j] == want) { hits = hits + 1; } j = j + 1; }
    return hits + names.len();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let a: string[] = []; a = a.append("w" + digit(i % 10)); a = a.append("q" + digit(i % 7)); acc = acc + a.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let items: Item[] = [];
    let i: i32 = 0;
    while (i < 10) { items = items.append(Item { name: label(i) }); i = i + 1; }
    if (probe(items, "row7") != 11) { return 97; }
    if (churn(64) != 128) { return 97; }
    let k: i32 = 0;
    while (k < items.len()) { if (items[k].name != label(k)) { return 97; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// The same co-owner with its element read spelled as a `.len()` borrow, which
	// the read scan does not mark (#10405). Nothing then kept the deep walk off
	// `Reg.rows`: `names` holds boxes `items` owns, so a bare-ident store of it
	// must withhold the walk wherever the array's elements are not provably its
	// own. The three rows reach the field directly, through the storing
	// parameter's call site, and through a second parameter forwarding it.
	{"strarr-field-caller-array-co-owner-len", `struct Item { name: string }
struct Reg { rows: string[], head: i32[] }
function digit(d: i32): string {
    if (d == 0) { return "0"; } if (d == 1) { return "1"; } if (d == 2) { return "2"; }
    if (d == 3) { return "3"; } if (d == 4) { return "4"; } if (d == 5) { return "5"; }
    if (d == 6) { return "6"; } if (d == 7) { return "7"; } if (d == 8) { return "8"; }
    return "9";
}
function label(i: i32): string { return "row" + digit(i); }
function reg_of(rows: string[]): Reg { let head: i32[] = []; let i: i32 = 0; while (i < rows.len()) { head = head.append(i); i = i + 1; } return Reg { rows: rows, head: head }; }
function probe(items: Item[], want: string): i32 {
    let names: string[] = [];
    let i: i32 = 0;
    while (i < items.len()) { names = names.append(items[i].name); i = i + 1; }
    let r: Reg = reg_of(names);
    let hits: i32 = 0;
    let j: i32 = 0;
    while (j < r.rows.len()) { if (r.rows[j].len() == want.len()) { hits = hits + 1; } j = j + 1; }
    return hits + names.len();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let a: string[] = []; a = a.append("w" + digit(i % 10)); a = a.append("q" + digit(i % 7)); acc = acc + a.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let items: Item[] = [];
    let i: i32 = 0;
    while (i < 10) { items = items.append(Item { name: label(i) }); i = i + 1; }
    if (probe(items, "row7") != 20) { return 97; }
    if (churn(64) != 128) { return 97; }
    let k: i32 = 0;
    while (k < items.len()) { if (items[k].name != label(k)) { return 97; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"strarr-field-borrowed-local-store", `struct Item { name: string }
struct Reg { rows: string[], head: i32[] }
function digit(d: i32): string {
    if (d == 0) { return "0"; } if (d == 1) { return "1"; } if (d == 2) { return "2"; }
    if (d == 3) { return "3"; } if (d == 4) { return "4"; } if (d == 5) { return "5"; }
    if (d == 6) { return "6"; } if (d == 7) { return "7"; } if (d == 8) { return "8"; }
    return "9";
}
function label(i: i32): string { return "row" + digit(i); }
function reg_of(rows: string[]): Reg { let head: i32[] = []; let i: i32 = 0; while (i < rows.len()) { head = head.append(i); i = i + 1; } return Reg { rows: rows, head: head }; }
function probe(items: Item[], want: string): i32 {
    let names: string[] = [];
    let i: i32 = 0;
    while (i < items.len()) { names = names.append(items[i].name); i = i + 1; }
    let head: i32[] = [];
    let r: Reg = Reg { rows: names, head: head };
    let hits: i32 = 0;
    let j: i32 = 0;
    while (j < r.rows.len()) { if (r.rows[j].len() == want.len()) { hits = hits + 1; } j = j + 1; }
    return hits + names.len();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let a: string[] = []; a = a.append("w" + digit(i % 10)); a = a.append("q" + digit(i % 7)); acc = acc + a.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let items: Item[] = [];
    let i: i32 = 0;
    while (i < 10) { items = items.append(Item { name: label(i) }); i = i + 1; }
    if (probe(items, "row7") != 20) { return 97; }
    if (churn(64) != 128) { return 97; }
    let k: i32 = 0;
    while (k < items.len()) { if (items[k].name != label(k)) { return 97; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"strarr-field-borrowed-forwarded-param", `struct Item { name: string }
struct Reg { rows: string[], head: i32[] }
function digit(d: i32): string {
    if (d == 0) { return "0"; } if (d == 1) { return "1"; } if (d == 2) { return "2"; }
    if (d == 3) { return "3"; } if (d == 4) { return "4"; } if (d == 5) { return "5"; }
    if (d == 6) { return "6"; } if (d == 7) { return "7"; } if (d == 8) { return "8"; }
    return "9";
}
function label(i: i32): string { return "row" + digit(i); }
function reg_of(rows: string[]): Reg { let head: i32[] = []; let i: i32 = 0; while (i < rows.len()) { head = head.append(i); i = i + 1; } return Reg { rows: rows, head: head }; }
function reg_via(rows: string[]): Reg { return reg_of(rows); }
function probe(items: Item[], want: string): i32 {
    let names: string[] = [];
    let i: i32 = 0;
    while (i < items.len()) { names = names.append(items[i].name); i = i + 1; }
    let r: Reg = reg_via(names);
    let hits: i32 = 0;
    let j: i32 = 0;
    while (j < r.rows.len()) { if (r.rows[j].len() == want.len()) { hits = hits + 1; } j = j + 1; }
    return hits + names.len();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let a: string[] = []; a = a.append("w" + digit(i % 10)); a = a.append("q" + digit(i % 7)); acc = acc + a.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let items: Item[] = [];
    let i: i32 = 0;
    while (i < 10) { items = items.append(Item { name: label(i) }); i = i + 1; }
    if (probe(items, "row7") != 20) { return 97; }
    if (churn(64) != 128) { return 97; }
    let k: i32 = 0;
    while (k < items.len()) { if (items[k].name != label(k)) { return 97; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// The control: the same shape with every element fresh keeps the walk.
	{"strarr-field-owned-param-store", `struct Item { name: string }
struct Reg { rows: string[], head: i32[] }
function digit(d: i32): string {
    if (d == 0) { return "0"; } if (d == 1) { return "1"; } if (d == 2) { return "2"; }
    if (d == 3) { return "3"; } if (d == 4) { return "4"; } if (d == 5) { return "5"; }
    if (d == 6) { return "6"; } if (d == 7) { return "7"; } if (d == 8) { return "8"; }
    return "9";
}
function label(i: i32): string { return "row" + digit(i); }
function reg_of(rows: string[]): Reg { let head: i32[] = []; let i: i32 = 0; while (i < rows.len()) { head = head.append(i); i = i + 1; } return Reg { rows: rows, head: head }; }
function probe(items: Item[], want: string): i32 {
    let names: string[] = [];
    let i: i32 = 0;
    while (i < items.len()) { names = names.append(label(i)); i = i + 1; }
    let r: Reg = reg_of(names);
    let hits: i32 = 0;
    let j: i32 = 0;
    while (j < r.rows.len()) { if (r.rows[j].len() == want.len()) { hits = hits + 1; } j = j + 1; }
    return hits + names.len();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let a: string[] = []; a = a.append("w" + digit(i % 10)); a = a.append("q" + digit(i % 7)); acc = acc + a.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let items: Item[] = [];
    let i: i32 = 0;
    while (i < 10) { items = items.append(Item { name: label(i) }); i = i + 1; }
    if (probe(items, "row7") != 20) { return 97; }
    if (churn(64) != 128) { return 97; }
    let k: i32 = 0;
    while (k < items.len()) { if (items[k].name != label(k)) { return 97; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostStrArrFieldBufferReleaseIRX86_64 drives the cases through the
// self-hosted x86-64 compiler, with the leak census on.
func TestSelfHostStrArrFieldBufferReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldBufferReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			stderr, code := hevRun(t, runner, bin)
			if code != tc.want {
				t.Errorf("%s = %d, want %d (a small non-zero is the leaked bytes per round; 98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}

// TestSelfHostStrArrFieldBufferReleaseIRArm64 is the arm64 leg. Both helper
// bodies are hand-transcribed per backend rather than shared, which is why this
// divergence existed at all — the register backends agreed with each other and
// not with wasm.
func TestSelfHostStrArrFieldBufferReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldBufferReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (a small non-zero is the leaked bytes per round; 98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldBufferReleaseWasmIR is the wasm leg. It read 0 on the
// first four cases before the register backends were changed; the base-copy
// case is the one it failed on its own (#8119), with the register backends
// already green, until its struct-drop walk moved onto the shared classifier.
func TestSelfHostStrArrFieldBufferReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[]-field buffer-release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strArrFieldBufferReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (a small non-zero is the leaked bytes per round; 98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldBorrowedElemsSanitizeX86_64 runs the borrowed-element
// rows (#10405) under FERN_SANITIZE. It may not release an
// element box `items` still holds. The sanitizer's leak line is not asserted:
// `Item.name` escaping into `names` withholds that type's field reclaim, which
// is the admission's sound leak.
func TestSelfHostStrArrFieldBorrowedElemsSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	rows := []string{"strarr-field-caller-array-co-owner-len", "strarr-field-borrowed-local-store", "strarr-field-borrowed-forwarded-param", "strarr-field-owned-param-store"}
	for _, tc := range strArrFieldBufferReleaseCases {
		if !slices.Contains(rows, tc.name) {
			continue
		}
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(tc.src+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != tc.want || strings.Contains(stderr, "use-after-free") || strings.Contains(stderr, "over-release") {
				t.Fatalf("exit = %d, want %d, with no use-after-free or over-release\n%s", exit, tc.want, stderr)
			}
		})
	}
}
