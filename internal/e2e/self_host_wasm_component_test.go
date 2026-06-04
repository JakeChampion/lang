package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmComponent exercises the first slice of the self-hosted
// Component-Model wrapper (wat_component.fern): the core-module encoder
// produces a (preview1) core module, and component_wrap embeds it in the
// binary component envelope (preamble + core-module section). The result
// must be a valid `(component (core module …))` per wasm-tools.
//
// The assembler here is the binary-encoder modules + wat_component + a
// driver that read_file()s a target WAT, runs tokenize -> parse ->
// emit_binary -> component_wrap, and prints the bytes; the test reassembles
// them and validates the component.
func TestSelfHostWasmComponent(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host component e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping self-host component e2e")
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

	// Assembler = encoder modules + wat_component + a wrapping driver.
	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentWrapDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	for _, tc := range []struct {
		name   string
		source string
	}{
		{"int", "function main(): i32 { return 42; }"},
		{"struct", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; return p.x + p.y; }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("WAT emitter produced 0 bytes")
			}
			if err := os.WriteFile(filepath.Join(dir, "target.wat"), wat, 0o644); err != nil {
				t.Fatalf("write target.wat: %v", err)
			}
			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				t.Fatalf("run component assembler: %v", err)
			}
			var bs []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				bs = append(bs, byte(n))
			}
			// Component preamble: \0asm + version 13, layer 1.
			if len(bs) < 8 || bs[0] != 0 || bs[1] != 97 || bs[2] != 115 || bs[3] != 109 || bs[4] != 13 || bs[6] != 1 {
				t.Fatalf("not a component preamble: %v", bs[:min(8, len(bs))])
			}
			compPath := filepath.Join(dir, tc.name+".component.wasm")
			if err := os.WriteFile(compPath, bs, 0o644); err != nil {
				t.Fatalf("write component: %v", err)
			}
			if vout, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate failed: %v\n%s", err, vout)
			}
			pout, err := exec.Command(wasmtools, "print", compPath).Output()
			if err != nil {
				t.Fatalf("wasm-tools print: %v", err)
			}
			if !strings.Contains(string(pout), "(component") || !strings.Contains(string(pout), "(core module") {
				t.Errorf("expected a (component (core module …)), got:\n%s", pout)
			}
		})
	}
}

// componentWrapDriver reads target.wat, assembles the core module, wraps it
// in the component envelope, and prints the bytes as decimals.
const componentWrapDriver = `
function main(): i32 {
    match (read_file("target.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var bytes: i32[] = component_wrap(core);
            var i: i32 = 0;
            while (i < bytes.len()) { print_int(bytes[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`
