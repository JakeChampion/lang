package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fault injection (docs/DST-PLATFORM-BRIEF.md slice 3) is std/sim's
// seed-driven fault layer on SimNet endpoints: fail (the real fetch's
// immediate-"" connect failure), stall (never resolves — exercises
// with_deadline drops), partial(k) (the first k chunks arrive on
// schedule, then silence — never resolves), and flaky(p) (each fetch
// draws once from the sim PRNG; the endpoint's fault fires when the
// draw lands below p). Every fault outcome is a pure function of the
// seed and the call order, and sim.sweep_seeds(n, prop) is the replay
// workflow in miniature: run a property over n seeds, report the first
// failing one.

// `examples/tests/sim_fault_test.fern` is the TAP suite: the four
// fault modes through gather / with_deadline / a hand drain, the
// seed-1 flaky(50) golden pattern, same-seed reproducibility +
// different-seed divergence, and sweep_seeds on both its all-green and
// first-failing-seed paths. Passing → exit 0.
func TestRunnerSimFaultExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/sim_fault_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/sim faults", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// simFaultNativeProgram exercises the same fault contracts as the TAP
// suite without std/test (whose fs assertion helpers keep every TAP
// file interp/self-host-gated — #5372). Exit 42 iff every check holds.
// The flaky(50) seed-1 pattern "SSSFFSSFSF" is the cross-backend
// determinism golden: pure integer arithmetic, so the identical
// program must produce the identical run on interp / native / wasm.
const simFaultNativeProgram = `import "std/async";
import "std/sim";

function flaky_pattern(seed: i64): string {
    var d: sim.Sim = sim.new(seed);
    var n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "body", 10000000 as i64);
    n = n.fault_flaky(1, 80, "/k", 50);
    var out: string = "";
    var i: i32 = 0;
    while (i < 10) {
        match (n.fetch_future(1, 80, "/k")) {
            Ready(v) => { out = out + "F"; },
            Pending(tok, c) => { out = out + "S"; },
        }
        i = i + 1;
    }
    return out;
}

function gather_shape_ok(seed: i64): boolean {
    var d: sim.Sim = sim.new(seed);
    var n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "alpha", 10000000 as i64);
    n = n.serve(2, 80, "/k", "beta", 20000000 as i64);
    n = n.serve(3, 80, "/k", "gamma", 5000000 as i64);
    n = n.fault_flaky(2, 80, "/k", 50);
    n = n.fault_stall(3, 80, "/k");
    var fs: async.Future[string][] = [
        n.fetch_future(1, 80, "/k"),
        n.fetch_future(2, 80, "/k"),
        n.fetch_future(3, 80, "/k")
    ];
    var got: string[] = async.gather_on(d, fs, "!");
    if (got.len() != 3) { return false; }
    if (got[0] != "alpha") { return false; }
    if (got[1] != "beta" && got[1] != "") { return false; }
    return got[2] == "!";
}

function flaky_first_call_ok(seed: i64): boolean {
    var d: sim.Sim = sim.new(seed);
    var n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "body", 10000000 as i64);
    n = n.fault_flaky(1, 80, "/k", 50);
    var ok: boolean = false;
    match (n.fetch_future(1, 80, "/k")) {
        Ready(v) => { },
        Pending(tok, c) => { ok = true; },
    }
    return ok;
}

function main(): i32 {
    var fd: sim.Sim = sim.new(1);
    var fn2: sim.Net = sim.net(fd);
    fn2 = fn2.serve(1, 80, "/k", "body", 10000000 as i64);
    fn2 = fn2.fault_fail(1, 80, "/k");
    match (fn2.fetch_future(1, 80, "/k")) {
        Ready(v) => { if (v != "") { return 1; } },
        Pending(t, c) => { return 2; },
    }
    if (fd.now_ns() != 0) { return 3; }
    if (fn2.hits(1, 80, "/k") != 1) { return 4; }

    var d: sim.Sim = sim.new(7);
    var n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "healthy", 10000000 as i64);
    n = n.serve(2, 80, "/k", "silent", 5000000 as i64);
    n = n.fault_stall(2, 80, "/k");
    var fs: async.Future[string][] = [
        n.fetch_future(1, 80, "/k"),
        n.fetch_future(2, 80, "/k")
    ];
    var got: Option[string][] = async.with_deadline_on(d, 25, fs);
    match (got[0]) { Some(v) => { if (v != "healthy") { return 5; } }, None => { return 6; } }
    match (got[1]) { Some(v) => { return 7; }, None => { } }
    if (d.now_ns() != 25000000) { return 8; }

    var gd: sim.Sim = sim.new(1);
    var gn: sim.Net = sim.net(gd);
    gn = gn.serve(1, 80, "/k", "ok", 10000000 as i64);
    gn = gn.serve(2, 80, "/k", "gone", 5000000 as i64);
    gn = gn.fault_stall(2, 80, "/k");
    var gfs: async.Future[string][] = [
        gn.fetch_future(1, 80, "/k"),
        gn.fetch_future(2, 80, "/k")
    ];
    var g: string[] = async.gather_on(gd, gfs, "!");
    if (g[0] != "ok" || g[1] != "!") { return 9; }

    var pd: sim.Sim = sim.new(1);
    var pn: sim.Net = sim.net(pd);
    pn = pn.serve_chunked(1, 80, "/big", "abcdefghij", 5000000 as i64, 5000000 as i64, sim.chunks_of(10, 4));
    pn = pn.fault_partial(1, 80, "/big", 2);
    var f: async.Future[string] = pn.fetch_future(1, 80, "/big");
    var toks: i32[] = [];
    var silent: boolean = false;
    var guard: i32 = 0;
    while (guard < 10 && !silent) {
        match (f) {
            Ready(v) => { return 10; },
            Pending(tok, resume) => {
                if (tok < 0) {
                    silent = true;
                } else {
                    toks = toks.append(tok);
                    f = resume(tok);
                }
            },
        }
        guard = guard + 1;
    }
    if (!silent) { return 11; }
    if (toks.len() != 2) { return 12; }
    if (toks[0] != 5 || toks[1] != 10) { return 13; }

    if (flaky_pattern(1) != "SSSFFSSFSF") { return 14; }
    if (flaky_pattern(9) != flaky_pattern(9)) { return 15; }
    if (flaky_pattern(1) == flaky_pattern(2)) { return 16; }

    if (sim.sweep_seeds(20, gather_shape_ok) != 0) { return 17; }
    if (sim.sweep_seeds(20, flaky_first_call_ok) != 2) { return 18; }
    return 42;
}
`

func TestSimFaultNativeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "simfault.fern")
	if err := os.WriteFile(srcPath, []byte(simFaultNativeProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "simfault.bin")
	if o, err := exec.Command(bin, "-target", "x86-64", "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("x86-64 build of a sim-fault program failed: %v\n%s", err, o)
	}
	cmd := runX86Bin(qemu, out)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("native sim-fault exit = %d, want 42 (failing check index)", code)
	}
}

func TestWASMSimFault(t *testing.T) {
	if code := runWasm(t, simFaultNativeProgram); code != 42 {
		t.Errorf("wasm sim-fault exit = %d, want 42 (failing check index)", code)
	}
}
