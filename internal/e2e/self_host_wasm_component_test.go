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

// TestSelfHostWasmComponentFull exercises the full Component-Model framing
// (wat_component.fern's component_full): given a core module that exports
// `_lang_run`, it emits the core-instance / alias / type / canon-lift /
// instance / `wasi:cli/run` sections around it. The framing is constant for
// this fixed shape, so the test feeds the Go backend's own core module
// (extracted from its `-target wasm` component output) to component_full and
// asserts the result is byte-identical to — and runs the same as — the Go
// reference component.
func TestSelfHostWasmComponentFull(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host component-full e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping self-host component-full e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}

	// Build the self-host component assembler once (program-independent): it
	// read_file()s a core module and wraps it with component_full.
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
	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_encode.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("component-full assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	// Cover both run-result paths: main()==0 → run() ok (exit 0),
	// main()!=0 → run() err (exit 1).
	for _, tc := range []struct {
		name   string
		source string
	}{
		{"ok", "function main(): i32 { return 0; }"},
		{"err", "function main(): i32 { return 42; }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progPath := filepath.Join(dir, "prog.fern")
			if err := os.WriteFile(progPath, []byte(tc.source), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}
			refPath := filepath.Join(dir, "ref.wasm")
			if out, err := exec.Command(fernBin, "-target", "wasm", "-o", refPath, progPath).CombinedOutput(); err != nil {
				t.Fatalf("fern -target wasm: %v\n%s", err, out)
			}
			ref, err := os.ReadFile(refPath)
			if err != nil {
				t.Fatalf("read ref: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
				t.Fatalf("write core.bin: %v", err)
			}

			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				t.Fatalf("run component assembler: %v", err)
			}
			var got []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				got = append(got, byte(n))
			}

			// The self-host component must be byte-identical to the Go
			// reference (same core + the same constant wasi:cli/run framing).
			if !bytesEqual(got, ref) {
				t.Fatalf("component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
			}
			myPath := filepath.Join(dir, "mine.component.wasm")
			if err := os.WriteFile(myPath, got, 0o644); err != nil {
				t.Fatalf("write component: %v", err)
			}
			if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
			}
			// Run mine + the Go reference; their exit codes must match (the
			// run() result convention: ok → 0, err → 1).
			mineExit := exec.Command(wasmtime, "run", myPath).Run()
			refExit := exec.Command(wasmtime, "run", refPath).Run()
			if (mineExit == nil) != (refExit == nil) {
				t.Errorf("run mismatch: mine=%v ref=%v", mineExit, refExit)
			}
		})
	}
}

