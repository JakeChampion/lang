package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWatEncode exercises the binary-wasm byte-emission primitives
// (examples/self_host/wat_encode.fern) — slice 4a of the self-hosted
// binary backend (section framing, vecs, names, valtypes, magic/version)
// the module-walker builds on.
//
// wat_encode.fern depends on leb128.fern's leb_u32 but is otherwise
// import-free, so the test concatenates leb128.fern + wat_encode.fern + a
// self-test main() that builds each primitive and asserts the bytes, then
// runs it through the self-host wasm pipeline (wasm_run -> WAT ->
// wasmtime). Exit 0 = all checks pass; a failing check returns its 1-based
// id.
func TestSelfHostWatEncode(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wat_encode e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	watbin, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	source := string(watbin) + "\n" + watEncodeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wat_encode self-test")
	}
	watPath := filepath.Join(dir, "wat_encode_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wat_encode self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// watEncodeSelfTestMain checks each byte-emission primitive: the module
// preamble, a name (LEB len + bytes), a section (id + LEB len + body), a
// vec (LEB count + elems), valtype codes, and raw concat. Each `return N`
// is a distinct failing-check id (0 = pass).
const watEncodeSelfTestMain = `
function main(): i32 {
    var m: i32[] = wmagic();
    if (m.len() != 8 || m[0] != 0 || m[1] != 97 || m[2] != 115 || m[3] != 109 || m[4] != 1) { return 1; }
    var nm: i32[] = wname([], "ab");
    if (nm.len() != 3 || nm[0] != 2 || nm[1] != 97 || nm[2] != 98) { return 2; }
    var sec: i32[] = wsection(1, [96]);
    if (sec.len() != 3 || sec[0] != 1 || sec[1] != 1 || sec[2] != 96) { return 3; }
    var v: i32[] = wvec(2, [127, 124]);
    if (v.len() != 3 || v[0] != 2 || v[1] != 127 || v[2] != 124) { return 4; }
    if (valtype_byte("i32") != 127 || valtype_byte("f64") != 124 || valtype_byte("i64") != 126) { return 5; }
    var c: i32[] = wcat([1, 2], [3, 4, 5]);
    if (c.len() != 5 || c[2] != 3 || c[4] != 5) { return 6; }
    return 0;
}
`
