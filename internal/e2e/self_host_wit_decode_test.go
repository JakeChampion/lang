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
