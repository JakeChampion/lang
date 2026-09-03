package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- map alias groups: who owns the box a clone superseded (#7235) ----------
//
// A mapbox carries no rc word, so `var q: Map[..] = m` is an UNCOUNTED share and
// the release of every box the pair reaches has to be decided statically. Before
// this, an aliased map lost its "MAP:" credit outright — nothing freed either
// box — and the clone `m = m.insert(k, v)` takes when an alias is live left the
// receiver's old box with no owner at all: 192 B/round on the repro, native 0.
//
// THE MODEL: m, its plain aliases and the local tuples holding it at an element
// form a GROUP. Every release in the group is guarded by identity — a holder
// frees its box only when no holder released after it still holds the same
// pointer, and the clone site frees the superseded box only when no holder at
// all does. m is released last with its deep column credit; every other release
// is the shallow free, so an element two boxes share is freed once, by m. A
// conditional clone and a clone inside a loop are both exact under that rule,
// which the counts here pin per shape.
//
// THE CLONE DECISION moved with it: an insert clones only when an alias can be
// LIVE at it — bound earlier in lowering order, or anywhere inside a loop
// enclosing the insert (the loop's back edge carries an alias bound textually
// after the insert into the next iteration). `alias_in_enclosing_loop_snapshot`
// is the wrong-answer case for a positional scan: every snapshot must hold the
// box it was taken from, and a scan that stops at the current statement hands
// all four the same box and answers 12 for 6.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.
// Counts are the x86-64 leakcheck census at the row's round count; the two
// STRING rows pin the residue their no-alias controls leak identically (a
// string-column gap this change does not touch), so an alias-side regression
// shows as a count move against a flat control.

type mapAliasGroupCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func mapAliasGroupMain(rounds string) string {
	return "\nfunction main(): i32 { var x: i32 = 0; var r: i32 = 0; " +
		"while (r < " + rounds + ") { x = x + round(r); r = r + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"
}

func mapAliasGroupCases() []mapAliasGroupCase {
	const repro = `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var q: Map[string, i32] = m;
    m = m.insert("k", i);
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}`
	return []mapAliasGroupCase{
		{
			// #7235's repro. The alias holds the old box, m the clone; the
			// sweep releases q (old != m) and then m. Base: 1200/600, 19200 live.
			name: "alias_before_insert",
			src:  repro + mapAliasGroupMain("100"),
			want: 64, allocs: 1200, frees: 1200,
		},
		{
			// The same at 200 rounds — flat, not merely smaller. Base: 2400/1200, 38400.
			name: "alias_before_insert_200",
			src:  repro + mapAliasGroupMain("200"),
			want: 43, allocs: 2400, frees: 2400,
		},
		{
			// No alias is live at the insert, so it mutates in place and no
			// clone exists; q then shares the one box and the guard skips it.
			// Allocs DROP from 1200 (the clone and its snapshots) to 700. Base:
			// 1200/600, 19200 live.
			name: "alias_after_insert",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    var q: Map[string, i32] = m;
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 17, allocs: 700, frees: 700,
		},
		{
			// The clone happens on half the rounds: q holds the old box on
			// those and m's box on the rest, and the identity guard tells the
			// two apart at runtime. Base: 850/400, 12800 live.
			name: "alias_and_conditional_insert",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var q: Map[string, i32] = m;
    if (i % 2 == 0) { m = m.insert("k", i); }
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 11, allocs: 850, frees: 850,
		},
		{
			// Three clones per round; the clone site frees each intermediate
			// clone (held by nobody) and keeps the first box for q. Base:
			// 2600/1400, 44800 live — the worst shape measured.
			name: "alias_before_a_loop_of_inserts",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var q: Map[string, i32] = m;
    var k: i32 = 0;
    while (k < 3) { m = m.insert("k", i + k); k = k + 1; }
    return (q.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 82, allocs: 2600, frees: 2600,
		},
		{
			// Two aliases bound around two inserts: q keeps the first box, r
			// the first clone, m the second. Each alias is guarded against the
			// holders after it, so three distinct boxes free once each.
			name: "two_aliases_two_inserts",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var q: Map[string, i32] = m;
    m = m.insert("k", i);
    var r: Map[string, i32] = m;
    m = m.insert("k", i + 1);
    return (q.get_or("k", 0) + r.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 26, allocs: 2000, frees: 2000,
		},
		{
			// THE WRONG-ANSWER CASE for a positional alias scan (#7235's loop
			// comment). The alias is bound textually AFTER the insert, but the
			// loop's back edge makes it live at the next iteration's insert, so
			// every insert must clone and each snapshot keeps its own box:
			// 0+1+2+3 = 6. In-place would hand all four the same box: 12. The
			// alias escapes into `seen`, so the group is refused and the counts
			// pin today's leak; the answer is the gate.
			name: "alias_in_enclosing_loop_snapshot",
			src: `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    var seen: Map[string, i32][] = [];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        m = m.insert("k", i);
        var alias: Map[string, i32] = m;
        seen = seen.append(alias);
        i = i + 1;
    }
    var j: i32 = 0;
    while (j < seen.len()) { acc = acc + seen[j].get_or("k", 0); j = j + 1; }
    return acc + __rc_underflow_count() * 100;
}`,
			want: 6, allocs: 37, frees: 22,
		},
		{
			// A string VALUE column. The clone shares the column's pointers
			// uncounted, so m's sweep alone takes the deep free and the alias
			// the shallow one. The 100-block residue is the no-alias control's
			// own (measured identically without `q`): a string-column gap, not
			// a group one. Base: 1300/600, 22400 live.
			name: "string_values",
			src: `import "core/map";
import "std/i32";
function round(i: i32): i32 {
    var m: Map[string, string] = map_new(4);
    var q: Map[string, string] = m;
    m = m.insert("k", i.to_string());
    return (q.get_or("k", "").len() + m.get_or("k", "").len() + i) % 91;
}` + mapAliasGroupMain("100"),
			want: 72, allocs: 1300, frees: 1200,
		},
		{
			// Fresh string KEYS as well, one insert in place before the alias
			// and one clone after it. Same rule, same control-owned residue
			// (500 blocks without `q`).
			name: "string_keys",
			src: `import "core/map";
import "std/i32";
function round(i: i32): i32 {
    var m: Map[string, string] = map_new(4);
    m = m.insert(i.to_string(), i.to_string());
    var q: Map[string, string] = m;
    m = m.insert((i + 1).to_string(), (i + 1).to_string());
    return (q.len() + m.len() + m.get_or(i.to_string(), "").len() + i) % 91;
}` + mapAliasGroupMain("100"),
			want: 16, allocs: 1800, frees: 1300,
		},
		{
			// The #7212 residue: the tuple holds the old box at an element,
			// released ahead of the array sweep that frees the tuple box it is
			// read through. Base: 1300/1000, 6400 live (64 B/round).
			name: "tuple_element",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var t: (i32, Map[string, i32]) = (i, m);
    m = m.insert("k", i);
    return (t.0 + t.1.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 17, allocs: 1300, frees: 1300,
		},
		{
			// CONTROL: never aliased. Unchanged — the in-place insert and the
			// plain sweep, byte for byte.
			name: "never_aliased",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    return m.get_or("k", 0) % 91;
}` + mapAliasGroupMain("100"),
			want: 64, allocs: 600, frees: 600,
		},
		{
			// REFUSED: an alias declared inside a loop body. Its re-declaration
			// would run a guard the holders declared after it are absent from,
			// so the group is not admitted and the map keeps no credit. Pinned
			// at today's leak; MORE frees here means the loop rule weakened.
			name: "alias_declared_in_loop_refused",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 2) { var q: Map[string, i32] = m; m = m.insert("k", i + k); acc = acc + q.get_or("k", 0); k = k + 1; }
    return (acc + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 26, allocs: 2000, frees: 1100,
		},
		{
			// REFUSED: a chain. `var r = q` is an escape of q, so q is not a
			// holder and the group — all or nothing — is refused with it.
			name: "alias_chain_refused",
			src: `import "core/map";
function round(i: i32): i32 {
    var m: Map[string, i32] = map_new(4);
    var q: Map[string, i32] = m;
    var r: Map[string, i32] = q;
    m = m.insert("k", i);
    return (q.get_or("k", 0) + r.get_or("k", 0) + m.get_or("k", 0)) % 91;
}` + mapAliasGroupMain("100"),
			want: 64, allocs: 1300, frees: 700,
		},
	}
}

// TestSelfHostMapAliasGroupX86_64 — the census leg, plus a second build of each
// row under FERN_SANITIZE=1: an identity guard that frees a box another holder
// still reads is an over-release into a freelist, which the census cannot see.
func TestSelfHostMapAliasGroupX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapAliasGroupCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "mapgrp_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; a wrong sum means an "+
					"insert mutated a box an alias still read)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d (MORE on an in-place row means a clone "+
					"came back; FEWER on a clone row means one stopped)", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means a holder lost its release; "+
					"MORE on a refused row means the group admitted a shape it must decline",
					tc.name, summary, tc.frees)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "mapgrp_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

// TestSelfHostMapAliasGroupWasmIR — the wasm sibling. Exit codes only: the
// answer is what proves each insert cloned exactly when an alias was live.
func TestSelfHostMapAliasGroupWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map alias-group wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapAliasGroupCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "mapgrp_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("map alias-group wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostMapAliasGroupIRArm64 — the arm64 sibling under qemu.
func TestSelfHostMapAliasGroupIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapAliasGroupCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mapgrp_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
