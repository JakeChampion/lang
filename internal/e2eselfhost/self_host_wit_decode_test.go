package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostWitSectionRoundTrip gates the self-host port of the
// bring-your-own-WIT decoder (examples/self_host/wit_decode.fern, P1 slice
// 1). It compiles, through the self-host, a driver that walks the real
// fern.bin component-type payload into sections and re-emits them, and
// asserts under wasmtime that the result reproduces the input byte-for-byte
// — the same round-trip the Go decoder's tests use, but executed by the
// self-hosted compiler. The payload is injected from componenttype (single
// source of truth; no duplicated hex). Returns 0 on success, a check id
// otherwise.
func TestSelfHostWitSectionRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-decode e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" + witSectionSelfTestMain(t)

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-decode self-test")
	}
	watPath := filepath.Join(dir, "wit_section_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-decode section round-trip failed at check %d", code)
	}
}

// witSectionSelfTestMain builds the Fern driver: a FERN_BIN() returning the
// real fern.bin payload, plus a main() that round-trips it through
// wit_reencode_sections and returns 0 iff the bytes match.
func witSectionSelfTestMain(t *testing.T) string {
	t.Helper()
	payload, err := componenttype.PayloadFor("fern")
	if err != nil {
		t.Fatalf("PayloadFor(fern): %v", err)
	}
	var sb strings.Builder
	sb.WriteString(`function FERN_BIN(): string { return "`)
	for _, b := range payload {
		fmt.Fprintf(&sb, `\x%02x`, b)
	}
	sb.WriteString("\"; }\n")
	sb.WriteString(`
function wit_bytes_of(s: string): i32[] {
    var o: i32[] = [];
    var i: i32 = 0;
    while (i < s.len()) { o = o.append((s[i] as i32)); i = i + 1; }
    return o;
}
function main(): i32 {
    var payload: i32[] = wit_bytes_of(FERN_BIN());
    var got: i32[] = wit_reencode_sections(payload);
    if (got.len() != payload.len()) { return 1; }
    var i: i32 = 0;
    while (i < got.len()) {
        if ((got[i] as i32) != (payload[i] as i32)) { return 2; }
        i = i + 1;
    }
    return 0;
}
`)
	return sb.String()
}

// TestSelfHostWitValtypeRoundTrip gates P1 slice 2 of the self-host WIT
// decoder port: the value-type grammar (defined types + func types) decoded
// into a model and re-encoded must reproduce crafted byte vectors, run
// through the self-host under wasmtime. Returns 0 on success, else a check id
// (def checks 1..6, func checks 7..8).
func TestSelfHostWitValtypeRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-valtype e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" + witValtypeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-valtype self-test")
	}
	watPath := filepath.Join(dir, "wit_valtype_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-valtype round-trip failed at check %d", code)
	}
}

// witValtypeSelfTestMain round-trips one crafted vector per defined-type kind
// and func shape (incl. the result variants, a multi-byte sleb type index,
// and the empty-named-results func), returning the first failing check id.
const witValtypeSelfTestMain = `
function wit_eq(a: i32[], b: i32[]): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; }
    return true;
}
function wit_rt_def(v: i32[]): boolean {
    var r: WitDefR = wit_decode_def(v, 0);
    if (r.next != v.len()) { return false; }
    return wit_eq(wit_encode_def([], r.def), v);
}
function wit_rt_func(v: i32[]): boolean {
    var r: WitFuncR = wit_decode_func(v, 0);
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

// TestSelfHostWitWorldRoundTrip completes P1 of the self-host WIT decoder
// port: it compiles, through the self-host, a driver that transcodes the
// whole type and export sections of BOTH shipped worlds (fern + http) — the
// full nested component/instance-type + decl/alias/externdesc grammar — and
// asserts under wasmtime that each reproduces the original section bytes. The
// payloads are injected from componenttype (single source of truth). This is
// the self-host mirror of the Go decoder's fern.bin/http.bin round-trip.
// Returns 0 on success; 1/2 = fern type/export, 3/4 = http type/export.
func TestSelfHostWitWorldRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-world e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" +
		witPayloadFunc(t, "FERN_BIN", "fern") + witPayloadFunc(t, "HTTP_BIN", "http") + witWorldSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-world self-test")
	}
	watPath := filepath.Join(dir, "wit_world_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-world round-trip failed at check %d", code)
	}
}

// witPayloadFunc emits `function <fn>(): string { return "<world bytes>"; }`.
func witPayloadFunc(t *testing.T, fn, world string) string {
	t.Helper()
	payload, err := componenttype.PayloadFor(world)
	if err != nil {
		t.Fatalf("PayloadFor(%s): %v", world, err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `function %s(): string { return "`, fn)
	for _, b := range payload {
		fmt.Fprintf(&sb, `\x%02x`, b)
	}
	sb.WriteString("\"; }\n")
	return sb.String()
}

