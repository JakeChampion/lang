package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- A map local borrowed by a TUPLE element keeps its reclaim (#7212) -------
//
// `var t: (i32, Map[K, V]) = (i, m)` mentions `m` at a container-element
// position, so both map-reclaim gates refused it: alias_idents_in_value credited
// the alias and expr_unsafe_for called the element an escape. Nothing on the
// tuple side took over either — construction emits no rc_inc for a map slot, and
// both tuple child-drop walks skip a bare-ident element — so the mapbox ended up
// with no owner at all:
//
//	(i, m), churn 200 rounds   allocs=1000 frees=400  live_bytes=12800   (64 B/round)
//	the same map with no tuple allocs=1600 frees=1597 live_bytes=64      (flat)
//
// against 0 on native for both. map_tuple_elem_borrow_only restores the credit
// when the tuple is the ONLY thing aliasing the map and cannot outlive it.
//
// Exactly ONE release is owed. The tuple holds a BARE pointer — no retain at
// construction — so `m` and `t.1` are one box at rc 1, and this is NOT the
// string interlock of #7184 where construction retained to rc 2 and both a
// local credit and an element release were required. Releasing from both sides
// here would double-free, which is why the element side is untouched and why
// every case below folds `__rc_underflow_count()` into its exit code: a byte
// delta alone cannot tell a fixed leak from an over-release.

// mapTupleElemChurn wraps a loop body in the churn/`main` shape every case
// shares. `rounds` is what the flatness assertion varies; the underflow term is
// what turns an over-release into an exit code no `want` can collide with.
func mapTupleElemChurn(prelude, body string, rounds int) string {
	return fmt.Sprintf(`import "core/map";
%sfunction churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
%s
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return churn(%d) + __rc_underflow_count() * 100; }
`, prelude, body, rounds)
}

// mapTupleElemFlatCases are the shapes the credit now covers. Each must be FLAT
// — the same live_bytes at 100 and 200 rounds — where the parent leaked 64 B per
// round per map. Both `want`s of every case were adjudicated against BOTH
// oracles (`bin/fern -interp` and the native x86-64 backend), never read off the
// self-host run under test.
var mapTupleElemFlatCases = []struct {
	name    string
	prelude string
	body    string
	want100 int
	want200 int
}{
	{
		// The shape itself: a fresh map at a tuple element, read back through
		// the tuple. The tuple is built AFTER the map's last direct mention, so
		// a PRECISE drop on `m` would free it before `t.1` is read — which is
		// why only the exit-set gate is overridden and precise_drop_names is
		// left refusing.
		name: "basic",
		body: `        var m: Map[string, i32] = map_new(4);
        var t: (i32, Map[string, i32]) = (i, m);
        acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		// The map at element 0. Nothing in the gate keys on position; this pins
		// that.
		name: "pos0",
		body: `        var m: Map[string, i32] = map_new(4);
        var t: (Map[string, i32], i32) = (m, i);
        acc = (acc + t.1 + t.0.len()) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		// Two maps in one tuple: two names, two boxes, two releases. The gate
		// checks EVERY Map-typed position of the host, not just the one the
		// call is about, so a second map cannot ride in on the first's proof.
		name: "two_maps",
		body: `        var a: Map[string, i32] = map_new(4);
        var b: Map[string, i32] = map_new(4);
        var t: (Map[string, i32], Map[string, i32]) = (a, b);
        acc = (acc + i + t.0.len() + t.1.len()) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		// The SAME map at both positions. One box, two uncounted pointers, one
		// release — an over-release here would show as +100 in the exit code.
		name: "same_map_twice",
		body: `        var m: Map[string, i32] = map_new(4);
        var t: (Map[string, i32], Map[string, i32]) = (m, m);
        acc = (acc + i + t.0.len() + t.1.len()) % 91;`,
		want100: 36,
		want200: 62,
	},
	{
		// A tuple that ALSO carries a fresh array literal, so it is credited
		// "TUPRC:" and emit_tuple_child_drops runs over it. That walk must free
		// the array and still skip the map, or the map is released twice.
		name: "arr_and_map",
		body: `        var m: Map[string, i32] = map_new(4);
        var t: (i32[], Map[string, i32]) = ([i, i + 1], m);
        acc = (acc + t.0.len() + t.1.len()) % 91;`,
		want100: 18,
		want200: 36,
	},
	{
		// The host in an `if` body, a `match` arm, and a `for` body in turn —
		// the three nested arms of used_only_as_tuple_elem. Each recurses before
		// it skips, so a use in the enclosing tail is still seen.
		name: "if_host",
		body: `        if (i % 2 == 0) {
            var m: Map[string, i32] = map_new(4);
            var t: (i32, Map[string, i32]) = (i, m);
            acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;
        } else { acc = (acc + 1) % 91; }`,
		want100: 43,
		want200: 81,
	},
	{
		name: "match_host",
		body: `        var o: Option[i32] = Some(i);
        match (o) {
            Some(v) => {
                var m: Map[string, i32] = map_new(4);
                var t: (i32, Map[string, i32]) = (v, m);
                acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;
            },
            None => { acc = acc + 1; }
        }`,
		want100: 36,
		want200: 62,
	},
	{
		// The map ALSO passed as a bare call argument. expr_unsafe_for already
		// reads a borrowable param position as a borrow, so the tuple was the
		// only thing costing this shape its credit.
		name:    "borrow_call_arg",
		prelude: "function tlen(mm: Map[string, i32]): i32 { return mm.len(); }\n",
		body: `        var m: Map[string, i32] = map_new(4);
        var t: (i32, Map[string, i32]) = (i, m);
        acc = (acc + t.0 + tlen(m)) % 91;`,
		want100: 36,
		want200: 62,
	},
}

// mapTupleElemHazardCases are the shapes the gate must keep REFUSING. They
// assert BEHAVIOUR, not bytes: each of the first three SEGFAULTED against a
// version of this change that had the alias/escape override but no
// tuple_pos_borrow_only, because the map box left through `t.1` and the local's
// sweep then freed it under its new owner. A leak here is the correct outcome.
var mapTupleElemHazardCases = []struct {
	name string
	src  string
	want int
}{
	{
		// The element is extracted and RETURNED. `t.1` is a field read on an
		// ident, which expr_unsafe_for calls a borrow, so the host looks
		// non-escaping — only the positional check sees this.
		name: "payload_returned",
		src: `import "core/map";
function pick(i: i32): Map[string, i32] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    var t: (i32, Map[string, i32]) = (i, m);
    return t.1;
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var g: Map[string, i32] = pick(i); acc = (acc + g.get_or("k", 0)) % 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(60) + __rc_underflow_count() * 100; }
`,
		want: 41,
	},
	{
		// The same escape routed through a local first.
		name: "payload_extracted_escapes",
		src: `import "core/map";
function pick(i: i32): Map[string, i32] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", i);
    var t: (i32, Map[string, i32]) = (i, m);
    var keep: Map[string, i32] = t.1;
    return keep;
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var g: Map[string, i32] = pick(i); acc = (acc + g.get_or("k", 0)) % 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(60) + __rc_underflow_count() * 100; }
`,
		want: 41,
	},
	{
		// Extracted but never escaping. Refused anyway: the positional check
		// asks whether the box gets a second NAME, not whether that name
		// outlives the frame — the cheap conservative reading.
		name: "payload_extracted_local",
		src: mapTupleElemChurn("", `        var m: Map[string, i32] = map_new(4);
        var t: (i32, Map[string, i32]) = (i, m);
        var keep: Map[string, i32] = t.1;
        acc = (acc + t.0 + keep.len()) % 91;`, 60),
		want: 41,
	},
	{
		// An identity-carrying method reached THROUGH the tuple: insert hands
		// back the receiver's own box, so `mm` is a third name for it.
		// map_recv_borrows is a whitelist precisely so this stays out.
		name: "identity_through_tuple",
		src: mapTupleElemChurn("", `        var m: Map[string, i32] = map_new(4);
        var t: (i32, Map[string, i32]) = (i, m);
        var mm: Map[string, i32] = t.1.insert("k", i);
        acc = (acc + t.0 + mm.get_or("k", 0)) % 91;`, 60),
		want: 82,
	},
	{
		// The tuple itself is moved out by return.
		name: "tuple_returned",
		src: `import "core/map";
function mk(i: i32): (i32, Map[string, i32]) {
    var m: Map[string, i32] = map_new(4);
    var t: (i32, Map[string, i32]) = (i, m);
    return t;
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) { var p: (i32, Map[string, i32]) = mk(i); acc = (acc + p.0 + p.1.len()) % 91; i = i + 1; }
    return acc;
}
function main(): i32 { return churn(60) + __rc_underflow_count() * 100; }
`,
		want: 41,
	},
	{
		// A second local alias of the map, alongside the tuple. The override is
		// for tuple-element positions ONLY; anything else still excludes.
		name: "second_alias",
		src: mapTupleElemChurn("", `        var m: Map[string, i32] = map_new(4);
        var t: (i32, Map[string, i32]) = (i, m);
        var q: Map[string, i32] = m;
        acc = (acc + t.0 + q.len()) % 91;`, 60),
		want: 41,
	},
	{
		// No tuple ANNOTATION, so nothing says which positions are maps.
		name: "unannotated_host",
		src: mapTupleElemChurn("", `        var m: Map[string, i32] = map_new(4);
        var t = (i, m);
        acc = (acc + t.0 + t.1.len()) % 91;`, 60),
		want: 41,
	},
	{
		// The host is REASSIGNED, so the tuple binding is not the only thing
		// that decides what the box is reachable from.
		name: "host_reassigned",
		src: mapTupleElemChurn("", `        var m: Map[string, i32] = map_new(4);
        var t: (i32, Map[string, i32]) = (i, m);
        t = (i + 1, m);
        acc = (acc + t.0 + t.1.len()) % 91;`, 60),
		want: 10,
	},
	{
		// A `for (k, v) in m` alongside the tuple. Iteration has its own
		// exclusion and it is not one this override touches.
		name: "map_iterated",
		src: mapTupleElemChurn("", `        var m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        var t: (i32, Map[string, i32]) = (i, m);
        for (kk, vv) in m { acc = (acc + vv) % 91; }
        acc = (acc + t.0) % 91;`, 60),
		want: 82,
	},
	{
		// The host in a `for` body. The map IS reclaimed here — this is in the
		// hazard table rather than the flat one because the tuple BOX is not:
		// a `(i32, Map[K, V])` tuple is neither all-scalar ("TUP:") nor
		// carrying an rc child ("TUPRC:"), so it earns no box reclaim of its
		// own, and the something-else that frees it at the other nesting levels
		// does not reach a for-body local. Measured 208 B/round before, 80 B
		// after; the no-tuple control at the same nesting is flat, so the
		// residue is the box, not the map.
		name: "for_host_box_residue",
		src: mapTupleElemChurn("", `        for k in 0 .. 2 {
            var m: Map[string, i32] = map_new(4);
            var t: (i32, Map[string, i32]) = (i + k, m);
            acc = (acc + t.0 + t.1.get_or("k", 0)) % 91;
        }`, 200),
		want: 51,
	},
}

// TestSelfHostMapTupleElemReclaimX86_64 is the leak gate: live_bytes must be the
// SAME at 100 and 200 rounds. An absolute ceiling would be a budget for the rest
// of the Perceus port rather than a gate on this shape — every case retains a
// small constant residue that the no-tuple control has too — so the assertion is
// that the residue does not scale with the loop.
//
// Non-vacuity: all eight cases fail this against the parent commit, at 64 B per
// round per map (128 for two_maps).
func TestSelfHostMapTupleElemReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapTupleElemFlatCases {
		t.Run(tc.name, func(t *testing.T) {
			live := func(rounds, want int) int64 {
				t.Helper()
				src := mapTupleElemChurn(tc.prelude, tc.body, rounds)
				asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
				name := fmt.Sprintf("maptupelem_%s_%d", tc.name, rounds)
				stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, name, asm))
				if exit != want {
					t.Fatalf("%s exited %d, want %d (a value >= 100 is an rc over-release, not a leak)",
						name, exit, want)
				}
				summary := ""
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, "leakcheck: ") {
						summary = line
					}
				}
				if summary == "" {
					t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
				}
				var allocs, frees, l int64
				if _, err := fmtSscan(summary, &allocs, &frees, &l); err != nil {
					t.Fatalf("%s: parse %q: %v", name, summary, err)
				}
				if allocs == 0 {
					t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
				}
				t.Logf("%s rounds=%d: %s", tc.name, rounds, summary)
				return l
			}
			l100, l200 := live(100, tc.want100), live(200, tc.want200)
			if l100 != l200 {
				t.Errorf("a map local at a tuple element leaks per round: live_bytes=%d at 100 "+
					"rounds and %d at 200. Nothing releases the mapbox — construction takes no "+
					"rc_inc and the tuple child-drops skip a bare ident, so the local owes the "+
					"one release (#7212)", l100, l200)
			}
		})
	}
}

// TestSelfHostMapTupleElemHazardsX86_64 pins the refusals. A wrong answer or a
// crash here means the map was freed while the tuple, an extracted local, or the
// caller still owned it — an over-release, not a leak. Each `want` came from the
// interpreter and the native backend agreeing.
func TestSelfHostMapTupleElemHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapTupleElemHazardCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			bin := buildBin(t, gcc, dir, "maptupelem_hazard_"+tc.name, asm)
			if _, exit := hevRun(t, runner, bin); exit != tc.want {
				t.Errorf("exited %d, want %d — %d+100 would be an rc over-release, and a "+
					"crash means the mapbox was freed under a second owner", exit, tc.want, tc.want)
			}
		})
	}
}

// mapTupleElemAllCases is every case above as a (name, src, want) triple, for
// the backend legs — which check the ANSWER and the over-release counter rather
// than bytes. The self-host x86-64 emitter is the only one that carries the
// leakcheck census (asm_ir.fern's leak_check_on has no arm64 or wasm sibling),
// so the byte gate lives in the test above and these two prove the same programs
// still compute the right thing everywhere.
func mapTupleElemAllCases() []struct {
	name string
	src  string
	want int
} {
	var out []struct {
		name string
		src  string
		want int
	}
	for _, tc := range mapTupleElemFlatCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, mapTupleElemChurn(tc.prelude, tc.body, 200), tc.want200})
	}
	for _, tc := range mapTupleElemHazardCases {
		out = append(out, struct {
			name string
			src  string
			want int
		}{tc.name, tc.src, tc.want})
	}
	return out
}

// TestSelfHostMapTupleElemReclaimArm64 runs them through the self-host arm64
// backend, which produces the finished binary itself (emit + assemble + link
// in-process).
func TestSelfHostMapTupleElemReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapTupleElemAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "maptupelem_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}

// TestSelfHostMapTupleElemReclaimWasm is the wasm leg. Every `want` is well
// under WASI's 126 ceiling, so an over-release (+100) is still expressible.
func TestSelfHostMapTupleElemReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-tuple-element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapTupleElemAllCases() {
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
			watFile := filepath.Join(dir, "maptupelem_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d (>= 100 is an rc over-release)", code, tc.want)
			}
		})
	}
}
