package e2eselfhost

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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// Assembler = encoder modules + wat_component + a wrapping driver.
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
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

	copySelfHostDriver(t, dir, "wasm_ir.fern")
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
	for _, name := range []string{"watbin.fern"} {
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
		// Allocating no-I/O programs: the mode-1 core carries the heap + RC
		// runtime with no imports at all to reach it, the one shape where a
		// missing allocator surfaces as a trap rather than a link error.
		{"alloc-string", `function main(): i32 { var s: string = "ab" + "cd"; if (s.len() == 4) { return 0; } return 1; }`, true},
		{"alloc-array", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var t: i32 = 0; var i: i32 = 0; while (i < xs.len()) { t = t + xs[i]; i = i + 1; } return t - 6; }", true},
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

const p1Driver = `
import "std/io";
import "./lexer";
import "./parser";
import "./wasm_ir";
function main(): i32 { write(wasm_ir.emit_module_mode_or_error(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))), false, false)); return 0; }
`

const p2Driver = `
import "std/io";
import "./lexer";
import "./parser";
import "./wasm_ir";
function main(): i32 { write(wasm_ir.emit_module_mode_or_error(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))), true, false)); return 0; }
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
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

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
		{"putchar", `function main(): i32 { putchar(72); putchar(105); putchar(33); return 0; }`, "Hi!", 0},
		{"putchar-loop", `function main(): i32 { var c: i32 = 97; while (c < 101) { putchar(c); c = c + 1; } return 0; }`, "abcd", 0},
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

const p2IODriver = `
import "std/io";
import "./lexer";
import "./parser";
import "./wasm_ir";
function main(): i32 { write(wasm_ir.emit_module_mode_or_error(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))), true, true)); return 0; }
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
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

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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

	// The Err payload must be a real, matchable IoError variant carrying the
	// path — the same thing every other target produces. The preview2 helper
	// stored the RAW wasi error code there until the fs shapes moved onto the
	// IR leg, so matching a variant on it dereferenced an integer as a variant
	// box. The sibling missing-file-err case above cannot see that: it takes
	// the Err branch but never inspects `e`, which is how it survived (#5795).
	t.Run("missing-file-err-variant", func(t *testing.T) {
		comp := build(t, `function main(): i32 {
			match (read_file("nope.txt")) {
				Ok(s) => { write("OK"); return 0; },
				Err(e) => {
					match (e) {
						NotFound(p) => { write("notfound:"); write(p); return 0; },
						PermissionDenied(p) => { write("denied"); return 1; },
						AlreadyExists(p) => { write("exists"); return 1; },
						InvalidUtf8(p) => { write("utf8"); return 1; },
						Interrupted => { write("intr"); return 1; },
						Unsupported => { write("unsup"); return 1; },
						Other(p, m) => { write("other:"); write(p); return 1; }
					}
				}
			}
			return 2;
		}`)
		cmd := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp)
		out, _ := cmd.Output()
		if string(out) != "notfound:nope.txt" {
			t.Errorf("missing-file variant = %q, want %q", string(out), "notfound:nope.txt")
		}
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("missing-file variant: exit = %d, want 0", code)
		}
	})

	// The same Err variant, but on the AST emitter rather than the IR one.
	// An fs shape normally lowers through the IR leg; padding the module past
	// eligible_core's 512-function budget pushes it onto the AST fallback,
	// whose variant boxes are 4-byte-slotted instead of 8. The boxer emitted
	// one layout for both consumers, so this leg read the path out of the id
	// slot; before that it stored the raw wasi error code here and there was
	// no variant to match at all (#5795).
	t.Run("missing-file-err-variant-ast", func(t *testing.T) {
		var pad strings.Builder
		for i := 0; i < 520; i++ {
			pad.WriteString("function fn" + strconv.Itoa(i) + "(x: i32): i32 { return x + " + strconv.Itoa(i) + "; }\n")
		}
		comp := build(t, pad.String()+`
		function main(): i32 {
			if (fn1(0) + fn519(0) == 123456) { write("never"); }
			match (read_file("nope.txt")) {
				Ok(s) => { write("OK"); return 0; },
				Err(e) => {
					match (e) {
						NotFound(p) => { write("notfound:"); write(p); return 0; },
						PermissionDenied(p) => { write("denied"); return 1; },
						AlreadyExists(p) => { write("exists"); return 1; },
						InvalidUtf8(p) => { write("utf8"); return 1; },
						Interrupted => { write("intr"); return 1; },
						Unsupported => { write("unsup"); return 1; },
						Other(p, m) => { write("other:"); write(p); return 1; }
					}
				}
			}
			return 2;
		}`)
		cmd := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp)
		out, _ := cmd.Output()
		if string(out) != "notfound:nope.txt" {
			t.Errorf("missing-file variant (AST leg) = %q, want %q", string(out), "notfound:nope.txt")
		}
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("missing-file variant (AST leg): exit = %d, want 0", code)
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

const p2FSDriver = `
import "std/io";
import "./lexer";
import "./parser";
import "./wasm_ir";
function main(): i32 { write(wasm_ir.emit_module_mode_or_error(parser.module_with_builtins(parser.parse_module(lexer.tokenize(io.read_all_stdin()))), true, true)); return 0; }
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
	src := `function main(): i32 { match (write_file("out.txt", "hello from fern\n")) { Err(_) => { return 1; }, Ok(_) => {} } write("wrote it\n"); return 0; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs_write(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
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

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
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
		comp := build(t, `function main(): i32 { match (write_file("w.txt", "self-hosted write\n")) { Err(_) => { return 1; }, Ok(_) => {} } write("ok\n"); return 0; }`)
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
		comp := build(t, `function main(): i32 { match (write_file("t.txt", "new")) { Err(_) => { return 1; }, Ok(_) => {} } return 0; }`)
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
		comp := build(t, `function main(): i32 { var s: string = ""; var i: i32 = 0; while (i < 10000) { s = s + "x"; i = i + 1; } match (write_file("big.txt", s)) { Err(_) => { return 1; }, Ok(_) => {} } return 0; }`)
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

// TestSelfHostWasmComponentFullIOFSRW is the read+write counterpart of the
// io-fs framing tests: given the Go backend's own read_file + write_file +
// stdout core, the self-host's component_full_io_fs_rw framing must reproduce
// the native compiler's combined file-I/O component byte-for-byte and run
// (copying a preopened file and printing a marker).
func TestSelfHostWasmComponentFullIOFSRW(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-rw e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-rw e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (read_file("in.txt")) { Ok(s) => { match (write_file("out.txt", s)) { Err(_) => { return 1; }, Ok(_) => {} } write("done\n"); return 0; }, Err(e) => { return 2; } } return 3; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSRWDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs-rw component assembler: %v", err)
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
		t.Fatalf("io-fs-rw component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofsrw.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	// Run it: copies in.txt -> out.txt and prints "done".
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("copy me over"), 0o644); err != nil {
		t.Fatalf("write in.txt: %v", err)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs-rw component: %v", err)
	}
	if string(stdout) != "done\n" {
		t.Errorf("io-fs-rw component stdout = %q, want %q", string(stdout), "done\n")
	}
	copied, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back out.txt: %v", err)
	}
	if string(copied) != "copy me over" {
		t.Errorf("io-fs-rw out.txt = %q, want %q", string(copied), "copy me over")
	}
}

// componentFullIOFSRWDriver reads a (preview2 read_file+write_file+stdout)
// core and wraps it into a wasi:cli/run component via component_full_io_fs_rw.
const componentFullIOFSRWDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs_rw(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentReadWriteFile exercises the fully self-hosted
// preview2 read+write path: source -> emit_module_run_io_fs (a run core
// importing both the wasi:filesystem read and write interfaces + the stdout
// shim, with preview2 $__fern_read_file + $__fern_write_file + cabi_realloc)
// -> emit_binary -> component_full_io_fs_rw -> a wasi:cli/run component that
// copies one preopened file to another under wasmtime.
func TestSelfHostWasmComponentReadWriteFile(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-readwritefile e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-readwritefile e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSRWDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw component assembler produced 0 bytes")
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
			t.Fatalf("run io-fs-rw assembler: %v", err)
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

	t.Run("copy", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (read_file("src.txt")) { Ok(s) => { match (write_file("dst.txt", s)) { Err(_) => { return 1; }, Ok(_) => {} } write("copied\n"); return 0; }, Err(e) => { return 2; } } return 3; }`)
		if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("round-trip via self-host"), 0o644); err != nil {
			t.Fatalf("write src.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output()
		if string(out) != "copied\n" {
			t.Errorf("copy: stdout = %q, want %q", string(out), "copied\n")
		}
		dst, err := os.ReadFile(filepath.Join(dir, "dst.txt"))
		if err != nil {
			t.Fatalf("read back dst.txt: %v", err)
		}
		if string(dst) != "round-trip via self-host" {
			t.Errorf("copy: dst.txt = %q, want %q", string(dst), "round-trip via self-host")
		}
	})

	t.Run("missing-src-err", func(t *testing.T) {
		// A missing read source short-circuits before any write. The
		// wasi:cli/run convention collapses any nonzero main() to an err
		// result (exit 1), so the "ERR" marker on stdout is the signal
		// that the Err arm ran, not the specific return value.
		comp := build(t, `function main(): i32 { match (read_file("nope.txt")) { Ok(s) => { match (write_file("dst.txt", s)) { Err(_) => { return 1; }, Ok(_) => {} } return 0; }, Err(e) => { write("ERR"); return 9; } } return 3; }`)
		cmd := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp)
		out, _ := cmd.Output()
		if string(out) != "ERR" {
			t.Errorf("missing-src: stdout = %q, want %q", string(out), "ERR")
		}
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("missing-src: exit = %d, want 1 (run collapses nonzero main)", code)
		}
	})
}

// componentCompileIOFSRWDriver reads a preview2 read_file+write_file+stdout
// core WAT, assembles it, and wraps it via component_full_io_fs_rw.
const componentCompileIOFSRWDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs_rw(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIORandom is the random+stdout framing test:
// given the Go backend's own random_bytes + stdout core, the self-host's
// component_full_io_random framing must reproduce the native compiler's
// component byte-for-byte, validate, and run.
func TestSelfHostWasmComponentFullIORandom(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-random e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-random e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var b: string = random_bytes(16); if (b.len() == 16) { write("ok\n"); return 0; } return 1; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIORandomDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-random component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-random component assembler: %v", err)
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
		t.Fatalf("io-random component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iorand.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-random component: %v", err)
	}
	if string(stdout) != "ok\n" {
		t.Errorf("io-random component stdout = %q, want %q", string(stdout), "ok\n")
	}
}

// componentFullIORandomDriver reads a (preview2 random+stdout) core and wraps
// it into a wasi:cli/run component via component_full_io_random.
const componentFullIORandomDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_random(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentRandom exercises the fully self-hosted preview2
// random path: source -> emit_module_run_io (a run core importing
// wasi:random/random's get-random-u64 + the stdout shim, with a preview2
// $__fern_random_bytes) -> emit_binary -> component_full_io_random -> a
// wasi:cli/run component that draws randomness under wasmtime.
func TestSelfHostWasmComponentRandom(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-random e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-random e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIORandomDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-random component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-random assembler: %v", err)
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

	t.Run("len", func(t *testing.T) {
		// random_bytes(16) returns 16 elements; print the count.
		comp := build(t, `function main(): i32 { var b: i32[] = random_bytes(16); print_int(b.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "16" {
			t.Errorf("len: stdout = %q, want %q", string(out), "16")
		}
	})

	t.Run("byte-range", func(t *testing.T) {
		// Every drawn byte must be in 0..255 (the shift+mask is correct).
		comp := build(t, `function main(): i32 { var b: i32[] = random_bytes(64); var i: i32 = 0; while (i < b.len()) { if (b[i] < 0) { return 1; } if (b[i] > 255) { return 2; } i = i + 1; } write("inrange\n"); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "inrange\n" {
			t.Errorf("byte-range: stdout = %q, want %q", string(out), "inrange\n")
		}
	})

	t.Run("non-multiple-of-8", func(t *testing.T) {
		// A length that isn't a multiple of 8 must still return exactly n.
		comp := build(t, `function main(): i32 { var b: i32[] = random_bytes(13); print_int(b.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "13" {
			t.Errorf("non-multiple-of-8: stdout = %q, want %q", string(out), "13")
		}
	})
}

// componentCompileIORandomDriver reads a preview2 random+stdout core WAT,
// assembles it, and wraps it via component_full_io_random.
const componentCompileIORandomDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_random(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOEnv is the env+stdout framing test: given
// the Go backend's own env + stdout core, the self-host's
// component_full_io_env framing must reproduce the native compiler's
// component byte-for-byte, validate, and run (reading a preopened env var).
func TestSelfHostWasmComponentFullIOEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-env e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (env("FERN_TEST")) { Some(v) => { write(v); write("\n"); return 0; }, None => { write("none\n"); return 0; } } return 1; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOEnvDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-env component assembler: %v", err)
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
		t.Fatalf("io-env component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.ioenv.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	cmd := exec.Command(wasmtime, "run", "--env", "FERN_TEST=hello-env", myPath)
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("wasmtime run io-env component: %v", err)
	}
	if string(stdout) != "hello-env\n" {
		t.Errorf("io-env component stdout = %q, want %q", string(stdout), "hello-env\n")
	}
}

// componentFullIOEnvDriver reads a (preview2 env+stdout) core and wraps it
// into a wasi:cli/run component via component_full_io_env.
const componentFullIOEnvDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_env(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentEnv exercises the fully self-hosted preview2 env
// path: source -> emit_module_run_io (a run core importing
// wasi:cli/environment's get-environment + the stdout shim + cabi_realloc,
// with a preview2 $__fern_env) -> emit_binary -> component_full_io_env -> a
// wasi:cli/run component that reads environment variables under wasmtime.
func TestSelfHostWasmComponentEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-env e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOEnvDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-env assembler: %v", err)
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

	t.Run("present", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (env("API_KEY")) { Some(v) => { write(v); return 0; }, None => { write("MISS"); return 1; } } return 2; }`)
		out, _ := exec.Command(wasmtime, "run", "--env", "API_KEY=sk-abc123", comp).Output()
		if string(out) != "sk-abc123" {
			t.Errorf("present: stdout = %q, want %q", string(out), "sk-abc123")
		}
	})

	t.Run("absent-none", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (env("NOPE_VAR")) { Some(v) => { write(v); return 0; }, None => { write("MISS"); return 0; } } return 2; }`)
		// No --env for NOPE_VAR; the None arm must run.
		out, _ := exec.Command(wasmtime, "run", "--env", "OTHER=1", comp).Output()
		if string(out) != "MISS" {
			t.Errorf("absent: stdout = %q, want %q", string(out), "MISS")
		}
	})

	t.Run("prefix-not-confused", func(t *testing.T) {
		// A var whose name is a prefix of another must not match (the
		// preview1 NAME= scan bug class); exact key compare required.
		comp := build(t, `function main(): i32 { match (env("FOO")) { Some(v) => { write(v); return 0; }, None => { write("MISS"); return 0; } } return 2; }`)
		out, _ := exec.Command(wasmtime, "run", "--env", "FOOBAR=wrong", "--env", "FOO=right", comp).Output()
		if string(out) != "right" {
			t.Errorf("prefix: stdout = %q, want %q", string(out), "right")
		}
	})
}

// componentCompileIOEnvDriver reads a preview2 env+stdout core WAT, assembles
// it, and wraps it via component_full_io_env.
const componentCompileIOEnvDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_env(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOArgs is the args+stdout framing test: given
// the Go backend's own args + stdout core, the self-host's
// component_full_io_args framing must reproduce the native compiler's
// component byte-for-byte, validate, and run.
func TestSelfHostWasmComponentFullIOArgs(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-args e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-args e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var a: string[] = args(); var i: i32 = 0; while (i < a.len()) { write(a[i]); write("\n"); i = i + 1; } return 0; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOArgsDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-args component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-args component assembler: %v", err)
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
		t.Fatalf("io-args component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.ioargs.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", myPath, "alpha", "beta").Output()
	if err != nil {
		t.Fatalf("wasmtime run io-args component: %v", err)
	}
	// argv[0] is the program name; then the two passed args.
	if !strings.HasSuffix(string(stdout), "alpha\nbeta\n") {
		t.Errorf("io-args component stdout = %q, want suffix %q", string(stdout), "alpha\nbeta\n")
	}
}

// componentFullIOArgsDriver reads a (preview2 args+stdout) core and wraps it
// into a wasi:cli/run component via component_full_io_args.
const componentFullIOArgsDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_args(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentArgs exercises the fully self-hosted preview2 args
// path: source -> emit_module_run_io (a run core importing
// wasi:cli/environment's get-arguments + the stdout shim + cabi_realloc, with
// a preview2 $__fern_args) -> emit_binary -> component_full_io_args -> a
// wasi:cli/run component that reads its argv under wasmtime.
func TestSelfHostWasmComponentArgs(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-args e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-args e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOArgsDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-args component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-args assembler: %v", err)
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

	t.Run("count", func(t *testing.T) {
		// argv[0] (program name) + 3 passed = 4.
		comp := build(t, `function main(): i32 { var a: string[] = args(); print_int(a.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp, "one", "two", "three").Output()
		if string(out) != "4" {
			t.Errorf("count: stdout = %q, want %q", string(out), "4")
		}
	})

	// arg_at(i) reads ONE argument rather than materialising the whole vector.
	// It shares the get-arguments import with args(), but had no preview2 body
	// of its own until #5818 — so the emitted core called an undefined
	// $arg_at, and component_shape (which classified the args category as
	// module_calls(mod, "args") only) wrapped it in the plain-stdout framing,
	// which has no get-arguments to satisfy it either.
	t.Run("arg-at", func(t *testing.T) {
		comp := build(t, `function main(): i32 { write(arg_at(1)); write("|"); write(arg_at(3)); write("|"); write(arg_at(9)); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp, "aa", "bb", "cc").Output()
		// argv[0] is the program name, so 1..3 are the passed args and 9 is
		// out of range — an empty string, matching preview1 arg_at.
		if string(out) != "aa|cc|" {
			t.Errorf("arg-at: stdout = %q, want %q", string(out), "aa|cc|")
		}
	})

	t.Run("values", func(t *testing.T) {
		// Print every arg after argv[0] (the program name).
		comp := build(t, `function main(): i32 { var a: string[] = args(); var i: i32 = 1; while (i < a.len()) { write(a[i]); write("|"); i = i + 1; } return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp, "x", "yy", "zzz").Output()
		if string(out) != "x|yy|zzz|" {
			t.Errorf("values: stdout = %q, want %q", string(out), "x|yy|zzz|")
		}
	})
}

// componentCompileIOArgsDriver reads a preview2 args+stdout core WAT,
// assembles it, and wraps it via component_full_io_args.
const componentCompileIOArgsDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_args(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOClock is the clock+stdout framing test:
// given the Go backend's own now_unix_ms + stdout core, the self-host's
// component_full_io_clock framing must reproduce the native compiler's
// component byte-for-byte, validate, and run.
func TestSelfHostWasmComponentFullIOClock(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-clock e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-clock e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var t: i64 = now_unix_ms(); if (t > 0) { write("ok\n"); return 0; } return 1; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOClockDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-clock component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-clock component assembler: %v", err)
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
		t.Fatalf("io-clock component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.ioclock.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-clock component: %v", err)
	}
	if string(stdout) != "ok\n" {
		t.Errorf("io-clock component stdout = %q, want %q", string(stdout), "ok\n")
	}
}

// componentFullIOClockDriver reads a (preview2 clock+stdout) core and wraps
// it into a wasi:cli/run component via component_full_io_clock.
const componentFullIOClockDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_clock(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentClock exercises the fully self-hosted preview2
// wall-clock path: source -> emit_module_run_io (a run core importing
// wasi:clocks/wall-clock's now + the stdout shim, with preview2 now_unix_ms /
// now_ns) -> emit_binary -> component_full_io_clock -> a wasi:cli/run
// component that reads the wall clock under wasmtime.
func TestSelfHostWasmComponentClock(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-clock e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-clock e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOClockDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-clock component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-clock assembler: %v", err)
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

	t.Run("now-positive", func(t *testing.T) {
		// now_unix_ms must be a large positive epoch-ms value (> year 2020
		// in ms = 1577836800000), proving seconds*1000 + nanos/1e6 is right.
		comp := build(t, `function main(): i32 { var t: i64 = now_unix_ms(); if (t > 1577836800000) { write("recent\n"); return 0; } write("bad\n"); return 1; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "recent\n" {
			t.Errorf("now-positive: stdout = %q, want %q", string(out), "recent\n")
		}
	})

	t.Run("now-ns-bigger-than-ms", func(t *testing.T) {
		// now_ns (seconds*1e9 + nanos) must exceed now_unix_ms by ~1e6x;
		// a coarse check that the two wall-clock readings use distinct math.
		comp := build(t, `function main(): i32 { var ns: i64 = now_ns(); var ms: i64 = now_unix_ms(); if (ns > ms) { write("ns>ms\n"); return 0; } return 1; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "ns>ms\n" {
			t.Errorf("now-ns: stdout = %q, want %q", string(out), "ns>ms\n")
		}
	})

	t.Run("now-to-string", func(t *testing.T) {
		// Formatting the clock reading composes the wall-clock import with the
		// wide `.to_string()` formatter (#5826) — the shape that used to bail
		// the whole component to the AST emitter. The value moves, so the
		// assertion is on its shape: epoch-ms is 13 digits through the year
		// 2286, all of them decimal.
		comp := build(t, `function main(): i32 {
    var s: string = now_unix_ms().to_string();
    if (s.len() != 13) { write("len\n"); return 1; }
    var i: i32 = 0;
    while (i < s.len()) {
        if (s[i] < 48) { write("digit\n"); return 2; }
        if (s[i] > 57) { write("digit\n"); return 2; }
        i = i + 1;
    }
    write("ms-ok\n");
    return 0;
}`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "ms-ok\n" {
			t.Errorf("now-to-string: stdout = %q, want %q", string(out), "ms-ok\n")
		}
	})
}

// componentCompileIOClockDriver reads a preview2 clock+stdout core WAT,
// assembles it, and wraps it via component_full_io_clock.
const componentCompileIOClockDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_clock(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOClockMono is the monotonic-clock+stdout
// framing test: given the Go backend's own monotonic_ns + stdout core, the
// self-host's component_full_io_clock_mono framing must reproduce native's
// component byte-for-byte, validate, and run.
func TestSelfHostWasmComponentFullIOClockMono(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-clock-mono e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-clock-mono e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var t: i64 = monotonic_ns(); if (t > 0) { write("ok\n"); return 0; } return 1; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOClockMonoDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-clock-mono component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-clock-mono component assembler: %v", err)
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
		t.Fatalf("io-clock-mono component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.ioclockmono.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-clock-mono component: %v", err)
	}
	if string(stdout) != "ok\n" {
		t.Errorf("io-clock-mono component stdout = %q, want %q", string(stdout), "ok\n")
	}
}

// componentFullIOClockMonoDriver reads a (preview2 monotonic-clock+stdout)
// core and wraps it via component_full_io_clock_mono.
const componentFullIOClockMonoDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_clock_mono(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentClockMono exercises the fully self-hosted preview2
// monotonic-clock path: source -> emit_module_run_io (a run core importing
// wasi:clocks/monotonic-clock's now + the stdout shim, with a preview2
// monotonic_ns) -> emit_binary -> component_full_io_clock_mono -> a
// wasi:cli/run component that reads the monotonic clock under wasmtime.
func TestSelfHostWasmComponentClockMono(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-clock-mono e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-clock-mono e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOClockMonoDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-clock-mono component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-clock-mono assembler: %v", err)
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

	t.Run("monotonic-advances", func(t *testing.T) {
		// Two monotonic reads with a busy loop between them: the second
		// must be >= the first (monotonic never goes backwards).
		comp := build(t, `function main(): i32 { var a: i64 = monotonic_ns(); var s: i64 = 0i64; var i: i32 = 0; while (i < 100000) { s = s + 1i64; i = i + 1; } var b: i64 = monotonic_ns(); if (b >= a) { write("monotonic\n"); return 0; } return 1; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "monotonic\n" {
			t.Errorf("monotonic-advances: stdout = %q, want %q", string(out), "monotonic\n")
		}
	})
}

// componentCompileIOClockMonoDriver reads a preview2 monotonic-clock+stdout
// core WAT, assembles it, and wraps it via component_full_io_clock_mono.
const componentCompileIOClockMonoDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_clock_mono(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOFSReadEnv is the read_file+env+stdout
// combination framing test: given the Go backend's own read+env+stdout core,
// the self-host's component_full_io_fs_read_env framing must reproduce
// native's component byte-for-byte, validate, and run. Proves the canonical-
// import-order reorder lets a multi-import combination wire up byte-
// identically (no per-combination native blob needed beyond the framing).
func TestSelfHostWasmComponentFullIOFSReadEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-read-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-read-env e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (read_file("config.txt")) { Ok(cfg) => { match (env("API_KEY")) { Some(k) => { write(cfg); write(k); return 0; }, None => { write(cfg); write("no-key"); return 0; } } }, Err(e) => { write("ERR"); return 1; } } return 2; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSReadEnvDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-read-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs-read-env component assembler: %v", err)
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
		t.Fatalf("io-fs-read-env component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofsreadenv.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.txt"), []byte("cfg:"), 0o644); err != nil {
		t.Fatalf("write config.txt: %v", err)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", "--env", "API_KEY=secret", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs-read-env component: %v", err)
	}
	if string(stdout) != "cfg:secret" {
		t.Errorf("io-fs-read-env component stdout = %q, want %q", string(stdout), "cfg:secret")
	}
}

// componentFullIOFSReadEnvDriver reads a (preview2 read+env+stdout) core and
// wraps it via component_full_io_fs_read_env.
const componentFullIOFSReadEnvDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs_read_env(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentReadEnv exercises the fully self-hosted preview2
// read_file+env path end to end: source -> emit_module_run_io_fs (a run core
// importing both env's get-environment and the fs read chain + the stdout
// shim) -> emit_binary -> component_full_io_fs_read_env -> a wasi:cli/run
// component that reads a config file AND an env var under wasmtime. The
// canonical edge-handler shape.
func TestSelfHostWasmComponentReadEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-read-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-read-env e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSReadEnvDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-read-env component assembler produced 0 bytes")
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
			t.Fatalf("run io-fs-read-env assembler: %v", err)
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

	t.Run("config-and-secret", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (read_file("cfg.txt")) { Ok(c) => { match (env("TOKEN")) { Some(tok) => { write(c); write("|"); write(tok); return 0; }, None => { write(c); write("|none"); return 0; } } }, Err(e) => { write("ERR"); return 1; } } return 2; }`)
		if err := os.WriteFile(filepath.Join(dir, "cfg.txt"), []byte("host=localhost"), 0o644); err != nil {
			t.Fatalf("write cfg.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", "--env", "TOKEN=abc123", comp).Output()
		if string(out) != "host=localhost|abc123" {
			t.Errorf("config-and-secret: stdout = %q, want %q", string(out), "host=localhost|abc123")
		}
	})

	t.Run("missing-env-falls-back", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (read_file("cfg.txt")) { Ok(c) => { match (env("TOKEN")) { Some(tok) => { write(c); write("|"); write(tok); return 0; }, None => { write(c); write("|none"); return 0; } } }, Err(e) => { write("ERR"); return 1; } } return 2; }`)
		// cfg.txt exists from the prior subtest; no TOKEN passed.
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", "--env", "OTHER=x", comp).Output()
		if string(out) != "host=localhost|none" {
			t.Errorf("missing-env: stdout = %q, want %q", string(out), "host=localhost|none")
		}
	})
}

// componentCompileIOFSReadEnvDriver reads a preview2 read+env+stdout core WAT,
// assembles it, and wraps it via component_full_io_fs_read_env.
const componentCompileIOFSReadEnvDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs_read_env(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOFSRWEnv is the read+write+env+stdout
// combination framing test: given the Go backend's own core, the self-host's
// component_full_io_fs_rw_env framing must reproduce native's component
// byte-for-byte, validate, and run. The richest realistic edge-handler shape.
func TestSelfHostWasmComponentFullIOFSRWEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-rw-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-rw-env e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (read_file("in.txt")) { Ok(cfg) => { var line: string = cfg; match (env("SUFFIX")) { Err(s) => { line = line + s; }, Ok(_) => {} } match (write_file("out.txt", line)) { Err(e) => { return 1; }, Ok(_) => {} } write("done\n"); return 0; }, Err(e) => { write("ERR"); return 2; } } return 3; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSRWEnvDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs-rw-env component assembler: %v", err)
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
		t.Fatalf("io-fs-rw-env component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofsrwenv.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write in.txt: %v", err)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", "--env", "SUFFIX=-X", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs-rw-env component: %v", err)
	}
	if string(stdout) != "done\n" {
		t.Errorf("io-fs-rw-env component stdout = %q, want %q", string(stdout), "done\n")
	}
	wrote, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back out.txt: %v", err)
	}
	if string(wrote) != "base-X" {
		t.Errorf("io-fs-rw-env out.txt = %q, want %q", string(wrote), "base-X")
	}
}

// componentFullIOFSRWEnvDriver reads a (read+write+env+stdout) core and wraps
// it via component_full_io_fs_rw_env.
const componentFullIOFSRWEnvDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs_rw_env(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentReadWriteEnv exercises the fully self-hosted
// preview2 read+write+env path end to end -- the full edge-handler shape:
// read a config file, read an env var, write an output file, respond.
func TestSelfHostWasmComponentReadWriteEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-read-write-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-read-write-env e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSRWEnvDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw-env component assembler produced 0 bytes")
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
			t.Fatalf("run io-fs-rw-env assembler: %v", err)
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

	t.Run("read-transform-write", func(t *testing.T) {
		// Read in.txt, append env PREFIX-derived text, write out.txt, respond.
		comp := build(t, `function main(): i32 { match (read_file("in.txt")) { Ok(c) => { var o: string = c; match (env("TAG")) { Err(tg) => { o = o + "[" + tg + "]"; }, Ok(_) => { o = o + "[none]"; } } match (write_file("out.txt", o)) { Err(e) => { return 1; }, Ok(_) => {} } write("wrote\n"); return 0; }, Err(e) => { write("ERR"); return 2; } } return 3; }`)
		if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("payload"), 0o644); err != nil {
			t.Fatalf("write in.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", "--env", "TAG=prod", comp).Output()
		if string(out) != "wrote\n" {
			t.Errorf("rtw: stdout = %q, want %q", string(out), "wrote\n")
		}
		wrote, err := os.ReadFile(filepath.Join(dir, "out.txt"))
		if err != nil {
			t.Fatalf("read back out.txt: %v", err)
		}
		if string(wrote) != "payload[prod]" {
			t.Errorf("rtw: out.txt = %q, want %q", string(wrote), "payload[prod]")
		}
	})
}

// componentCompileIOFSRWEnvDriver reads a read+write+env+stdout core WAT,
// assembles it, and wraps it via component_full_io_fs_rw_env.
const componentCompileIOFSRWEnvDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs_rw_env(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIORandomWrite is the random+write+stdout
// combination framing test: given the Go backend's own core, the self-host's
// component_full_io_random_write framing must reproduce native's component
// byte-for-byte, validate, and run. Mixes a memory-free import (random,
// lowered without memory) with the memory-dependent fs write chain.
func TestSelfHostWasmComponentFullIORandomWrite(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-random-write e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-random-write e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var id: string = random_bytes(8); match (write_file("id.bin", id)) { Err(e) => { return 1; }, Ok(_) => {} } write("saved\n"); return 0; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIORandomWriteDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-random-write component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-random-write component assembler: %v", err)
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
		t.Fatalf("io-random-write component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iorandwrite.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", myPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run io-random-write component: %v", err)
	}
	if string(stdout) != "saved\n" {
		t.Errorf("io-random-write component stdout = %q, want %q", string(stdout), "saved\n")
	}
	idb, err := os.ReadFile(filepath.Join(dir, "id.bin"))
	if err != nil {
		t.Fatalf("read back id.bin: %v", err)
	}
	if len(idb) != 8 {
		t.Errorf("io-random-write id.bin len = %d, want 8", len(idb))
	}
}

// componentFullIORandomWriteDriver reads a (random+write+stdout) core and
// wraps it via component_full_io_random_write.
const componentFullIORandomWriteDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_random_write(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentRandomWrite exercises the fully self-hosted preview2
// random+write path end to end -- the token-generating handler shape: draw a
// random id, persist it to a file, respond.
func TestSelfHostWasmComponentRandomWrite(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-random-write e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-random-write e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIORandomWriteDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-random-write component assembler produced 0 bytes")
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
			t.Fatalf("run io-random-write assembler: %v", err)
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

	t.Run("gen-and-persist", func(t *testing.T) {
		// Draw 16 random bytes (as a string), write them, report the count.
		comp := build(t, `function main(): i32 { var id: string = random_bytes(16); match (write_file("token.bin", id)) { Err(e) => { return 1; }, Ok(_) => {} } print_int(id.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp).Output()
		if string(out) != "16" {
			t.Errorf("gen-and-persist: stdout = %q, want %q", string(out), "16")
		}
		tok, err := os.ReadFile(filepath.Join(dir, "token.bin"))
		if err != nil {
			t.Fatalf("read back token.bin: %v", err)
		}
		if len(tok) != 16 {
			t.Errorf("gen-and-persist: token.bin len = %d, want 16", len(tok))
		}
	})
}

// componentCompileIORandomWriteDriver reads a random+write+stdout core WAT,
// assembles it, and wraps it via component_full_io_random_write.
const componentCompileIORandomWriteDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_random_write(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOEprint is the eprint+stdout framing test:
// given the Go backend's own eprint+stdout core, the self-host's
// component_full_io_eprint framing must reproduce native's component
// byte-for-byte, validate, and run (writing to both stdout and stderr).
func TestSelfHostWasmComponentFullIOEprint(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-eprint e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-eprint e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { write("out\n"); eprint("err\n"); return 0; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOEprintDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-eprint component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-eprint component assembler: %v", err)
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
		t.Fatalf("io-eprint component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.ioeprint.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	cmd := exec.Command(wasmtime, "run", myPath)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("wasmtime run io-eprint component: %v", err)
	}
	if string(stdout) != "out\n" {
		t.Errorf("io-eprint stdout = %q, want %q", string(stdout), "out\n")
	}
	if stderrBuf.String() != "err\n\n" {
		t.Errorf("io-eprint stderr = %q, want %q", stderrBuf.String(), "err\n\n")
	}
}

// componentFullIOEprintDriver reads a (eprint+stdout) core and wraps it via
// component_full_io_eprint.
const componentFullIOEprintDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_eprint(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentEprint exercises the fully self-hosted preview2
// eprint path end to end: source -> emit_module_run_io (a run core importing
// wasi:cli/stderr's get-stderr + the stdout pair, with a preview2
// $__fern_eprint stderr shim) -> emit_binary -> component_full_io_eprint -> a
// wasi:cli/run component that logs to stderr (and stdout) under wasmtime.
func TestSelfHostWasmComponentEprint(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-eprint e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-eprint e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOEprintDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-eprint component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-eprint assembler: %v", err)
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

	t.Run("stderr-and-stdout", func(t *testing.T) {
		comp := build(t, `function main(): i32 { write("to-out"); eprint("to-err"); return 0; }`)
		cmd := exec.Command(wasmtime, "run", comp)
		var eb strings.Builder
		cmd.Stderr = &eb
		out, _ := cmd.Output()
		if string(out) != "to-out" {
			t.Errorf("stdout = %q, want %q", string(out), "to-out")
		}
		if eb.String() != "to-err\n" {
			t.Errorf("stderr = %q, want %q", eb.String(), "to-err\n")
		}
	})

	t.Run("eprint-only", func(t *testing.T) {
		// No stdout output: the unused stdout import is harmless; stderr works.
		comp := build(t, `function main(): i32 { eprint("just-err\n"); return 0; }`)
		cmd := exec.Command(wasmtime, "run", comp)
		var eb strings.Builder
		cmd.Stderr = &eb
		out, _ := cmd.Output()
		if string(out) != "" {
			t.Errorf("eprint-only stdout = %q, want empty", string(out))
		}
		if eb.String() != "just-err\n\n" {
			t.Errorf("eprint-only stderr = %q, want %q", eb.String(), "just-err\n\n")
		}
	})
}

// componentCompileIOEprintDriver reads a eprint+stdout core WAT, assembles it,
// and wraps it via component_full_io_eprint.
const componentCompileIOEprintDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_eprint(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOExit is the exit+stdout framing test: given
// the Go backend's own exit+stdout core, the self-host's
// component_full_io_exit framing must reproduce native's component
// byte-for-byte, validate, and run (exit(0) terminates cleanly after stdout).
func TestSelfHostWasmComponentFullIOExit(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-exit e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-exit e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { write("hi\n"); exit(0); return 0; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOExitDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-exit component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-exit component assembler: %v", err)
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
		t.Fatalf("io-exit component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.ioexit.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	cmd := exec.Command(wasmtime, "run", myPath)
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("wasmtime run io-exit component: %v", err)
	}
	if string(stdout) != "hi\n" {
		t.Errorf("io-exit stdout = %q, want %q", string(stdout), "hi\n")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("io-exit exit = %d, want 0", code)
	}
}

// componentFullIOExitDriver reads a (exit+stdout) core and wraps it via
// component_full_io_exit.
const componentFullIOExitDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_exit(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentExit exercises the fully self-hosted preview2 exit
// path end to end: source -> emit_module_run_io (a run core importing
// wasi:cli/exit + the stdout pair, with a preview2 $__fern_exit) ->
// emit_binary -> component_full_io_exit -> a wasi:cli/run component that
// terminates early under wasmtime.
func TestSelfHostWasmComponentExit(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-exit e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-exit e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOExitDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-exit component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(source))
		if len(coreWat) == 0 {
			t.Fatal("preview2 io core WAT empty")
		}
		if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
			t.Fatalf("write core.wat: %v", err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
		if err != nil {
			t.Fatalf("run io-exit assembler: %v", err)
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

	t.Run("exit-zero-early", func(t *testing.T) {
		// exit(0) terminates before the second write; stdout shows only "a".
		comp := build(t, `function main(): i32 { write("a"); exit(0); write("b"); return 0; }`)
		cmd := exec.Command(wasmtime, "run", comp)
		out, _ := cmd.Output()
		if string(out) != "a" {
			t.Errorf("exit-zero: stdout = %q, want %q", string(out), "a")
		}
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("exit-zero: exit = %d, want 0", code)
		}
	})

	t.Run("exit-one", func(t *testing.T) {
		// exit(1) -> err discriminant -> exit code 1, after printing "x".
		comp := build(t, `function main(): i32 { write("x"); exit(1); return 0; }`)
		cmd := exec.Command(wasmtime, "run", comp)
		out, _ := cmd.Output()
		if string(out) != "x" {
			t.Errorf("exit-one: stdout = %q, want %q", string(out), "x")
		}
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("exit-one: exit = %d, want 1", code)
		}
	})
}

// componentCompileIOExitDriver reads a exit+stdout core WAT, assembles it, and
// wraps it via component_full_io_exit.
const componentCompileIOExitDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_exit(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOFSArgsRead is the args+read_file+stdout
// combination framing test: given the Go backend's own core, the self-host's
// component_full_io_fs_args_read framing must reproduce native's component
// byte-for-byte, validate, and run. The canonical CLI-tool shape.
func TestSelfHostWasmComponentFullIOFSArgsRead(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-args-read e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-args-read e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var a: string[] = args(); if (a.len() < 2) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR\n"); return 2; } } return 3; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSArgsReadDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-args-read component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs-args-read component assembler: %v", err)
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
		t.Fatalf("io-fs-args-read component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofsargsread.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("file body"), 0o644); err != nil {
		t.Fatalf("write data.txt: %v", err)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", myPath, "data.txt").Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs-args-read component: %v", err)
	}
	if string(stdout) != "file body" {
		t.Errorf("io-fs-args-read stdout = %q, want %q", string(stdout), "file body")
	}
}

// componentFullIOFSArgsReadDriver reads a (args+read+stdout) core and wraps it
// via component_full_io_fs_args_read.
const componentFullIOFSArgsReadDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs_args_read(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentArgsRead exercises the fully self-hosted preview2
// args+read path end to end -- the canonical CLI-tool shape: read a filename
// from argv, read that file, print it.
func TestSelfHostWasmComponentArgsRead(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-args-read e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-args-read e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSArgsReadDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-args-read component assembler produced 0 bytes")
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
			t.Fatalf("run io-fs-args-read assembler: %v", err)
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

	t.Run("cat-argv-file", func(t *testing.T) {
		comp := build(t, `function main(): i32 { var a: string[] = args(); if (a.len() < 2) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR\n"); return 2; } } return 3; }`)
		if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("contents via argv"), 0o644); err != nil {
			t.Fatalf("write hello.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp, "hello.txt").Output()
		if string(out) != "contents via argv" {
			t.Errorf("cat-argv-file: stdout = %q, want %q", string(out), "contents via argv")
		}
	})

	t.Run("missing-arg-usage", func(t *testing.T) {
		comp := build(t, `function main(): i32 { var a: string[] = args(); if (a.len() < 2) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR\n"); return 2; } } return 3; }`)
		// argv[0] only (program name); the usage arm runs, exit 1.
		cmd := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp)
		out, _ := cmd.Output()
		if string(out) != "usage\n" {
			t.Errorf("missing-arg: stdout = %q, want %q", string(out), "usage\n")
		}
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("missing-arg: exit = %d, want 1", code)
		}
	})
}

// componentCompileIOFSArgsReadDriver reads an args+read+stdout core WAT,
// assembles it, and wraps it via component_full_io_fs_args_read.
const componentCompileIOFSArgsReadDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs_args_read(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentFullIOFSRWArgs is the args+read+write+stdout
// framing test (file-transform CLI shape): given the Go backend's own core,
// the self-host's component_full_io_fs_rw_args framing must reproduce native's
// component byte-for-byte, validate, and run (copy a file named in argv to
// another). Its prefix is io_fs_rw_head() + an args tail (phase-3 composition).
func TestSelfHostWasmComponentFullIOFSRWArgs(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-rw-args e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-rw-args e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { var a: string[] = args(); if (a.len() < 3) { write("usage: tool IN OUT\n"); return 1; } match (read_file(a[1])) { Ok(s) => { match (write_file(a[2], s)) { Err(e) => { write("write err\n"); return 2; }, Ok(_) => {} } write("ok\n"); return 0; }, Err(e) => { write("read err\n"); return 3; } } return 4; }`
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

	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIOFSRWArgsDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw-args component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).Output()
	if err != nil {
		t.Fatalf("run io-fs-rw-args component assembler: %v", err)
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
		t.Fatalf("io-fs-rw-args component differs from Go reference: got %d bytes, want %d", len(got), len(ref))
	}
	myPath := filepath.Join(dir, "mine.iofsrwargs.wasm")
	if err := os.WriteFile(myPath, got, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", myPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("transform me"), 0o644); err != nil {
		t.Fatalf("write in.txt: %v", err)
	}
	stdout, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", myPath, "in.txt", "out.txt").Output()
	if err != nil {
		t.Fatalf("wasmtime run io-fs-rw-args component: %v", err)
	}
	if string(stdout) != "ok\n" {
		t.Errorf("io-fs-rw-args stdout = %q, want %q", string(stdout), "ok\n")
	}
	cp, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back out.txt: %v", err)
	}
	if string(cp) != "transform me" {
		t.Errorf("io-fs-rw-args out.txt = %q, want %q", string(cp), "transform me")
	}
}

// componentFullIOFSRWArgsDriver reads a (args+read+write+stdout) core and wraps
// it via component_full_io_fs_rw_args.
const componentFullIOFSRWArgsDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = [];
            var i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            var comp: i32[] = component_full_io_fs_rw_args(core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostWasmComponentArgsReadWrite exercises the fully self-hosted
// preview2 args+read+write path end to end -- the file-transform CLI shape:
// read a file named in argv[1], write it to argv[2].
func TestSelfHostWasmComponentArgsReadWrite(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-args-read-write e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-args-read-write e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileIOFSRWArgsDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(asmSrc.String()))
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw-args component assembler produced 0 bytes")
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
			t.Fatalf("run io-fs-rw-args assembler: %v", err)
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

	t.Run("transform-in-to-out", func(t *testing.T) {
		comp := build(t, `function main(): i32 { var a: string[] = args(); if (a.len() < 3) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { match (write_file(a[2], s)) { Err(e) => { return 2; }, Ok(_) => {} } write("done\n"); return 0; }, Err(e) => { write("ERR\n"); return 3; } } return 4; }`)
		if err := os.WriteFile(filepath.Join(dir, "src.dat"), []byte("copy this payload"), 0o644); err != nil {
			t.Fatalf("write src.dat: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp, "src.dat", "dst.dat").Output()
		if string(out) != "done\n" {
			t.Errorf("transform: stdout = %q, want %q", string(out), "done\n")
		}
		dst, err := os.ReadFile(filepath.Join(dir, "dst.dat"))
		if err != nil {
			t.Fatalf("read back dst.dat: %v", err)
		}
		if string(dst) != "copy this payload" {
			t.Errorf("transform: dst.dat = %q, want %q", string(dst), "copy this payload")
		}
	})
}

// componentCompileIOFSRWArgsDriver reads an args+read+write+stdout core WAT,
// assembles it, and wraps it via component_full_io_fs_rw_args.
const componentCompileIOFSRWArgsDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            var core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            var comp: i32[] = component_full_io_fs_rw_args(core);
            var i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`
