package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../../compiler", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentWrapDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(withPrintInt(asmSrc.String())))
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
		{"struct", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 1, y: 2 }; return p.x + p.y; }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(withPrintInt(tc.source)))
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

const componentWrapDriver = `
function main(): i32 {
    match (read_file("target.wat")) {
        Ok(wat) => {
            let core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            let bytes: i32[] = component_wrap(core);
            let i: i32 = 0;
            while (i < bytes.len()) { print_int(bytes[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

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

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../../compiler", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(withPrintInt(asmSrc.String())))
	if len(asmWat) == 0 {
		t.Fatal("component-full assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

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
			if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
				t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
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

const componentFullDriver = `
function main(): i32 {
    match (read_file_bytes("core.bin")) {
        Ok(s) => {
            let core: i32[] = [];
            let i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            let comp: i32[] = component_full(core);
            let j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_p2.fern"), []byte(p2Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run_p2.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	p2Bin := buildSelfHostBin(t, gcc, dir, "wasm_run_p2.fern", "wasm_run_p2")

	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../../compiler", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentCompileDriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(withPrintInt(asmSrc.String())))
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
		ok     bool
	}{
		{"return0", "function main(): i32 { return 0; }", true},
		{"return42", "function main(): i32 { return 42; }", false},
		{"compute-zero", "function main(): i32 { let x: i32 = 5; let y: i32 = 5; return x - y; }", true},
		{"compute-nonzero", "function main(): i32 { let n: i32 = 3; return n * 7; }", false},
		{"alloc-string", `function main(): i32 { let s: string = "ab" + "cd"; if (s.len() == 4) { return 0; } return 1; }`, true},
		{"alloc-array", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let t: i32 = 0; let i: i32 = 0; while (i < xs.len()) { t = t + xs[i]; i = i + 1; } return t - 6; }", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coreWat := runCapture(t, gcc, runner, p2Bin, []byte(withPrintInt(tc.source)))
			if len(coreWat) == 0 {
				t.Fatal("preview2 core WAT empty")
			}
			if err := os.WriteFile(filepath.Join(dir, "core.wat"), coreWat, 0o644); err != nil {
				t.Fatalf("write core.wat: %v", err)
			}
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
import "./semlower";
function main(): i32 {
    let src: string = "";
    match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
    let d: semlower.Driven = semlower.driven(parser.parse_module(lexer.tokenize(src)), "wasm32-wasi");
    write(wasm_ir.emit_module_mode_or_error_sub(d.full, false, false, d.sub));
    return 0;
}
`

const p2Driver = `
import "std/io";
import "./lexer";
import "./parser";
import "./wasm_ir";
import "./semlower";
function main(): i32 {
    let src: string = "";
    match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
    let d: semlower.Driven = semlower.driven(parser.parse_module(lexer.tokenize(src)), "wasm32-wasi");
    write(wasm_ir.emit_module_mode_or_error_sub(d.full, true, false, d.sub));
    return 0;
}
`

const componentCompileDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            let core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            let comp: i32[] = component_full(core);
            let i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

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
	if err := os.WriteFile(progPath, []byte(`function main(): i32 { putchar(104); putchar(105); return 0; }`), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	var asmSrc strings.Builder
	for _, name := range []string{"watbin.fern"} {
		b, err := os.ReadFile(filepath.Join("../../../compiler", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		asmSrc.Write(b)
		asmSrc.WriteByte('\n')
	}
	asmSrc.WriteString(componentFullIODriver)
	asmWat := runCapture(t, gcc, runner, driverBin, []byte(withPrintInt(asmSrc.String())))
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

const componentFullIODriver = `
function main(): i32 {
    match (read_file_bytes("core.bin")) {
        Ok(s) => {
            let core: i32[] = [];
            let i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            let comp: i32[] = component_full_io(core);
            let j: i32 = 0;
            while (j < comp.len()) { print_int((comp[j] as i32)); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
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
		{"multi-write", `function main(): i32 { let i: i32 = 0; while (i < 3) { write("ab"); i = i + 1; } return 0; }`, "ababab", 0},
		{"err-path", `function main(): i32 { write("x"); return 5; }`, "x", 1},
		{"putchar", `function main(): i32 { putchar(72); putchar(105); putchar(33); return 0; }`, "Hi!", 0},
		{"putchar-loop", `function main(): i32 { let c: i32 = 97; while (c < 101) { putchar(c); c = c + 1; } return 0; }`, "abcd", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(tc.source)))
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
import "./semlower";
function main(): i32 {
    let src: string = "";
    match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
    let d: semlower.Driven = semlower.driven(parser.parse_module(lexer.tokenize(src)), "wasm32-wasi");
    write(wasm_ir.emit_module_mode_or_error_sub(d.full, true, true, d.sub));
    return 0;
}
`

func TestSelfHostWasmComponentFullIOFS(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
						Other(p, m, _) => { write("other:"); write(p); return 1; }
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
						Other(p, m, _) => { write("other:"); write(p); return 1; }
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
import "./semlower";
function main(): i32 {
    let src: string = "";
    match (io.read_all_stdin()) { Ok(text) => { src = text; }, Err(_) => { return 253; } }
    let d: semlower.Driven = semlower.driven(parser.parse_module(lexer.tokenize(src)), "wasm32-wasi");
    write(wasm_ir.emit_module_mode_or_error_sub(d.full, true, true, d.sub));
    return 0;
}
`

func TestSelfHostWasmComponentFullIOFSWrite(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-write e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-write e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs-write component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let s: string = ""; let i: i32 = 0; while (i < 10000) { s = s + "x"; i = i + 1; } match (write_file("big.txt", s)) { Err(_) => { return 1; }, Ok(_) => {} } return 0; }`)
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

func TestSelfHostWasmComponentFullIOFSRW(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-rw e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-rw e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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

func TestSelfHostWasmComponentFullIORandom(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-random e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-random e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let b: u8[] = random_bytes(16); if (b.len() == 16) { write("ok\n"); return 0; } return 1; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-random component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let b: u8[] = random_bytes(16); print_int(b.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "16" {
			t.Errorf("len: stdout = %q, want %q", string(out), "16")
		}
	})

	t.Run("byte-range", func(t *testing.T) {
		comp := build(t, `function main(): i32 { let b: u8[] = random_bytes(64); let i: i32 = 0; while (i < b.len()) { if ((b[i] as i32) < 0) { return 1; } if ((b[i] as i32) > 255) { return 2; } i = i + 1; } write("inrange\n"); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "inrange\n" {
			t.Errorf("byte-range: stdout = %q, want %q", string(out), "inrange\n")
		}
	})

	t.Run("non-multiple-of-8", func(t *testing.T) {
		comp := build(t, `function main(): i32 { let b: u8[] = random_bytes(13); print_int(b.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "13" {
			t.Errorf("non-multiple-of-8: stdout = %q, want %q", string(out), "13")
		}
	})
}

func TestSelfHostWasmComponentFullIOEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-env e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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
		out, _ := exec.Command(wasmtime, "run", "--env", "OTHER=1", comp).Output()
		if string(out) != "MISS" {
			t.Errorf("absent: stdout = %q, want %q", string(out), "MISS")
		}
	})

	t.Run("prefix-not-confused", func(t *testing.T) {
		comp := build(t, `function main(): i32 { match (env("FOO")) { Some(v) => { write(v); return 0; }, None => { write("MISS"); return 0; } } return 2; }`)
		out, _ := exec.Command(wasmtime, "run", "--env", "FOOBAR=wrong", "--env", "FOO=right", comp).Output()
		if string(out) != "right" {
			t.Errorf("prefix: stdout = %q, want %q", string(out), "right")
		}
	})
}

func TestSelfHostWasmComponentFullIOArgs(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-args e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-args e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let a: string[] = args(); let i: i32 = 0; while (i < a.len()) { write(a[i]); write("\n"); i = i + 1; } return 0; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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
	if !strings.HasSuffix(string(stdout), "alpha\nbeta\n") {
		t.Errorf("io-args component stdout = %q, want suffix %q", string(stdout), "alpha\nbeta\n")
	}
}

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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-args component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let a: string[] = args(); print_int(a.len()); return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp, "one", "two", "three").Output()
		if string(out) != "4" {
			t.Errorf("count: stdout = %q, want %q", string(out), "4")
		}
	})

	t.Run("values", func(t *testing.T) {
		comp := build(t, `function main(): i32 { let a: string[] = args(); let i: i32 = 1; while (i < a.len()) { write(a[i]); write("|"); i = i + 1; } return 0; }`)
		out, _ := exec.Command(wasmtime, "run", comp, "x", "yy", "zzz").Output()
		if string(out) != "x|yy|zzz|" {
			t.Errorf("values: stdout = %q, want %q", string(out), "x|yy|zzz|")
		}
	})
}

func TestSelfHostWasmComponentFullIOClock(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-clock e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-clock e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let t: i64 = now_unix_ms(); if (t > 0) { write("ok\n"); return 0; } return 1; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-clock component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let t: i64 = now_unix_ms(); if (t > 1577836800000) { write("recent\n"); return 0; } write("bad\n"); return 1; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "recent\n" {
			t.Errorf("now-positive: stdout = %q, want %q", string(out), "recent\n")
		}
	})

	t.Run("now-ns-bigger-than-ms", func(t *testing.T) {
		comp := build(t, `function main(): i32 { let ns: i64 = now_ns(); let ms: i64 = now_unix_ms(); if (ns > ms) { write("ns>ms\n"); return 0; } return 1; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "ns>ms\n" {
			t.Errorf("now-ns: stdout = %q, want %q", string(out), "ns>ms\n")
		}
	})
}

func TestSelfHostWasmComponentFullIOClockMono(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-clock-mono e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-clock-mono e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let t: i64 = monotonic_ns(); if (t > 0) { write("ok\n"); return 0; } return 1; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-clock-mono component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let a: i64 = monotonic_ns(); let s: i64 = 0i64; let i: i32 = 0; while (i < 100000) { s = s + 1i64; i = i + 1; } let b: i64 = monotonic_ns(); if (b >= a) { write("monotonic\n"); return 0; } return 1; }`)
		out, _ := exec.Command(wasmtime, "run", comp).Output()
		if string(out) != "monotonic\n" {
			t.Errorf("monotonic-advances: stdout = %q, want %q", string(out), "monotonic\n")
		}
	})
}

func TestSelfHostWasmComponentFullIOFSReadEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-read-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-read-env e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs-read-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", "--env", "OTHER=x", comp).Output()
		if string(out) != "host=localhost|none" {
			t.Errorf("missing-env: stdout = %q, want %q", string(out), "host=localhost|none")
		}
	})
}

func TestSelfHostWasmComponentFullIOFSRWEnv(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-rw-env e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-rw-env e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { match (read_file("in.txt")) { Ok(cfg) => { let line: string = cfg; match (env("SUFFIX")) { Some(s) => { line = line + s; }, None => {} } match (write_file("out.txt", line)) { Err(e) => { return 1; }, Ok(_) => {} } write("done\n"); return 0; }, Err(e) => { write("ERR"); return 2; } } return 3; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw-env component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { match (read_file("in.txt")) { Ok(c) => { let o: string = c; match (env("TAG")) { Some(tg) => { o = o + "[" + tg + "]"; }, None => { o = o + "[none]"; } } match (write_file("out.txt", o)) { Err(e) => { return 1; }, Ok(_) => {} } write("wrote\n"); return 0; }, Err(e) => { write("ERR"); return 2; } } return 3; }`)
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

func TestSelfHostWasmComponentFullIORandomWrite(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-random-write e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-random-write e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let id: u8[] = random_bytes(8); match (write_file("id.bin", string_from_bytes_unchecked(id))) { Err(e) => { return 1; }, Ok(_) => {} } write("saved\n"); return 0; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-random-write component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let id: u8[] = random_bytes(16); match (write_file("token.bin", string_from_bytes_unchecked(id))) { Err(e) => { return 1; }, Ok(_) => {} } print_int(id.len()); return 0; }`)
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

func TestSelfHostWasmComponentFullIOEprint(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-eprint e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-eprint e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-eprint component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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

func TestSelfHostWasmComponentFullIOExit(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-exit e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-exit e2e")
	}
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-exit component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, ioBin, []byte(withPrintInt(source)))
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

func TestSelfHostWasmComponentFullIOFSArgsRead(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-args-read e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-args-read e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let a: string[] = args(); if (a.len() < 2) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR\n"); return 2; } } return 3; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs-args-read component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let a: string[] = args(); if (a.len() < 2) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR\n"); return 2; } } return 3; }`)
		if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("contents via argv"), 0o644); err != nil {
			t.Fatalf("write hello.txt: %v", err)
		}
		out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", comp, "hello.txt").Output()
		if string(out) != "contents via argv" {
			t.Errorf("cat-argv-file: stdout = %q, want %q", string(out), "contents via argv")
		}
	})

	t.Run("missing-arg-usage", func(t *testing.T) {
		comp := build(t, `function main(): i32 { let a: string[] = args(); if (a.len() < 2) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { write(s); return 0; }, Err(e) => { write("ERR\n"); return 2; } } return 3; }`)
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

func TestSelfHostWasmComponentFullIOFSRWArgs(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component-full-io-fs-rw-args e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping component-full-io-fs-rw-args e2e")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	progPath := filepath.Join(dir, "prog.fern")
	src := `function main(): i32 { let a: string[] = args(); if (a.len() < 3) { write("usage: tool IN OUT\n"); return 1; } match (read_file(a[1])) { Ok(s) => { match (write_file(a[2], s)) { Err(e) => { write("write err\n"); return 2; }, Ok(_) => {} } write("ok\n"); return 0; }, Err(e) => { write("read err\n"); return 3; } } return 4; }`
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	asmWat := worldComponentAssemblerWAT(t)
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

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run.fern"), []byte(p1Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io_fs.fern"), []byte(p2FSDriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io_fs.fern: %v", err)
	}
	fsBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io_fs.fern", "wasm_run_io_fs")

	asmWat := worldComponentAssemblerWAT(t)
	if len(asmWat) == 0 {
		t.Fatal("io-fs-rw-args component assembler produced 0 bytes")
	}
	asmWatPath := filepath.Join(dir, "casm.wat")
	if err := os.WriteFile(asmWatPath, asmWat, 0o644); err != nil {
		t.Fatalf("write casm wat: %v", err)
	}

	build := func(t *testing.T, source string) string {
		coreWat := runCapture(t, gcc, runner, fsBin, []byte(withPrintInt(source)))
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
		comp := build(t, `function main(): i32 { let a: string[] = args(); if (a.len() < 3) { write("usage\n"); return 1; } match (read_file(a[1])) { Ok(s) => { match (write_file(a[2], s)) { Err(e) => { return 2; }, Ok(_) => {} } write("done\n"); return 0; }, Err(e) => { write("ERR\n"); return 3; } } return 4; }`)
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

const componentCompileIOFSRWArgsDriver = `
function main(): i32 {
    match (read_file("core.wat")) {
        Ok(wat) => {
            let core: i32[] = emit_binary(wat_parse(wat_tokenize(wat)));
            let comp: i32[] = component_full_io_fs_rw_args(core);
            let i: i32 = 0;
            while (i < comp.len()) { print_int(comp[i]); write("\n"); i = i + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`
