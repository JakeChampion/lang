package e2ecompiler

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

func TestSelfHostWitSectionRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-decode e2e")
	}
	dir := t.TempDir()

	watPath := witCompileToWat(t, dir, "wit_section_selftest", witSectionSelfTestMain(t))
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-decode section round-trip failed at check %d", code)
	}
}

func witSectionSelfTestMain(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(witPayloadFunc(t, "FERN_BIN", "fern"))
	sb.WriteString(`
function main(): i32 {
    let payload: i32[] = FERN_BIN();
    let got: i32[] = wit_reencode_sections(payload);
    if (got.len() != payload.len()) { return 1; }
    let i: i32 = 0;
    while (i < got.len()) {
        if ((got[i] as i32) != (payload[i] as i32)) { return 2; }
        i = i + 1;
    }
    return 0;
}
`)
	return sb.String()
}

func TestSelfHostWitValtypeRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-valtype e2e")
	}
	dir := t.TempDir()

	watPath := witCompileToWat(t, dir, "wit_valtype_selftest", witValtypeSelfTestMain)
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-valtype round-trip failed at check %d", code)
	}
}

const witValtypeSelfTestMain = `
function wit_eq(a: i32[], b: i32[]): boolean {
    if (a.len() != b.len()) { return false; }
    let i: i32 = 0;
    while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; }
    return true;
}
function wit_rt_def(v: i32[]): boolean {
    let r: WitDefR = wit_decode_def(v, 0);
    if (r.next != v.len()) { return false; }
    return wit_eq(wit_encode_def([], r.def), v);
}
function wit_rt_func(v: i32[]): boolean {
    let r: WitFuncR = wit_decode_func(v, 0);
    if (r.next != v.len()) { return false; }
    return wit_eq(wit_encode_func([], r.fn), v);
}
function main(): i32 {
    if (!wit_rt_def([114, 2, 7, 115,101,99,111,110,100,115, 119, 11, 110,97,110,111,115,101,99,111,110,100,115, 121])) { return 1; }
    if (!wit_rt_def([113, 2, 21, 108,97,115,116,45,111,112,101,114,97,116,105,111,110,45,102,97,105,108,101,100, 1, 2, 0, 6, 99,108,111,115,101,100, 0, 0])) { return 2; }
    if (!wit_rt_def([109, 2, 1, 97, 1, 98])) { return 3; }
    if (!wit_rt_def([106, 0, 1, 4])) { return 4; }
    if (!wit_rt_def([106, 1, 119, 0])) { return 5; }
    if (!wit_rt_def([111, 1, 193, 0])) { return 6; }
    if (!wit_rt_func([64, 2, 4, 115,101,108,102, 7, 3, 108,101,110, 119, 0, 9])) { return 7; }
    if (!wit_rt_func([64, 0, 1, 0])) { return 8; }
    return 0;
}
`

func TestSelfHostWitWorldRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-world e2e")
	}
	dir := t.TempDir()

	watPath := witCompileToWat(t, dir, "wit_world_selftest", witPayloadFunc(t, "FERN_BIN", "fern")+witPayloadFunc(t, "HTTP_BIN", "http")+witWorldSelfTestMain)
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-world round-trip failed at check %d", code)
	}
}

func witPayloadFunc(t *testing.T, fn, world string) string {
	t.Helper()
	payload, err := componenttype.PayloadFor(world)
	if err != nil {
		t.Fatalf("PayloadFor(%s): %v", world, err)
	}
	return witBytesFunc(fn, payload)
}

