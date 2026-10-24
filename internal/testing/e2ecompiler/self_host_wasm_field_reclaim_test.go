package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostFieldReclaimWasm(t *testing.T) {
	boxedProbes(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm field-reclaim e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const reclaimSrc = "struct Acc { items: i32[] } " +
		"function build(a: Acc, n: i32): Acc { let i: i32 = 0; while (i < n) { a = Acc { items: [i, i, i, i] }; i = i + 1; } return a; } " +
		"function main(): i32 { let seed: Acc = Acc { items: [0] }; let r: Acc = build(seed, 2000000); return r.items[0] - 1999999; }"
	const cap = "16777216"

	cases := []struct {
		name   string
		src    string
		capped bool
		exit   int
	}{
		{"consume-rebind-reclaim", reclaimSrc, true, 0},
		{"snapshot-guard-caller-intact",
			"struct Acc { items: i32[] } " +
				"function build(a: Acc, n: i32): Acc { let i: i32 = 0; while (i < n) { a = Acc { items: [i, i, i] }; i = i + 1; } return a; } " +
				"function main(): i32 { let seed: Acc = Acc { items: [42, 7, 1] }; let r: Acc = build(seed, 5000); return (seed.items[0] + seed.items[1] + seed.items[2] - 50) + (r.items[0] - 4999); }",
			false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			ws := string(wat)
			at := strings.Index(ws, "(func $build ")
			if at < 0 {
				t.Fatalf("%s: no $build in the WAT", tc.name)
			}
			build, _, _ := strings.Cut(ws[at:], "\n  (func ")
			if !strings.Contains(build, "call $__sem_release_Acc") {
				t.Fatalf("%s: $build releases no superseded Acc\n--- WAT ---\n%s", tc.name, wat)
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			args := []string{"run"}
			if tc.capped {
				args = append(args, "-W", "max-memory-size="+cap, "-W", "trap-on-grow-failure=y")
			}
			args = append(args, "--dir", dir, watPath)
			cmd := exec.Command("wasmtime", args...)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				detail := ""
				if tc.capped {
					detail = " (a trap means the consume-rebind intermediates leaked past the " + cap + "-byte cap — the rebind did not reclaim)"
				}
				t.Errorf("%s: wasm exited %d, want %d%s\n--- WAT ---\n%s", tc.name, code, tc.exit, detail, wat)
			}
		})
	}
}