// componentCoreSection returns the bytes of the first core-module section
// (component section id 1) of a component binary.
func componentCoreSection(t *testing.T, b []byte) []byte {
	t.Helper()
	i := 8 // skip preamble
	for i < len(b) {
		sid := b[i]
		i++
		size := 0
		shift := 0
		for {
			x := b[i]
			i++
			size |= int(x&0x7f) << shift
			if x&0x80 == 0 {
				break
			}
			shift += 7
		}
		if sid == 1 {
			return b[i : i+size]
		}
		i += size
	}
	t.Fatal("no core-module section in component")
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// componentFullDriver reads a core module's bytes (core.bin) and wraps them
// with component_full into a complete wasi:cli/run component.
const componentFullDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.push(s[i]); i = i + 1; }
            var comp: i32[] = component_full(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int(comp[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentEndToEnd compiles a program to a wasi:cli/run
// component entirely through the self-host: source -> emit_module_run
// (preview2 core WAT) -> emit_binary (core) -> component_full (component).
// It then runs the component and checks the run() result convention
// (main()==0 -> ok -> exit 0; main()!=0 -> err -> exit 1). No-I/O programs
// only — the preview2 core is import-free.
func TestSelfHostWasmComponentEndToEnd(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host component end-to-end e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping self-host component end-to-end e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// A preview1 driver (to compile the component assembler) and a preview2
	// driver (to emit the run-core WAT for a program).
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_p2.fern"), []byte(p2Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run_p2.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	p2Bin := buildSelfHostBin(t, gcc, dir, "wasm_run_p2.fern", "wasm_run_p2")

	// Component assembler: read a core WAT, emit_binary, component_full.
	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileDriver)
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
		ok     bool // run() ok (main()==0) → exit 0
	}{
		{"return0", "function main(): i32 { return 0; }", true},
		{"return42", "function main(): i32 { return 42; }", false},
		{"compute-zero", "function main(): i32 { var x: i32 = 5; var y: i32 = 5; return x - y; }", true},
		{"compute-nonzero", "function main(): i32 { var n: i32 = 3; return n * 7; }", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// source -> preview2 core WAT
			coreWat := runCapture(t, gcc, runner, p2Bin, []byte(tc.source))
			if len(coreWat) == 0 {
				t.Fatal("preview2 core WAT empty")
			}
			if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
				t.Fatalf("write core.wat: %v", err)
			}
			// core WAT -> component bytes
			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				t.Fatalf("run component assembler: %v", err)
			}
			var comp []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				comp = append(comp, byte(n))
			}
			compPath := filepath.Join(dir, tc.name+".component.wasm")
			if err := os.WriteFile(compPath, comp, 0o644); err != nil {
				t.Fatalf("write component: %v", err)
			}
			// must validate as a component exporting wasi:cli/run
			if vout, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
			}
			pout, err := exec.Command(wasmtools, "print", compPath).Output()
			if err != nil {
				t.Fatalf("wasm-tools print: %v", err)
			}
			if !strings.Contains(string(pout), "wasi:cli/run") {
				t.Errorf("expected a wasi:cli/run export, got:\n%s", pout)
			}
			// run it: ok → exit 0, err → exit 1
			runErr := exec.Command(wasmtime, "run", compPath).Run()
			if tc.ok && runErr != nil {
				t.Errorf("expected run() ok (exit 0), got %v", runErr)
			}
			if !tc.ok && runErr == nil {
				t.Errorf("expected run() err (exit 1), got success")
			}
		})
	}
}

const p1Driver = `import "core/no_prelude";
import "std/io";
import "./lexer";
import "./parser";
import "./wasm";
function main(): i32 { write(wasm.emit_module(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))))); return 0; }
`

const p2Driver = `import "core/no_prelude";
import "std/io";
import "./lexer";
import "./parser";
import "./wasm";
function main(): i32 { write(wasm.emit_module_run(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))))); return 0; }
`

// componentCompileDriver reads a (preview2 run) core WAT, assembles it to a
// binary core module, and wraps it into a wasi:cli/run component.
const componentCompileDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIO exercises component_full_io: the stdout
// I/O component framing (wat_component.fern), embedded as \xNN blobs around
// the user core. Given a core that uses the preview2 stdout imports and
// exports _lang_run, it must reproduce the native compiler's I/O component
// byte-for-byte. The test feeds the Go backend's own I/O core (from its
// `-target wasm` output for a printing program) to component_full_io and
// asserts byte-equality + that the component prints under wasmtime.
func TestSelfHostWasmComponentFullIO(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(`function main(): i32 { write("hi"); return 0; }`), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

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
	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_encode.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIODriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-component assembler: %v", err)
	}
	var got []byte
	for _, tok := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			t.Fatalf("bad byte %q: %v", tok, err)
		}
		got = append(got, byte(n))
	}
	if !bytesEqual(got, ref) {
		t.Fatalf("io component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.io.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io component: %v", err)
	}
	if string(stdout) != "hi" {
		t.Errorf("io component stdout = %q, want %q", string(stdout), "hi")
	}
}