const witWorldSelfTestMain = `
function wit_eq2(a: i32[], b: i32[]): boolean {
    if (a.len() != b.len()) { return false; }
    let i: i32 = 0;
    while (i < a.len()) { if ((a[i] as i32) != (b[i] as i32)) { return false; } i = i + 1; }
    return true;
}
function main(): i32 {
    let fern: i32[] = FERN_BIN();
    if (!wit_eq2(wit_transcode_type_section(wit_section_body(fern, 7)), wit_section_body(fern, 7))) { return 1; }
    if (!wit_eq2(wit_transcode_export_section(wit_section_body(fern, 11)), wit_section_body(fern, 11))) { return 2; }
    let http: i32[] = HTTP_BIN();
    if (!wit_eq2(wit_transcode_type_section(wit_section_body(http, 7)), wit_section_body(http, 7))) { return 3; }
    if (!wit_eq2(wit_transcode_export_section(wit_section_body(http, 11)), wit_section_body(http, 11))) { return 4; }
    return 0;
}
`

func TestSelfHostWitWorldLift(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-lift e2e")
	}
	dir := t.TempDir()

	watPath := witCompileToWat(t, dir, "wit_lift_selftest", witPayloadFunc(t, "FERN_BIN", "fern")+witLiftSelfTestMain)
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-lift self-test failed at check %d", code)
	}
}

const witLiftSelfTestMain = `
function main(): i32 {
    let want: string[] = [
        "wasi:io/error@0.2.0", "wasi:io/poll@0.2.0", "wasi:io/streams@0.2.0",
        "wasi:cli/stdin@0.2.0", "wasi:cli/stdout@0.2.0", "wasi:cli/stderr@0.2.0",
        "wasi:cli/environment@0.2.0", "wasi:cli/exit@0.2.0", "wasi:clocks/monotonic-clock@0.2.0",
        "wasi:clocks/wall-clock@0.2.0", "wasi:filesystem/types@0.2.0",
        "wasi:filesystem/preopens@0.2.0", "wasi:sockets/network@0.2.0",
        "wasi:sockets/instance-network@0.2.0", "wasi:sockets/tcp@0.2.0",
        "wasi:sockets/tcp-create-socket@0.2.0", "wasi:sockets/udp@0.2.0",
        "wasi:sockets/udp-create-socket@0.2.0", "wasi:random/random@0.2.0"
    ];
    let got: string[] = wit_world_import_names(wit_section_body(FERN_BIN(), 7));
    if (got.len() != want.len()) { return 99; }
    let i: i32 = 0;
    while (i < want.len()) {
        if (got[i] != want[i]) { return i + 1; }
        i = i + 1;
    }
    return 0;
}
`

func TestSelfHostWitEmitWorldImports(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-emit e2e")
	}
	dir := t.TempDir()

	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	ref, err := w.EmitWorldImports()
	if err != nil {
		t.Fatalf("EmitWorldImports: %v", err)
	}

	watPath := witCompileToWat(t, dir, "wit_emit_selftest", witPayloadFunc(t, "FERN_BIN", "fern")+witBytesFunc("EMIT_REF", ref)+witEmitSelfTestMain)
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-emit self-test failed at check %d", code)
	}
}

func witBytesFunc(fn string, b []byte) string {
	return fmt.Sprintf("function %s(): i32[] { return hex_to_bytes(\"%x\"); }\n", fn, b)
}

const witEmitSelfTestMain = `
function main(): i32 {
    let tb: i32[] = wit_section_body(FERN_BIN(), 7);
    let ifaces: string[] = wit_world_import_names(tb);
    let whole: string[] = [];
    let k: i32 = 0;
    while (k < ifaces.len()) { whole = whole.append(""); k = k + 1; }
    let got: i32[] = wit_emit_world_imports(tb, wit_import_plan(tb, ifaces, whole));
    let want: i32[] = EMIT_REF();
    if (got.len() != want.len()) { return 1; }
    let i: i32 = 0;
    while (i < got.len()) {
        if ((got[i] as i32) != (want[i] as i32)) { return 2; }
        i = i + 1;
    }
    return 0;
}
`

func TestSelfHostWitClassify(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-classify e2e")
	}
	dir := t.TempDir()

	watPath := witCompileToWat(t, dir, "wit_classify_selftest", witPayloadFunc(t, "FERN_BIN", "fern")+witClassifySelfTestMain)
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-classify self-test failed at check %d", code)
	}
}