const witWorldSelfTestMain = `
function wit_bytes(s: string): i32[] {
    var o: i32[] = [];
    var i: i32 = 0;
    while (i < s.len()) { o = o.append((s[i] as i32)); i = i + 1; }
    return o;
}
function wit_eq2(a: i32[], b: i32[]): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) { if ((a[i] as i32) != (b[i] as i32)) { return false; } i = i + 1; }
    return true;
}
function main(): i32 {
    var fern: i32[] = wit_bytes(FERN_BIN());
    if (!wit_eq2(wit_transcode_type_section(wit_section_body(fern, 7)), wit_section_body(fern, 7))) { return 1; }
    if (!wit_eq2(wit_transcode_export_section(wit_section_body(fern, 11)), wit_section_body(fern, 11))) { return 2; }
    var http: i32[] = wit_bytes(HTTP_BIN());
    if (!wit_eq2(wit_transcode_type_section(wit_section_body(http, 7)), wit_section_body(http, 7))) { return 3; }
    if (!wit_eq2(wit_transcode_export_section(wit_section_body(http, 11)), wit_section_body(http, 11))) { return 4; }
    return 0;
}
`

// TestSelfHostWitWorldLift gates the self-host P2 lift: extracting the world's
// imported interface names from the decoded fern world, run through the
// self-host under wasmtime, must match the 19 fern imports in order. Returns
// 0 on success, else the 1-based index of the first mismatch (or 99 for a
// count mismatch).
func TestSelfHostWitWorldLift(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-lift e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" + witPayloadFunc(t, "FERN_BIN", "fern") + witLiftSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-lift self-test")
	}
	watPath := filepath.Join(dir, "wit_lift_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-lift self-test failed at check %d", code)
	}
}

const witLiftSelfTestMain = `
function wit_lift_bytes(s: string): i32[] {
    var o: i32[] = [];
    var i: i32 = 0;
    while (i < s.len()) { o = o.append((s[i] as i32)); i = i + 1; }
    return o;
}
function main(): i32 {
    var want: string[] = [
        "wasi:io/error@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/stdin@0.2.0",
        "wasi:cli/stdout@0.2.0", "wasi:cli/stderr@0.2.0", "wasi:cli/environment@0.2.0",
        "wasi:cli/exit@0.2.0", "wasi:io/poll@0.2.0", "wasi:clocks/monotonic-clock@0.2.0",
        "wasi:clocks/wall-clock@0.2.0", "wasi:filesystem/types@0.2.0",
        "wasi:filesystem/preopens@0.2.0", "wasi:sockets/network@0.2.0",
        "wasi:sockets/instance-network@0.2.0", "wasi:sockets/tcp@0.2.0",
        "wasi:sockets/tcp-create-socket@0.2.0", "wasi:sockets/udp@0.2.0",
        "wasi:sockets/udp-create-socket@0.2.0", "wasi:random/random@0.2.0"
    ];
    var got: string[] = wit_world_import_names(wit_section_body(wit_lift_bytes(FERN_BIN()), 7));
    if (got.len() != want.len()) { return 99; }
    var i: i32 = 0;
    while (i < want.len()) {
        if (got[i] != want[i]) { return i + 1; }
        i = i + 1;
    }
    return 0;
}
`

// TestSelfHostWitEmitWorldImports gates the self-host P2 emit: replaying the
// decoded world's decls as component sections must reproduce the Go
// EmitWorldImports bytes exactly (which wasm-tools validates, see the Go
// tests), run through the self-host under wasmtime. The Go reference is
// computed and injected so the two implementations are pinned together.
func TestSelfHostWitEmitWorldImports(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-emit e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	ref, err := w.EmitWorldImports()
	if err != nil {
		t.Fatalf("EmitWorldImports: %v", err)
	}

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" +
		witPayloadFunc(t, "FERN_BIN", "fern") + witBytesFunc("EMIT_REF", ref) + witEmitSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-emit self-test")
	}
	watPath := filepath.Join(dir, "wit_emit_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-emit self-test failed at check %d", code)
	}
}