// componentFullIODriver reads a (preview2 stdout) core and wraps it into a
// wasi:cli/run I/O component via component_full_io.
const componentFullIODriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.push(s[i]); i = i + 1; }
            var comp: i32[] = component_full_io(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int(comp[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentStdout exercises the fully self-hosted preview2
// stdout I/O path: source -> emit_module_run_io (a run core importing
// wasi:cli/stdout + wasi:io/streams, with a $fd_write shim over the stream)
// -> emit_binary -> component_full_io -> a wasi:cli/run component that
// prints under wasmtime. Asserts both stdout and the run() result
// (main()==0 -> exit 0; main()!=0 -> exit 1).
func TestSelfHostWasmComponentStdout(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-stdout e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-stdout e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIODriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	for _, tc := range []struct {
		name   string
		source string
		stdout string
		exit   int
	}{
		{"write", `function main(): i32 { write("hi"); return 0; }`, "hi", 0},
		{"write-newline", `function main(): i32 { write("hello world\n"); return 0; }`, "hello world\n", 0},
		{"fstring", `function main(): i32 { var n: i32 = 21; write(f"answer={n * 2}"); return 0; }`, "answer=42", 0},
		{"print-int", `function main(): i32 { print_int(42); return 0; }`, "42", 0},
		{"multi-write", `function main(): i32 { var i: i32 = 0; while (i < 3) { write("ab"); i = i + 1; } return 0; }`, "ababab", 0},
		{"err-path", `function main(): i32 { write("x"); return 5; }`, "x", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coreWat := runCapture(t, gcc, runner, ioBin, []byte(tc.source))
			if len(coreWat) == 0 {
				t.Fatal("preview2 io core WAT empty")
			}
			if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
				t.Fatalf("write core.wat: %v", err)
			}
			out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
			if err != nil {
				t.Fatalf("run io component assembler: %v", err)
			}
			var comp []byte
			for _, tok := range strings.Fields(string(out)) {
				n, err := strconv.Atoi(tok)
				if err != nil {
					t.Fatalf("bad byte %q: %v", tok, err)
				}
				comp = append(comp, byte(n))
			}
			compPath := filepath.Join(dir, tc.name+".io.wasm")
			if err := os.WriteFile(compPath, comp, 0o644); err != nil {
				t.Fatalf("write component: %v", err)
			}
			if vout, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
			}
			cmd := exec.Command(wasmtime, "run", compPath)
			stdout, _ := cmd.Output()
			if string(stdout) != tc.stdout {
				t.Errorf("stdout = %q, want %q", string(stdout), tc.stdout)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("exit = %d, want %d", code, tc.exit)
			}
		})
	}
}

const p2IODriver = `import "core/no_prelude";
import "std/io";
import "./lexer";
import "./parser";
import "./wasm";
function main(): i32 { write(wasm.emit_module_run_io(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))))); return 0; }
`

// componentCompileIODriver reads a preview2 stdout core WAT, assembles it,
// and wraps it into a wasi:cli/run I/O component via component_full_io.
const componentCompileIODriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOFS exercises component_full_io_fs: the
// read_file + stdout component framing (wat_component.fern), embedded as
// \xNN blobs around the core. Given the Go backend's own read_file core, it
// must reproduce the native compiler's file-I/O component byte-for-byte and
// run (reading a preopened file and printing its contents).
func TestSelfHostWasmComponentFullIOFS(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (read_file("in.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 2; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_encode.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs component assembler: %v", err)
	}
	var got []byte
	for _, tok := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			t.Fatalf("bad byte %q: %v", tok, err)
		}
		got = append(got, byte(n))
	}
	if !bytesEqual(got, ref) {
		t.Fatalf("io-fs component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofs.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	// Run it reading a preopened file.
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("file contents here"), 0o644); err != nil {
		t.Fatalf("write in.txt: %v", err)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs component: %v", err)
	}
	if string(stdout) != "file contents here" {
		t.Errorf("io-fs component stdout = %q, want %q", string(stdout), "file contents here")
	}
}

