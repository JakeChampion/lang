package e2e

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

	var src strings.Builder
	for _, name := range []string{"leb128.fern", "wat_encode.fern", "wat_component.fern", "wit_decode.fern"} {
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
function component_from_world(tbody: i32[], core: i32[]): i32[] {
    var ci: WitCoreImports = wit_core_func_imports(core);
    var n: i32 = ci.ifaces.len();
    var kinds: i32[] = [];
    var insts: i32[] = [];
    var rts: i32[] = [];
    var i: i32 = 0;
    while (i < n) {
        kinds = kinds.push(wit_classify(tbody, ci.ifaces[i], ci.names[i]));
        insts = insts.push(wit_import_instance_index(tbody, ci.ifaces[i]));
        rts = rts.push(0);
        i = i + 1;
    }
    var pl: WitPrefixLayout = wit_prefix_layout(tbody);
    var suffix: i32[] = component_suffix(ci.ifaces, ci.names, kinds, insts, ci.params, rts, pl.types, pl.instances, 0, "_lang_run");
    var o: i32[] = component_preamble();
    o = wcat(o, wit_emit_world_imports(tbody));
    o = wcat(o, comp_core_module_section(core));
    return wcat(o, suffix);
}
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
