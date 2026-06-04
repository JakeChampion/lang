package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmBinary exercises the self-hosted *binary* wasm emitter
// end to end — slice 4b, the module-walker + opcode encoder
// (wat_emit_bin.fern) on top of leb128 / wat_lex / wat_parse / wat_encode.
//
// For each program it:
//  1. runs the WAT emitter (wasm_run) to get the textual module + its
//     reference exit code under wasmtime,
//  2. builds an "assembler" program: the five binary-encoder modules
//     concatenated with a driver that embeds that WAT, runs it through
//     tokenize -> parse -> emit_binary, and prints the resulting bytes as
//     newline-separated decimals,
//  3. compiles + runs that assembler (itself through the self-host wasm
//     pipeline) to obtain the bytes,
//  4. reassembles the bytes into a .wasm and runs it,
//  5. asserts the binary module's exit code matches both the WAT path and
//     the expected value.
//
// The encoder modules are import-free, so the assembler is a single
// concatenated module (the WAT is embedded rather than read from stdin,
// which a stdin-less program can't do).
func TestSelfHostWasmBinary(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binary-wasm e2e")
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

	// The binary-encoder modules, concatenated as the assembler's prelude.
	var encPrelude strings.Builder
	for _, name := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		encPrelude.Write(b)
		encPrelude.WriteByte('\n')
	}

	runWat := func(t *testing.T, watBytes []byte, tag string) int {
		watPath := filepath.Join(dir, tag+".wat")
		if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
			t.Fatalf("write %s: %v", watPath, err)
		}
		cmd := exec.Command(wasmtime, "run", watPath)
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name   string
		source string
		exit   int
	}{
		{"return-literal", "function main(): i32 { return 42; }", 42},
		{"arithmetic", "function main(): i32 { return 1 + 2 * 3; }", 7},
		{"locals", "function main(): i32 { var x: i32 = 5; return x + 37; }", 42},
		{"subtraction", "function main(): i32 { var a: i32 = 100; var b: i32 = 58; return a - b; }", 42},
		{"bitwise", "function main(): i32 { return (10 & 6) + (10 | 1); }", 13},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. WAT + its reference exit.
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("WAT emitter produced 0 bytes")
			}
			watExit := runWat(t, wat, tc.name+"_wat")
			if watExit != tc.exit {
				t.Fatalf("WAT path exited %d, want %d", watExit, tc.exit)
			}

			// 2. Assembler program: encoder modules + driver embedding the
			//    WAT (whitespace-collapsed; the tokenizer is whitespace-
			//    insensitive) and printing the bytes as decimals.
			flat := strings.Join(strings.Fields(string(wat)), " ")
			esc := strings.ReplaceAll(flat, `\`, `\\`)
			esc = strings.ReplaceAll(esc, `"`, `\"`)
			driver := "function main(): i32 {\n" +
				"    var wat: string = \"" + esc + "\";\n" +
				"    var bytes: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));\n" +
				"    var i: i32 = 0;\n" +
				"    while (i < bytes.len()) { print_int(bytes[i]); write(\"\\n\"); i = i + 1; }\n" +
				"    return 0;\n}\n"
			asmSrc := encPrelude.String() + driver

			// 3. Compile + run the assembler to get the bytes.
			asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc))
			if len(asmWat) == 0 {
				t.Fatal("assembler emitter produced 0 bytes")
			}
			asmWatPath := filepath.Join(dir, tc.name+"_asm.wat")
			if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
				t.Fatalf("write asm wat: %v", err)
			}
			out, err := exec.Command(wasmtime, "run", asmWatPath).Output()
			if err != nil {
				t.Fatalf("run assembler: %v", err)
			}

			// 4. Reassemble the decimal byte stream into a .wasm.
			var bs []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				if n < 0 || n > 255 {
					t.Fatalf("byte out of range: %d", n)
				}
				bs = append(bs, byte(n))
			}
			if len(bs) < 8 {
				t.Fatalf("binary too short: %d bytes", len(bs))
			}
			wasmPath := filepath.Join(dir, tc.name+".wasm")
			if err := os.WriteFile(wasmPath, bs, 0o644); err != nil {
				t.Fatalf("write wasm: %v", err)
			}

			// 5. Run the binary module; assert it matches.
			cmd := exec.Command(wasmtime, "run", wasmPath)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("binary module exited %d, want %d (WAT path: %d)\n%s",
					code, tc.exit, watExit, fmt.Sprintf("%d bytes", len(bs)))
			}
		})
	}
}
