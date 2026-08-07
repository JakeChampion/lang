package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Heap-bump FIXPOINTS for the self-host x86-64 IR path's map reclaim —
// #4357's self-host twin (the native side landed in #5096). Two fixes pinned:
//
//   - the COW-threaded loop shape `var m = map_new(8); m = m.insert(..)`
//     keeps its "MAP:" reclaim credit (map_nonself_reassigned admits the
//     sanctioned self-cow reassign; any other rebind still disqualifies), and
//   - `m.get_or(k, d)` no longer leaks its intermediate: __fern_map_get
//     allocates a raw 16-byte Option box that the map_get_or emission
//     consumes on the spot, so the box now goes straight back to the
//     size-class-2 freelist. Without it ~16 B leaks per call on both the
//     hit and miss paths, unbounded in a loop (m.has() never leaked).
//
// Fixpoint contract: growth at N=50 == growth at N=5000, non-zero, under a
// hard leak guard. The fixed-exit cases pin value-correctness churn and the
// alias negative (`var x = m.insert(..)` must keep m EXCLUDED from reclaim —
// the identity-smuggle UAF guard — while staying value-correct).
// self_host_map_reclaim_ir_test.go keeps the value-only reclaim cases; these
// are the bump-scaling twins.
var mapFixpointIRCases = []struct {
	name  string
	src   func(n string) string
	fixed bool
	want  int
}{
	// A cow-threaded map DECLARED INSIDE a loop body: the loop-reinit drop
	// (emit_map_buffers_free before the shared store) frees the prior
	// iteration's box, so the loop's high-water is one box wide. Without the
	// reinit drop it leaks one box per iteration: precise_drop_names is
	// top-level-only, so no early drop fires for a loop-declared map.
	{name: "cow-loop-getor", src: func(n string) string {
		return `import "core/map";
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) {
        var m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc < 0) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "straightline-cow-getor", src: func(n string) string {
		return `import "core/map";
function step2(k: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m.get_or(k, 0) + m.get_or(k + 1, 0);
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step2(i); i = i + 1; }
    if (acc < 0) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "per-call-getor", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m.get_or(k, 7);
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc < 0) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "getor-miss-path", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    var m: Map[i32, i32] = map_new(8);
    return m.get_or(k, 7);
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + n + ` * 7) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// Bound-from-call: `var m: Map[..] = mk(i)` where mk is a registered
	// builder ("MAPF:" — map_fresh_ret_fns_of) earns the same reclaim credit
	// as a local map_new, so the loop-reinit drop and end-of-scope free fire
	// on the binding. A collector that credits only literal map_new inits
	// leaks every mk() box.
	{name: "mkcall-loop-getor", src: func(n string) string {
		return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) {
        var m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc != ` + n + ` * (` + n + ` - 1)) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "mkcall-straightline", src: func(n string) string {
		return `import "core/map";
function mk2(k: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function step(k: i32): i32 {
    var m: Map[i32, i32] = mk2(k);
    return m.get_or(k, 0) + m.get_or(k + 1, 0);
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc < 0) { return 121; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// A DISCARDED builder call (`mk(i);` as a bare statement) has no binding,
	// so no slot reclaim fires and the whole fresh map leaks per call. The
	// "MAPRET:" seeding (from the MAPF registry, the map
	// sibling of TUPRET) now frees the result on the spot via
	// __fern_map_free.
	{name: "mkcall-discard", src: func(n string) string {
		return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    var g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "value-churn", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 300) {
        var m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        m = m.insert(i + 1, i * 3);
        acc = acc + m.get_or(i, 0) + m.get_or(i + 1, 0);
        i = i + 1;
    }
    if (acc != 5 * (300 * 299 / 2)) { return 121; }
    return 0;
}`
	}},
	{name: "alias-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(8);
    var x: Map[i32, i32] = m.insert(1, 11);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { acc = acc + x.get_or(1, 0) + m.get_or(1, 0); i = i + 1; }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
	// Param-shaped "builders" must stay OFF the "MAPF:" registry: feed
	// returns its param (rejected — the returned name is a param, not a
	// fresh local decl), grow's binding init is a method call on a param
	// (rejected — not is_fresh_map_init). If either were credited, the
	// caller's reclaim would free base's live buffers → value corruption.
	{name: "mkcall-param-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function feed(m: Map[i32, i32]): Map[i32, i32] {
    return m;
}
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    var t: Map[i32, i32] = m.insert(k, k * 2);
    return t;
}
function main(): i32 {
    var base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var x: Map[i32, i32] = feed(base);
        var y: Map[i32, i32] = grow(base, i + 2);
        acc = acc + x.get_or(1, 0) + y.get_or(1, 0);
        i = i + 1;
    }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
	// A discarded call to a NON-builder map-returning function must NOT be
	// freed: feed returns its param, so freeing the discarded result would
	// free base's live box + buffers out from under the loop.
	{name: "discard-param-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function feed(m: Map[i32, i32]): Map[i32, i32] {
    return m;
}
function main(): i32 {
    var base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { feed(base); acc = acc + base.get_or(1, 0); i = i + 1; }
    if (acc != 200 * 11) { return 121; }
    return 0;
}`
	}},
	// A NESTED `return p;` hides from a top-level-only return scan: pick's
	// only top-level return is the bare fresh local, but the k<0 arm hands
	// out the param. map_builder_body_ok rejects any statement containing a
	// nested return (body_has_return), so pick stays uncredited — otherwise
	// the caller's loop-reinit drop would free base every iteration.
	{name: "mkcall-nested-return-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function pick(p: Map[i32, i32], k: i32): Map[i32, i32] {
    if (k < 0) { return p; }
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    var base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var m: Map[i32, i32] = pick(base, 0 - 1);
        acc = acc + m.get_or(1, 0) + base.get_or(1, 0);
        i = i + 1;
    }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
}

// TestSelfHostMapFixpointIRX86_64 runs the shapes through the self-hosted
// x86-64 IR driver (asm_run). Fixpoint cases assert growth(N=50) ==
// growth(N=5000), non-zero, under the leak guard; fixed cases assert their
// exact exit (121 = value mismatch, 119 = leak guard).
func TestSelfHostMapFixpointIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	sh := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range mapFixpointIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixed {
				if code := sh(t, tc.name, tc.src("")); code != tc.want {
					t.Errorf("%s: exited %d, want %d (121=value mismatch, 119=leak guard)", tc.name, code, tc.want)
				}
				return
			}
			small := sh(t, tc.name+"-50", tc.src("50"))
			large := sh(t, tc.name+"-5000", tc.src("5000"))
			if small != large {
				t.Errorf("%s: high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: growth is 0 — probe does not allocate", tc.name)
			}
			if small >= 119 {
				t.Errorf("%s: leak guard tripped (%d)", tc.name, small)
			}
		})
	}
}
