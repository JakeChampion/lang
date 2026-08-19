package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSelfHostWasmIRPollFds pins the POSIX-shaped `poll(fds, timeout_ms)` on
// the self-host wasm-IR path — the follow-up #4316 did not actually cover.
//
// #4316 closed on `wasm_poll`, the wasm-native primitive that blocks on a set
// of pollables with no timeout. `poll` is the op its issue text named, and it
// is a different contract (checker.go): "Waits up to `timeout_ms` (negative =
// block indefinitely, 0 = non-blocking) for any fd in `fds` to become
// readable; returns the index of the first ready fd, or -1 on timeout."
//
// On wasm the elements are wasi:io/poll pollable handles — wasm has no file
// descriptors, so a pollable IS the fd analog. The timeout is expressed the
// only way wasi:io/poll can express one: subscribe a monotonic-clock timer for
// `timeout_ms` and append it to the polled set. If the winning index is that
// trailing entry the wait timed out and the result is -1; otherwise the winner
// is a caller pollable whose index is already correct, since the timer was
// appended after them.
//
// All three arms of the contract are asserted, and the ELAPSED TIME is checked
// alongside the value: a helper that returned the right number without actually
// waiting (the failure mode that bit #4316 during development) would pass a
// value-only test.
func TestSelfHostWasmIRPollFds(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR poll-fds e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR poll-fds e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR poll-fds e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	run := func(t *testing.T, name, src string) (string, time.Duration) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		if !strings.Contains(string(wat), "call $__fern_poll") {
			t.Fatalf("%s: emitted WAT has no `call $__fern_poll` (module bailed off the IR path?)", name)
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
		return strings.TrimSpace(string(out)), elapsed
	}

	// A 50ms pollable inside a 5s budget: the pollable wins, so the answer is
	// its index, and the wait is short.
	t.Run("ready_before_timeout_returns_index", func(t *testing.T) {
		out, elapsed := run(t, "poll_ready", `function main(): i32 {
    var d: i64 = 50000000i64;
    var a: i32 = wasm_timer_pollable(d);
    var ps: i32[] = [a];
    write("ready="); print_int(poll(ps, 5000)); write("\n");
    return 0;
}`)
		if out != "ready=0" {
			t.Errorf("stdout = %q, want %q", out, "ready=0")
		}
		if elapsed > 3*time.Second {
			t.Errorf("poll took %v — it waited out the 5s budget instead of returning on the ready pollable", elapsed)
		}
	})

	// A 3s pollable against a 200ms budget: the appended timer wins, so the
	// answer is -1 and the wait is bounded by the budget, not the pollable.
	t.Run("timeout_returns_negative_one", func(t *testing.T) {
		out, elapsed := run(t, "poll_timeout", `function main(): i32 {
    var d: i64 = 3000000000i64;
    var a: i32 = wasm_timer_pollable(d);
    var ps: i32[] = [a];
    write("timedout="); print_int(poll(ps, 200)); write("\n");
    return 0;
}`)
		if out != "timedout=-1" {
			t.Errorf("stdout = %q, want %q", out, "timedout=-1")
		}
		if elapsed > 2*time.Second {
			t.Errorf("poll took %v — it waited for the 3s pollable instead of timing out at 200ms", elapsed)
		}
	})

	// Negative timeout = block indefinitely, which delegates to
	// $__fern_wasm_poll. The 400ms pollable must actually be waited for.
	t.Run("negative_timeout_blocks_indefinitely", func(t *testing.T) {
		out, elapsed := run(t, "poll_block", `function main(): i32 {
    var d: i64 = 400000000i64;
    var a: i32 = wasm_timer_pollable(d);
    var ps: i32[] = [a];
    write("blocking-ready="); print_int(poll(ps, 0 - 1)); write("\n");
    return 0;
}`)
		if out != "blocking-ready=0" {
			t.Errorf("stdout = %q, want %q", out, "blocking-ready=0")
		}
		if elapsed < 300*time.Millisecond {
			t.Errorf("poll returned after %v — it did not block on the 400ms pollable", elapsed)
		}
	})
}
