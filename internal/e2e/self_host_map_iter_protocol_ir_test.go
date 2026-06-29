package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapIterProtocolIR pins the MapIter cursor protocol on the
// self-host IR path: `m.iter()` then `it.has_next()` / `it.key()` /
// `it.value()` / `it.advance()` over a heap cursor. std/json's json_encode
// walks a JObject's Map[string, JsonValue] exactly this way; before this the
// `iter` call fell to the user-method path (`BAIL call[Map.iter]`), dragging
// every module that imports std/json (json_roundtrip, json_detail, …) onto the
// AST emitter. Now `m.iter()` lowers to op_map_iter (a fresh 16-byte
// [map@0, cursor@8] box) and the four cursor methods to the mapiter_* ops,
// mirroring asm.fern's inline box walk.
//
// Asserts the program routes IR (`-decide` → "ir") and that the actual cursor
// walk runs correctly (parse an object, re-encode it via the map iterator, and
// compare) to exit 0.
//
// Native only: the file-loading driver reads stdlib modules by host path from
// argv (mirrors TestSelfHostStdTestE2E / TestSelfHostImportAliasIR).
func TestSelfHostMapIterProtocolIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "util.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// json_encode of a parsed JObject walks the Map via m.iter(). The
	// parallel-array map preserves insertion (document) order, so re-encoding
	// reproduces the input verbatim.
	src := `import "std/json" as json;
function main(): i32 {
    match (json.json_parse("{\"a\":1,\"b\":2}")) {
        Some(v) => {
            if (json.json_encode(v) != "{\"a\":1,\"b\":2}") { return 1; }
            return 0;
        },
        None => { return 2; }
    }
}`
	prog := filepath.Join(dir, "mapiter_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}

	if got := strings.TrimSpace(runDriverDecide(t, mmc, prog, stdlibRoot)); got != "ir" {
		t.Fatalf("map-iter program routed %q, want \"ir\" (Map.iter must lower on the IR path)", got)
	}

	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("self-host compile failed: %v", err)
	}
	bin := buildBin(t, gcc, dir, "mapiter_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Errorf("map-iter IR program exited %d, want 0 (cursor walk miscompiled)", code)
	}
}
