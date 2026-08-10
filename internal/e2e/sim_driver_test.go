package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/sim is the deterministic-simulation driver for std/async
// (docs/DST-PLATFORM-BRIEF.md slice 1): a virtual-clock, seeded-PRNG
// implementation of the async.Driver seam, driven through the
// gather_on / race_on / with_deadline_on combinator siblings. These
// gates pin the headline win: deadline-based async is testable under
// `-interp` — where fd-backed Pending futures never resolve on the
// real driver — with EXACT virtual-time assertions instead of sleeps.

// `examples/tests/sim_driver_test.fern` is the TAP suite: with_deadline
// winners/losers at exact virtual times, seed-deterministic race
// tie-breaks (incl. a 20-seed sweep), gather over out-of-order
// readiness, and the re-suspending chain shape. Passing → exit 0.
func TestRunnerSimDriverExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/sim_driver_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/sim driver", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// simDriverNativeProgram exercises the same virtual-clock contracts as
// the TAP suite without std/test (whose fs assertion helpers reference
// `remove_dir_all` and so keep every TAP file interp/self-host-gated —
// no examples/tests file compiles through the native CLI pipeline
// today). Exit 42 iff every check holds.
const simDriverNativeProgram = `import "std/async";
import "std/sim";

function tie_winner(seed: i64): i32 {
    var d: sim.Sim = sim.new(seed);
    var fs: async.Future[i32][] = [
        sim.future_at(d, 5000000, 100),
        sim.future_at(d, 5000000, 200)
    ];
    var (w, v) = async.race_on(d, fs, -1);
    if (w == 0 && v != 100) { return -2; }
    if (w == 1 && v != 200) { return -3; }
    return w;
}

function main(): i32 {
    var d: sim.Sim = sim.new(7);
    var fs: async.Future[string][] = [
        sim.future_at(d, 40000000, "late"),
        sim.future_at(d, 10000000, "early")
    ];
    var got: Option[string][] = async.with_deadline_on(d, 25, fs);
    match (got[0]) { Some(v) => { return 1; }, None => { } }
    match (got[1]) { Some(v) => { if (v != "early") { return 2; } }, None => { return 3; } }
    if (d.now_ns() != 25000000) { return 4; }
    if (tie_winner(1) != tie_winner(1)) { return 5; }
    // Two seeds picking opposite sides of a tie -- the pair is what proves the
    // tie-break is a real draw and not a fixed order. Re-recorded when sim's
    // generator moved to std/rand's PCG32 + Lemire rejection (#6193): the two
    // seeds swapped sides. Same-seed reproducibility (check 5) is unchanged.
    if (tie_winner(1) != 0) { return 6; }
    if (tie_winner(2) != 1) { return 7; }
    var g: sim.Sim = sim.new(1);
    var gf: async.Future[i32][] = [
        sim.future_at(g, 30000000, 10),
        sim.future_at(g, 10000000, 20),
        sim.future_at(g, 20000000, 30)
    ];
    var order: i32[] = async.gather_on(g, gf, -1);
    if (order[0] != 10 || order[1] != 20 || order[2] != 30) { return 8; }
    if (g.now_ns() != 30000000) { return 9; }
    var c: sim.Sim = sim.new(1);
    var cf: async.Future[i32][] = [sim.future_chain(c, 10000000, 5000000, 3, 9)];
    var res: i32[] = async.gather_on(c, cf, -1);
    if (res[0] != 9) { return 10; }
    if (c.now_ns() != 25000000) { return 11; }
    return 42;
}
`

// The sim programs are pure computation (no fds, no clock syscalls), so
// the identical program must produce the identical run on every
// backend — the determinism contract is cross-backend, not just
// cross-run.
func TestSimDriverNativeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "sim.fern")
	if err := os.WriteFile(srcPath, []byte(simDriverNativeProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "sim.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("x86-64 build of a std/sim program failed: %v\n%s", err, o)
	}
	cmd := runX86Bin(qemu, out)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("native std/sim exit = %d, want 42 (failing check index)", code)
	}
}

func TestWASMSimDriver(t *testing.T) {
	if code := runWasm(t, simDriverNativeProgram); code != 42 {
		t.Errorf("wasm std/sim exit = %d, want 42 (failing check index)", code)
	}
}
