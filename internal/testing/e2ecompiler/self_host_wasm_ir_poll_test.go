package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelfHostWasmIRPoll(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR poll e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR poll e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR poll e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	build := func(t *testing.T, name, src string) (string, time.Duration) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(withPrintInt(src)))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		if !strings.Contains(string(wat), "call $__fern_wasm_poll") {
			t.Fatalf("%s: emitted WAT has no `call $__fern_wasm_poll` (module bailed off the IR path?)", name)
		}
		watPath := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, name+".core.wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		embedPath := filepath.Join(dir, name+".embed.wasm")
		if out, err := exec.Command(wasmtools, "component", "embed", witDir,
			"-w", "fern", corePath, "-o", embedPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
		}
		compPath := filepath.Join(dir, name+".component.wasm")
		if out, err := exec.Command(wasmtools, "component", "new", embedPath,
			"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component new: %v\n%s", err, out)
		}
		if out, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate: %v\n%s", err, out)
		}
		start := time.Now()
		out, err := exec.Command(wasmtime, "run", compPath).Output()
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("wasmtime run: %v", err)
		}
		return string(out), elapsed
	}

	t.Run("blocks_until_ready", func(t *testing.T) {
		out, elapsed := build(t, "poll_block", `function main(): i32 {
    let d: i64 = 400000000i64;
    let p: i32 = wasm_timer_pollable(d);
    let ps: i32[] = [p];
    let i: i32 = wasm_poll(ps);
    write("idx="); print_int(i); write("\n");
    return 0;
}`)
		if got := strings.TrimSpace(out); got != "idx=0" {
			t.Errorf("stdout = %q, want %q", got, "idx=0")
		}
		if elapsed < 300*time.Millisecond {
			t.Errorf("poll returned after %v — it did not block on the 400ms timer", elapsed)
		}
	})

	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "fast_first_returns_0",
			src: `function main(): i32 {
    let fast: i64 = 10000000i64;
    let slow: i64 = 3000000000i64;
    let a: i32 = wasm_timer_pollable(fast);
    let b: i32 = wasm_timer_pollable(slow);
    let ps: i32[] = [a, b];
    let i: i32 = wasm_poll(ps);
    write("idx="); print_int(i); write("\n");
    return 0;
}`,
			want: "idx=0",
		},
		{
			name: "fast_second_returns_1",
			src: `function main(): i32 {
    let slow: i64 = 3000000000i64;
    let fast: i64 = 10000000i64;
    let a: i32 = wasm_timer_pollable(slow);
    let b: i32 = wasm_timer_pollable(fast);
    let ps: i32[] = [a, b];
    let i: i32 = wasm_poll(ps);
    write("idx="); print_int(i); write("\n");
    return 0;
}`,
			want: "idx=1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, elapsed := build(t, tc.name, tc.src)
			if got := strings.TrimSpace(out); got != tc.want {
				t.Errorf("stdout = %q, want %q", got, tc.want)
			}
			if elapsed > 2*time.Second {
				t.Errorf("poll returned after %v — it waited past the first ready pollable", elapsed)
			}
		})
	}
}