const witClassifySelfTestMain = `
function wit_ck(tb: i32[], iface: string, fn: string, want: i32, id: i32): i32 {
    if (wit_classify(tb, iface, fn) != want) { return id; }
    return 0;
}
function main(): i32 {
    let tb: i32[] = FERN_BIN();
    tb = wit_section_body(tb, 7);
    let r: i32 = 0;
    r = wit_ck(tb, "wasi:cli/stdout@0.2.0", "get-stdout", 0, 1); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:io/streams@0.2.0", "[method]output-stream.blocking-write-and-flush", 1, 2); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:io/streams@0.2.0", "[method]input-stream.blocking-read", 2, 3); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:filesystem/preopens@0.2.0", "get-directories", 2, 4); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:filesystem/types@0.2.0", "[method]descriptor.open-at", 1, 5); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:filesystem/types@0.2.0", "[method]descriptor.read-via-stream", 1, 6); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:io/poll@0.2.0", "[method]pollable.block", 0, 7); if (r != 0) { return r; }
    r = wit_ck(tb, "wasi:sockets/tcp@0.2.0", "[method]tcp-socket.accept", 1, 8); if (r != 0) { return r; }
    return 0;
}
`

func TestSelfHostWitPrefixLayout(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-layout e2e")
	}
	dir := t.TempDir()

	watPath := witCompileToWat(t, dir, "wit_layout_selftest", witPayloadFunc(t, "FERN_BIN", "fern")+witLayoutSelfTestMain)
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-layout self-test failed at check %d", code)
	}
}

const witLayoutSelfTestMain = `
function wit_ly_whole(n: i32): string[] {
    let o: string[] = [];
    let i: i32 = 0;
    while (i < n) { o = o.append(""); i = i + 1; }
    return o;
}
function main(): i32 {
    let tb: i32[] = wit_section_body(FERN_BIN(), 7);
    let ifaces: string[] = wit_world_import_names(tb);
    let all: WitImportPlan = wit_import_plan(tb, ifaces, wit_ly_whole(ifaces.len()));
    if (all.n_type != 32) { return 1; }
    if (all.n_inst != 19) { return 2; }
    if (wit_import_instance_index(tb, all, "wasi:io/error@0.2.0") != 0) { return 3; }
    if (wit_import_instance_index(tb, all, "wasi:io/streams@0.2.0") != 2) { return 4; }
    if (wit_import_instance_index(tb, all, "wasi:cli/stdout@0.2.0") != 4) { return 5; }
    if (wit_import_instance_index(tb, all, "wasi:random/random@0.2.0") != 18) { return 6; }
    if (wit_import_instance_index(tb, all, "wasi:not/here@0.2.0") != (0 - 1)) { return 7; }
    let stdout: WitImportPlan = wit_import_plan(tb, ["wasi:cli/stdout@0.2.0"], ["get-stdout"]);
    if (stdout.n_inst != 2) { return 8; }
    if (wit_import_instance_index(tb, stdout, "wasi:io/error@0.2.0") != (0 - 1)) { return 9; }
    if (wit_import_instance_index(tb, stdout, "wasi:io/streams@0.2.0") != 0) { return 10; }
    if (wit_import_instance_index(tb, stdout, "wasi:cli/stdout@0.2.0") != 1) { return 11; }
    if (wit_import_instance_index(tb, stdout, "wasi:random/random@0.2.0") != (0 - 1)) { return 12; }
    let stat: WitImportPlan = wit_import_plan(tb, ["wasi:filesystem/types@0.2.0"], ["[method]descriptor.stat-at"]);
    if (stat.n_inst != 1) { return 13; }
    if (wit_import_instance_index(tb, stat, "wasi:filesystem/types@0.2.0") != 0) { return 14; }
    if (wit_import_instance_index(tb, stat, "wasi:clocks/wall-clock@0.2.0") != (0 - 1)) { return 15; }
    return 0;
}
`
