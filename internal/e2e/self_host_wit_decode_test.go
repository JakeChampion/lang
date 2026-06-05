package e2e

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
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/leb128.fern")
	if err != nil {
		t.Fatalf("read leb128.fern: %v", err)
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
    while (i < s.len()) { o = o.push(s[i]); i = i + 1; }
    return o;
}
function main(): i32 {
    var payload: i32[] = wit_bytes_of(FERN_BIN());
    var got: i32[] = wit_reencode_sections(payload);
    if (got.len() != payload.len()) { return 1; }
    var i: i32 = 0;
    while (i < got.len()) {
        if (got[i] != payload[i]) { return 2; }
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
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/leb128.fern")
	if err != nil {
		t.Fatalf("read leb128.fern: %v", err)
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
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/leb128.fern")
	if err != nil {
		t.Fatalf("read leb128.fern: %v", err)
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
    while (i < s.len()) { o = o.push(s[i]); i = i + 1; }
    return o;
}
function wit_eq2(a: i32[], b: i32[]): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; }
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