// witBytesFunc emits `function <fn>(): string { return "<bytes>"; }`.
func witBytesFunc(fn string, b []byte) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `function %s(): string { return "`, fn)
	for _, c := range b {
		fmt.Fprintf(&sb, `\x%02x`, c)
	}
	sb.WriteString("\"; }\n")
	return sb.String()
}

const witEmitSelfTestMain = `
function wit_emit_bytes(s: string): i32[] {
    var o: i32[] = [];
    var i: i32 = 0;
    while (i < s.len()) { o = o.append((s[i] as i32)); i = i + 1; }
    return o;
}
function main(): i32 {
    var got: i32[] = wit_emit_world_imports(wit_section_body(wit_emit_bytes(FERN_BIN()), 7));
    var want: i32[] = wit_emit_bytes(EMIT_REF());
    if (got.len() != want.len()) { return 1; }
    var i: i32 = 0;
    while (i < got.len()) {
        if ((got[i] as i32) != (want[i] as i32)) { return 2; }
        i = i + 1;
    }
    return 0;
}
`

// TestSelfHostWitClassify gates the self-host P2 classifier: deriving each
// import's lowering kind from the decoded fern world (via the canonical-ABI
// flattening rules) must match the Go classifier's kinds, run through the
// self-host under wasmtime. Covers all three kinds and the subtle cases
// (indirect-return mem, scalar-handle no-opt, heap-result mem+realloc).
// Returns 0 on success, else the 1-based index of the first mismatch.
func TestSelfHostWitClassify(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-classify e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" + witPayloadFunc(t, "FERN_BIN", "fern") + witClassifySelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-classify self-test")
	}
	watPath := filepath.Join(dir, "wit_classify_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-classify self-test failed at check %d", code)
	}
}

const witClassifySelfTestMain = `
function wit_cl_bytes(s: string): i32[] {
    var o: i32[] = [];
    var i: i32 = 0;
    while (i < s.len()) { o = o.append((s[i] as i32)); i = i + 1; }
    return o;
}
function wit_ck(tb: i32[], iface: string, fn: string, want: i32, id: i32): i32 {
    if (wit_classify(tb, iface, fn) != want) { return id; }
    return 0;
}
function main(): i32 {
    var tb: i32[] = wit_cl_bytes(FERN_BIN());
    tb = wit_section_body(tb, 7);
    var r: i32 = 0;
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

// TestSelfHostWitPrefixLayout gates the self-host prefix index layout: the
// component type / instance counts and per-interface instance index derived
// from the decoded fern world must match the Go PrefixLayout /
// ImportInstanceIndex, run through the self-host under wasmtime. Returns 0 on
// success, else a check id.
func TestSelfHostWitPrefixLayout(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wit-layout e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	decode, err := os.ReadFile("../../examples/self_host/wit_decode.fern")
	if err != nil {
		t.Fatalf("read wit_decode.fern: %v", err)
	}
	source := string(leb) + "\n" + string(decode) + "\n" + witPayloadFunc(t, "FERN_BIN", "fern") + witLayoutSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wit-layout self-test")
	}
	watPath := filepath.Join(dir, "wit_layout_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wit-layout self-test failed at check %d", code)
	}
}

const witLayoutSelfTestMain = `
function wit_ly_bytes(s: string): i32[] {
    var o: i32[] = [];
    var i: i32 = 0;
    while (i < s.len()) { o = o.append((s[i] as i32)); i = i + 1; }
    return o;
}
function main(): i32 {
    var tb: i32[] = wit_section_body(wit_ly_bytes(FERN_BIN()), 7);
    var pl: WitPrefixLayout = wit_prefix_layout(tb);
    if (pl.types != 32) { return 1; }
    if (pl.instances != 19) { return 2; }
    if (wit_import_instance_index(tb, "wasi:io/error@0.2.0") != 0) { return 3; }
    if (wit_import_instance_index(tb, "wasi:io/streams@0.2.0") != 1) { return 4; }
    if (wit_import_instance_index(tb, "wasi:cli/stdout@0.2.0") != 3) { return 5; }
    if (wit_import_instance_index(tb, "wasi:random/random@0.2.0") != 18) { return 6; }
    if (wit_import_instance_index(tb, "wasi:not/here@0.2.0") != (0 - 1)) { return 7; }
    return 0;
}
`
