package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestComposeFromUserWorld is P3: bring-your-own WIT. A user-authored world
// (here a minimal stdout-only one) is embedded with wasm-tools, decoded from
// its raw component-type bytes (not an embedded fern/http payload), and used
// to compose a stdout program. The resulting component imports *only* the
// interfaces the user's world declares (io/error + io/streams + cli/stdout —
// three, vs the fern world's fourteen), and runs under wasmtime. This proves
// the import surface is driven by the supplied WIT, not hardcoded.
func TestComposeFromUserWorld(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()

	// A user's WIT directory: the wasi deps + a custom minimal world.
	witDir := filepath.Join(dir, "wit")
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps")).CombinedOutput(); err != nil {
		// cp -r needs the parent to exist
		_ = os.MkdirAll(witDir, 0o755)
		if out2, err2 := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps")).CombinedOutput(); err2 != nil {
			t.Fatalf("copy deps: %v / %v\n%s%s", err, err2, out, out2)
		}
	}
	const worldName = "stdout-only"
	if err := os.WriteFile(filepath.Join(witDir, "byo.wit"),
		[]byte("package local:byo@0.0.0;\nworld "+worldName+" {\n    import wasi:cli/stdout@0.2.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write byo.wit: %v", err)
	}

	// Embed the world into an empty core and pull out the component-type payload.
	emptyWat := filepath.Join(dir, "empty.wat")
	emptyWasm := filepath.Join(dir, "empty.wasm")
	embedded := filepath.Join(dir, "embedded.wasm")
	if err := os.WriteFile(emptyWat, []byte("(module)"), 0o644); err != nil {
		t.Fatalf("write empty.wat: %v", err)
	}
	if out, err := exec.Command(wasmtools, "parse", emptyWat, "-o", emptyWasm).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtools, "component", "embed", witDir, "-w", worldName, emptyWasm, "-o", embedded).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
	}
	embeddedBytes, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	payload := extractComponentType(t, embeddedBytes)

	// Decode the user world from its raw bytes — the bring-your-own entry point.
	w, err := componenttype.DecodeWorldBytes(payload)
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}
	if got := len(w.Interfaces()); got != 3 {
		t.Errorf("user world has %d interfaces, want 3", got)
	}

	// Compile a stdout program and compose it against the user world.
	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}
	const want = "hi from a user world"
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(`function main(): i32 { write("`+want+`"); return 0; }`), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	core := componentCoreSection(t, ref)

	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "user-world.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}

	// The component imports only the user world's three interfaces.
	wit, err := exec.Command(wasmtools, "component", "wit", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	for _, iface := range []string{"wasi:io/error@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/stdout@0.2.0"} {
		if !strings.Contains(string(wit), iface) {
			t.Errorf("component WIT missing %q", iface)
		}
	}
	for _, iface := range []string{"wasi:sockets/tcp", "wasi:filesystem/types", "wasi:random/random"} {
		if strings.Contains(string(wit), iface) {
			t.Errorf("component WIT unexpectedly imports %q (not in the user world)", iface)
		}
	}

	stdout, err := exec.Command(wasmtime, "run", mine).Output()
	if err != nil {
		t.Fatalf("wasmtime run: %v", err)
	}
	if string(stdout) != want {
		t.Errorf("stdout = %q, want %q", string(stdout), want)
	}
}
