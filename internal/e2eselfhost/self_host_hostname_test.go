package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostHostnameSource prints the node name and exits by whether it
// matched `want`. The value comes from os.Hostname — uname(2)'s nodename,
// the same kernel field the helper reads — so a body that copied the
// neighbouring sysname field ("Linux") fails here rather than passing a
// non-empty check.
func selfHostHostnameSource(want string) string {
	return fmt.Sprintf(`function main(): i32 {
    var h: string = hostname();
    print(h);
    if (h == %q) { return 0; }
    return 1;
}
`, want)
}

func selfHostHostHostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil || h == "" {
		t.Fatalf("os.Hostname: %q, %v", h, err)
	}
	return h
}

// TestSelfHostHostnameIR pins the x86-64 lowering: the Fern-source runtime
// helper (asmcore.rt_src_hostname) compiled through the self-host's own IR
// path, reading nodename at offset 65 of the struct uname(2) fills.
func TestSelfHostHostnameIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("hostname test runs only natively (compares the host's own name)")
	}
	want := selfHostHostHostname(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostHostnameSource(want)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fn___fern_hostname")) {
		t.Fatal("hostname did not reach the IR runtime path (no __fn___fern_hostname in asm)")
	}
	progBin := buildBin(t, gcc, dir, "hostname_prog", string(asm))
	run := exec.Command(progBin)
	out, _ := run.Output()
	if code := run.ProcessState.ExitCode(); code != 0 || strings.TrimSpace(string(out)) != want {
		t.Errorf("hostname() = %q (exit %d), want %q", strings.TrimSpace(string(out)), code, want)
	}
}

// The arm64-linux leg: the same Fern body with the asm-generic uname number,
// run under qemu — which reports the host's own nodename.
func TestSelfHostHostnameIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("hostname test runs only natively (compares the host's own name)")
	}
	want := selfHostHostHostname(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostHostnameSource(want)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_hostname")) {
		t.Fatal("hostname did not reach the arm64 IR runtime path (no bl __fn___fern_hostname in asm)")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "hostname_arm64", string(asm))
	run := runArm64Bin(qemu, bin)
	out, _ := run.Output()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("arm64 hostname program did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 0 || strings.TrimSpace(string(out)) != want {
		t.Errorf("arm64 hostname() = %q (exit %d), want %q", strings.TrimSpace(string(out)), code, want)
	}
}

// The wasm leg answers the empty string — a component has no node name —
// and the test runs the module rather than reading the WAT, because a
// wrong stack discipline around the scratch local is a validation error at
// instantiation, not a textual difference.
func TestSelfHostHostnameIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host hostname wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    var h: string = hostname();
    if (h.len() != 0) { return 1; }
    if (h != "") { return 2; }
    var s: string = "[" + h + "]";
    if (s != "[]") { return 3; }
    return 0;
}`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "hostname.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (the code names the case)\n%s", code, wat)
	}
}