// componentFullIOFSDriver reads a (preview2 read_file+stdout) core and wraps
// it into a wasi:cli/run component via component_full_io_fs.
const componentFullIOFSDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.push(s[i]); i = i + 1; }
            var comp: i32[] = component_full_io_fs(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int(comp[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentReadFile exercises the fully self-hosted preview2
// read_file path: source -> emit_module_run_io_fs (a run core importing the
// wasi:filesystem read interfaces + the stdout shim, with a preview2
// $__fern_read_file + cabi_realloc) -> emit_binary -> component_full_io_fs ->
// a wasi:cli/run component that reads a preopened file under wasmtime.
func TestSelfHostWasmComponentReadFile(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-readfile e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-readfile e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 fs core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-fs assembler: %v", err)
		}
		var comp []byte
		for _, tok := range strings.Fields(string(out)) {
			n, _ := strconv.Atoi(tok)
			comp = append(comp, byte(n))
		}
		p := filepath.Join(dir, "comp.wasm")
		if err := os.WriteFile(p, comp, 0o644); err != nil {
			t.Fatalf("write comp: %v", err)
		}
		if vout, err := exec.Command(wasmtools, "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("validate: %v\n%s", err, vout)
		}
		return p
	}

	t.Run("read", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (read_file("in.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR"); return 1; } } return 2; }`)
		if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("the file contents"), 0o644); err != nil {
			t.Fatalf("write in.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output()
		if string(out) != "the file contents" {
			t.Errorf("read: stdout = %q, want %q", string(out), "the file contents")
		}
	})

	t.Run("missing-file-err", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (read_file("nope.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR"); return 1; } } return 2; }`)
		cmd := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp)
		out, _ := cmd.Output()
		if string(out) != "ERR" {
			t.Errorf("missing-file: stdout = %q, want %q", string(out), "ERR")
		}
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("missing-file: exit = %d, want 1", code)
		}
	})

	t.Run("large-file-loop", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (read_file("big.txt")) { Ok(s) => { print_int(s.len()); return 0; }, Err(e) => { return 1; } } return 2; }`)
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), make([]byte, 10000), 0o644); err != nil {
			t.Fatalf("write big.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output()
		if string(out) != "10000" {
			t.Errorf("large-file: stdout = %q, want %q (multi-chunk read loop)", string(out), "10000")
		}
	})
}

const p2FSDriver = `import "core/no_prelude";
import "std/io";
import "./lexer";
import "./parser";
import "./wasm";
function main(): i32 { write(wasm.emit_module_run_io_fs(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))))); return 0; }
`

// componentCompileIOFSDriver reads a preview2 read_file+stdout core WAT,
// assembles it, and wraps it via component_full_io_fs.
const componentCompileIOFSDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOFSWrite is the write counterpart of
// TestSelfHostWasmComponentFullIOFS: given the Go backend's own write_file +
// stdout core, the self-host's component_full_io_fs_write framing must
// reproduce the native compiler's file-write component byte-for-byte and run
// (creating a preopened file with the written contents).
func TestSelfHostWasmComponentFullIOFSWrite(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-write e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-write e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (write_file("out.txt", "hello from fern\n")) { Some(_) => { return 1; }, None => {} } write("wrote it\n"); return 0; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_encode.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSWriteDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-write component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs-write component assembler: %v", err)
	}
	var got []byte
	for _, tok := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			t.Fatalf("bad byte %q: %v", tok, err)
		}
		got = append(got, byte(n))
	}
	if !bytesEqual(got, ref) {
		t.Fatalf("io-fs-write component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofsw.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	// Run it; it should create out.txt with the written contents and print
	// "wrote it" to stdout.
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs-write component: %v", err)
	}
	if string(stdout) != "wrote it\n" {
		t.Errorf("io-fs-write component stdout = %q, want %q", string(stdout), "wrote it\n")
	}
	wrote, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back out.txt: %v", err)
	}
	if string(wrote) != "hello from fern\n" {
		t.Errorf("io-fs-write file = %q, want %q", string(wrote), "hello from fern\n")
	}
}

// componentFullIOFSWriteDriver reads a (preview2 write_file+stdout) core and
// wraps it into a wasi:cli/run component via component_full_io_fs_write.
const componentFullIOFSWriteDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.push(s[i]); i = i + 1; }
            var comp: i32[] = component_full_io_fs_write(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int(comp[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentWriteFile exercises the fully self-hosted preview2
// write_file path: source -> emit_module_run_io_fs (a run core importing the
// wasi:filesystem write interfaces + the stdout shim, with a preview2
// $__fern_write_file + cabi_realloc) -> emit_binary ->
// component_full_io_fs_write -> a wasi:cli/run component that writes a
// preopened file under wasmtime.
func TestSelfHostWasmComponentWriteFile(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-writefile e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-writefile e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern", "wat_component.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSWriteDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-write component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 fs core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-fs-write assembler: %v", err)
		}
		var comp []byte
		for _, tok := range strings.Fields(string(out)) {
			n, _ := strconv.Atoi(tok)
			comp = append(comp, byte(n))
		}
		p := filepath.Join(dir, "comp.wasm")
		if err := os.WriteFile(p, comp, 0o644); err != nil {
			t.Fatalf("write comp: %v", err)
		}
		if vout, err := exec.Command(wasmtools, "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("validate: %v\n%s", err, vout)
		}
		return p
	}

	t.Run("write", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (write_file("w.txt", "self-hosted write\n")) { Some(_) => { return 1; }, None => {} } write("ok\n"); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output()
		if string(out) != "ok\n" {
			t.Errorf("write: stdout = %q, want %q", string(out), "ok\n")
		}
		wrote, err := os.ReadFile(filepath.Join(dir, "w.txt"))
		if err != nil {
			t.Fatalf("read back w.txt: %v", err)
		}
		if string(wrote) != "self-hosted write\n" {
			t.Errorf("write: file = %q, want %q", string(wrote), "self-hosted write\n")
		}
	})

	t.Run("truncate-existing", func(t *testing.T) {
		// Pre-create with longer content; write_file must truncate.
		if err := os.WriteFile(filepath.Join(dir, "t.txt"), []byte("OLD LONGER CONTENT HERE"), 0o644); err != nil {
			t.Fatalf("seed t.txt: %v", err)
		}
		comp := build(t, `function main(): i32 { match (write_file("t.txt", "new")) { Some(_) => { return 1; }, None => {} } return 0; }`)
		if _, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output(); err != nil {
			t.Fatalf("run: %v", err)
		}
		wrote, err := os.ReadFile(filepath.Join(dir, "t.txt"))
		if err != nil {
			t.Fatalf("read back t.txt: %v", err)
		}
		if string(wrote) != "new" {
			t.Errorf("truncate: file = %q, want %q", string(wrote), "new")
		}
	})

	t.Run("large-write-loop", func(t *testing.T) {
		// 10000 bytes forces multiple <=4096-byte blocking-write chunks.
		comp := build(t, `function main(): i32 { var s: string = ""; var i: i32 = 0; while (i < 10000) { s = s + "x"; i = i + 1; } match (write_file("big.txt", s)) { Some(_) => { return 1; }, None => {} } return 0; }`)
		if _, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output(); err != nil {
			t.Fatalf("run: %v", err)
		}
		wrote, err := os.ReadFile(filepath.Join(dir, "big.txt"))
		if err != nil {
			t.Fatalf("read back big.txt: %v", err)
		}
		if len(wrote) != 10000 {
			t.Errorf("large-write: len = %d, want 10000 (multi-chunk write loop)", len(wrote))
		}
	})
}

// componentCompileIOFSWriteDriver reads a preview2 write_file+stdout core WAT,
// assembles it, and wraps it via component_full_io_fs_write.
const componentCompileIOFSWriteDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs_write(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`
