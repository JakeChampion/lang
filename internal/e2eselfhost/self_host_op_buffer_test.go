package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const opBufferProgram = `import "./ir";
function check(n: i32): i32 {
    var buffer: ir.OpBuffer = ir.op_buffer();
    var snapshots: ir.OpBuffer[] = [];
    var i: i32 = 0;
    while (i < n) {
        if (i % 31 == 0) { snapshots = snapshots.append(buffer); }
        buffer = buffer.append(ir.Op { ...ir.op_const_i32(i), str: "instruction" });
        i = i + 1;
    }
    if (buffer.len() != n) { return 1; }
    var fallback: ir.Op = ir.op_const_i32(0 - 1);
    if (buffer.last_or(fallback).i32_imm != n - 1) { return 2; }
    var flat: ir.Op[] = buffer.to_array();
    if (flat.len() != n) { return 3; }
    var extended: ir.OpBuffer = buffer.append(ir.op_const_i32(0 - 7));
    var fork: ir.OpBuffer = buffer.append(ir.op_const_i32(0 - 9));
    if (buffer.len() != n || extended.len() != n + 1 || fork.len() != n + 1) { return 4; }
    if (extended.last_or(fallback).i32_imm != 0 - 7 || fork.last_or(fallback).i32_imm != 0 - 9) { return 5; }
    var again: ir.Op[] = buffer.to_array();
    i = 0;
    while (i < n) {
        if (flat[i].i32_imm != i || again[i].i32_imm != i) { return 6; }
        if (flat[i].kind_tag != ir.kind_id("const_i32") || flat[i].str != "instruction") { return 7; }
        i = i + 1;
    }
    i = 0;
    while (i < snapshots.len()) {
        var snapshot: ir.OpBuffer = snapshots[i];
        if (snapshot.len() != i * 31 || snapshot.last_or(fallback).i32_imm != i * 31 - 1) { return 8; }
        var prefix: ir.Op[] = snapshot.to_array();
        if (prefix.len() != i * 31) { return 9; }
        var j: i32 = 0;
        while (j < prefix.len()) {
            if (prefix[j].i32_imm != j || prefix[j].str != "instruction") { return 10; }
            j = j + 1;
        }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    var sizes: i32[] = [0, 1, 31, 32, 33, 63, 64, 65, 1023, 1024, 1025, 4096];
    for n in sizes {
        var code: i32 = check(n);
        if (code != 0) { return code; }
    }
    if (__rc_underflow_count() != 0) { return 11; }
    return 0;
}`

func TestSelfHostOpBufferSnapshots(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_modload_run.fern", "wasm_modload_run.fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			name := "asm_modload_run.fern"
			if target == "wasm32-wasi" {
				name = "wasm_modload_run.fern"
			}
			driver := buildSelfHostBin(t, gcc, dir, name, strings.TrimSuffix(name, ".fern"))
			proj := t.TempDir()
			copySelfHostFiles(t, proj, "ir.fern", "builtins.fern")
			entry := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(entry, []byte(opBufferProgram), 0o644); err != nil {
				t.Fatal(err)
			}
			drive := func(args ...string) []byte {
				t.Helper()
				return runDriverFile(t, runner, driver, entry, args...)
			}
			var command *exec.Cmd
			if target == "wasm32-wasi" {
				wasmtime, err := exec.LookPath("wasmtime")
				if err != nil {
					t.Skip("wasmtime not on PATH")
				}
				count, err := strconv.Atoi(strings.TrimSpace(string(drive("-per-module-count"))))
				if err != nil || count <= 0 {
					t.Fatalf("module count: %d, %v", count, err)
				}
				cache := filepath.Join(proj, "cache")
				if err := os.Mkdir(cache, 0o755); err != nil {
					t.Fatal(err)
				}
				for i := range count {
					drive("-per-module-emit", strconv.Itoa(i), "-cache-dir", cache)
				}
				wat := filepath.Join(proj, "program.wat")
				if err := os.WriteFile(wat, drive("-link", "-cache-dir", cache), 0o644); err != nil {
					t.Fatal(err)
				}
				command = exec.Command(wasmtime, "run", wat)
			} else {
				assembly := string(drive("-target", target))
				if target == "arm64-linux" {
					armgcc, armrunner := arm64Tooling(t)
					binary := buildBinArm64(t, armgcc, proj, "buffer", assembly)
					command = runArm64Bin(armrunner, binary)
				} else {
					binary := buildBin(t, gcc, proj, "buffer", assembly)
					command = runX86_64Bin(runner, binary)
				}
			}
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("snapshot program: %v\n%s", err, out)
			}
		})
	}
}
