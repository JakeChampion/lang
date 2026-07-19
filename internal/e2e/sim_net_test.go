package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SimNet (docs/DST-PLATFORM-BRIEF.md slice 2) is std/sim's scripted
// request/response layer — the sim sibling of fetch.fetch_future:
// endpoints registered on a sim.Net with per-endpoint body, first-byte
// latency, and a chunking schedule, fetched through futures that honour
// the real fetch contract (body on success, "" for a dead upstream,
// one re-suspension per chunk). These gates pin that handler fan-out
// logic is testable against scripted upstreams with EXACT virtual-time
// assertions, on every backend including the interpreter.

// `examples/tests/sim_net_test.fern` is the TAP suite: input-order
// gather over three latencies, race picking the fast endpoint,
// with_deadline dropping only the slow one, chunk accumulation +
// per-chunk re-suspension, the dead-upstream "" contract, and the
// per-endpoint hits counter / wildcard path. Passing → exit 0.
func TestRunnerSimNetExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/sim_net_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/sim net", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// simNetNativeProgram exercises the same SimNet contracts as the TAP
// suite without std/test (whose fs assertion helpers keep every TAP
// file interp/self-host-gated — #5372). Exit 42 iff every check holds.
const simNetNativeProgram = `import "std/async";
import "std/sim";

function main(): i32 {
    var d: sim.Sim = sim.new(1);
    var n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "primary", 30000000 as i64);
    n = n.serve(2, 80, "/k", "cache", 10000000 as i64);
    n = n.serve(3, 80, "/k", "mirror", 20000000 as i64);
    var fs: async.Future[string][] = [
        n.fetch_future(1, 80, "/k"),
        n.fetch_future(2, 80, "/k"),
        n.fetch_future(3, 80, "/k"),
        n.fetch_future(9, 80, "/k")
    ];
    var got: string[] = async.gather_on(d, fs, "!");
    if (got[0] != "primary" || got[1] != "cache" || got[2] != "mirror") { return 1; }
    if (got[3] != "") { return 2; }
    if (d.now_ns() != 30000000) { return 3; }
    if (n.hits(2, 80, "/k") != 1) { return 4; }
    if (n.hits(9, 80, "/k") != 0) { return 5; }

    var rd: sim.Sim = sim.new(1);
    var rn: sim.Net = sim.net(rd);
    rn = rn.serve(1, 80, "/k", "slow", 40000000 as i64);
    rn = rn.serve(2, 80, "/k", "fast", 10000000 as i64);
    var rf: async.Future[string][] = [
        rn.fetch_future(1, 80, "/k"),
        rn.fetch_future(2, 80, "/k")
    ];
    var (w, v) = async.race_on(rd, rf, "!");
    if (w != 1 || v != "fast") { return 6; }
    if (rd.now_ns() != 10000000) { return 7; }

    var dd: sim.Sim = sim.new(7);
    var dn: sim.Net = sim.net(dd);
    dn = dn.serve(1, 80, "/k", "late", 40000000 as i64);
    dn = dn.serve(2, 80, "/k", "early", 10000000 as i64);
    var df: async.Future[string][] = [
        dn.fetch_future(1, 80, "/k"),
        dn.fetch_future(2, 80, "/k")
    ];
    var dl: Option[string][] = async.with_deadline_on(dd, 25, df);
    match (dl[0]) { Some(x) => { return 8; }, None => { } }
    match (dl[1]) { Some(x) => { if (x != "early") { return 9; } }, None => { return 10; } }
    if (dd.now_ns() != 25000000) { return 11; }

    var cd: sim.Sim = sim.new(1);
    var cn: sim.Net = sim.net(cd);
    cn = cn.serve_chunked(1, 80, "/big", "abcdefghij", 5000000 as i64, 5000000 as i64, sim.chunks_of(10, 4));
    var cf: async.Future[string][] = [cn.fetch_future(1, 80, "/big")];
    var cb: string[] = async.gather_on(cd, cf, "!");
    if (cb[0] != "abcdefghij") { return 12; }
    if (cd.now_ns() != 15000000) { return 13; }
    return 42;
}
`

// SimNet programs are pure computation (no fds, no clock syscalls), so
// the identical program must produce the identical run on every backend
// — the determinism contract is cross-backend, not just cross-run.
func TestSimNetNativeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "simnet.fern")
	if err := os.WriteFile(srcPath, []byte(simNetNativeProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "simnet.bin")
	if o, err := exec.Command(bin, "-target", "x86-64", "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("x86-64 build of a SimNet program failed: %v\n%s", err, o)
	}
	cmd := runX86Bin(qemu, out)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("native SimNet exit = %d, want 42 (failing check index)", code)
	}
}

func TestWASMSimNet(t *testing.T) {
	if code := runWasm(t, simNetNativeProgram); code != 42 {
		t.Errorf("wasm SimNet exit = %d, want 42 (failing check index)", code)
	}
}
