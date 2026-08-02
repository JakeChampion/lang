package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostComposeFromWorld is the end-to-end gate for the self-host
// world-driven composer: the self-hosted compiler decodes the fern world,
// parses a stdout core's imports, classifies them against the world, emits the
// full world import prefix, and wires the suffix — producing a component that
// validates under wasm-tools and runs under wasmtime, printing the program's
// output. This is the self-host mirror of the Go ComposeFromWorldAuto, and
// completes P2 in both compilers.
func TestSelfHostComposeFromWorld(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	// Compile a stdout program and extract its core module.
	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	const want = "hi from the self-host world"
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(`function main(): i32 { write("`+want+`"); return 0; }`), 0o644); err != nil {
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
	core := componentCoreSection(t, ref)
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), core, 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	// Build the self-host wasm emitter, then compile a driver that turns the
	// core into a component via the world-driven path.
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	var src strings.Builder
	for _, name := range []string{"watbin.fern", "wit_decode.fern", "wit_compose.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src.Write(b)
		src.WriteByte('\n')
	}
	src.WriteString(witPayloadFunc(t, "FERN_BIN", "fern"))
	src.WriteString(selfHostComposeWorldDriver)

	driverWat := runCapture(t, gcc, runner, driverBin, []byte(src.String()))
	if len(driverWat) == 0 {
		t.Fatal("driver produced 0 bytes")
	}
	driverWatPath := filepath.Join(dir, "driver.wat")
	if err := os.WriteFile(driverWatPath, driverWat, 0o644); err != nil {
		t.Fatalf("write driver wat: %v", err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, driverWatPath).Output()
	if err != nil {
		t.Fatalf("run driver: %v", err)
	}
	var comp []byte
	for _, tok := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			t.Fatalf("bad byte %q: %v", tok, err)
		}
		comp = append(comp, byte(n))
	}
	mine := filepath.Join(dir, "world.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if vout, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	stdout, err := exec.Command(wasmtime, "run", mine).Output()
	if err != nil {
		t.Fatalf("wasmtime run component: %v", err)
	}
	if string(stdout) != want {
		t.Errorf("component stdout = %q, want %q", string(stdout), want)
	}
	_ = componenttype.PayloadFor // keep import used via witPayloadFunc
}

// selfHostComposeWorldDriver: component_from_world (the self-host orchestration)
// + a main that reads the core, decodes the fern world, and emits the
// world-driven component as space-separated decimal bytes.
const selfHostComposeWorldDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = blob_to_bytes(s);
            var tbody: i32[] = wit_section_body(blob_to_bytes(FERN_BIN()), 7);
            var comp: i32[] = component_from_world(tbody, core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int(comp[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// TestSelfHostComposeFromUserWorld is the self-host P3 gate: the self-hosted
// compiler composes a component from a USER-supplied WIT world (a minimal
// stdout-only one), not the embedded fern world. The component imports only
// the user world's three interfaces and runs under wasmtime. Self-host mirror
// of TestComposeFromUserWorld.
func TestSelfHostComposeFromUserWorld(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	// Author + embed a minimal stdout-only world; pull out its payload.
	witDir := filepath.Join(dir, "wit")
	_ = os.MkdirAll(witDir, 0o755)
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps")).CombinedOutput(); err != nil {
		t.Fatalf("copy deps: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(witDir, "byo.wit"),
		[]byte("package local:byo@0.0.0;\nworld stdout-only {\n    import wasi:cli/stdout@0.2.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write byo.wit: %v", err)
	}
	emptyWat := filepath.Join(dir, "empty.wat")
	emptyWasm := filepath.Join(dir, "empty.wasm")
	embedded := filepath.Join(dir, "embedded.wasm")
	_ = os.WriteFile(emptyWat, []byte("(module)"), 0o644)
	if out, err := exec.Command(wasmtools, "parse", emptyWat, "-o", emptyWasm).CombinedOutput(); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtools, "component", "embed", witDir, "-w", "stdout-only", emptyWasm, "-o", embedded).CombinedOutput(); err != nil {
		t.Fatalf("embed: %v\n%s", err, out)
	}
	embeddedBytes, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	payload := extractComponentType(t, embeddedBytes)

	// Compile a stdout program and extract its core.
	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	const want = "hi from a self-host user world"
	progPath := filepath.Join(dir, "prog.fern")
	_ = os.WriteFile(progPath, []byte(`function main(): i32 { write("`+want+`"); return 0; }`), 0o644)
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, _ := os.ReadFile(refPath)
	if err := os.WriteFile(filepath.Join(dir, "core.bin"), componentCoreSection(t, ref), 0o644); err != nil {
		t.Fatalf("write core.bin: %v", err)
	}

	// Build the self-host emitter and a driver that composes from the user world.
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		b, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		_ = os.WriteFile(filepath.Join(dir, name), b, 0o644)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	var src strings.Builder
	for _, name := range []string{"watbin.fern", "wit_decode.fern", "wit_compose.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src.Write(b)
		src.WriteByte('\n')
	}
	src.WriteString(witBytesFunc("USER_BIN", payload))
	src.WriteString(selfHostComposeUserDriver)

	driverWat := runCapture(t, gcc, runner, driverBin, []byte(src.String()))
	if len(driverWat) == 0 {
		t.Fatal("driver produced 0 bytes")
	}
	driverWatPath := filepath.Join(dir, "driver.wat")
	_ = os.WriteFile(driverWatPath, driverWat, 0o644)
	out, err := exec.Command(wasmtime, "run", "--dir", dir, driverWatPath).Output()
	if err != nil {
		t.Fatalf("run driver: %v", err)
	}
	var comp []byte
	for _, tok := range strings.Fields(string(out)) {
		n, _ := strconv.Atoi(tok)
		comp = append(comp, byte(n))
	}
	mine := filepath.Join(dir, "user-world.wasm")
	_ = os.WriteFile(mine, comp, 0o644)
	if vout, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("validate: %v\n%s", err, vout)
	}
	wit, err := exec.Command(wasmtools, "component", "wit", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("component wit: %v\n%s", err, wit)
	}
	for _, iface := range []string{"wasi:io/error@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/stdout@0.2.0"} {
		if !strings.Contains(string(wit), iface) {
			t.Errorf("component WIT missing %q", iface)
		}
	}
	if strings.Contains(string(wit), "wasi:sockets/") || strings.Contains(string(wit), "wasi:filesystem/") {
		t.Errorf("component imports interfaces outside the user world:\n%s", wit)
	}
	stdout, err := exec.Command(wasmtime, "run", mine).Output()
	if err != nil {
		t.Fatalf("wasmtime run component: %v", err)
	}
	if string(stdout) != want {
		t.Errorf("stdout = %q, want %q", string(stdout), want)
	}
}

// selfHostComposeUserDriver composes from a user world payload (USER_BIN).
const selfHostComposeUserDriver = `
function main(): i32 {
    match (read_file("core.bin")) {
        Ok(s) => {
            var core: i32[] = blob_to_bytes(s);
            var tbody: i32[] = wit_section_body(blob_to_bytes(USER_BIN()), 7);
            var comp: i32[] = component_from_world(tbody, core);
            var j: i32 = 0;
            while (j < comp.len()) { print_int(comp[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`
