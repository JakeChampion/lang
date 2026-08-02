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

// TestSelfHostWasmIRPoll pins wasm_poll on the self-host wasm-IR path (#4316):
// the wasi:io/poll.poll readiness multiplexer, which blocks until at least one
// pollable in an i32[] is ready and yields the FIRST ready index (-1 on an
// empty ready list).
//
// It is the first LIST-typed component import the self-host emits, and that is
// what made it more than an import line. Three things it needs that the scalar
// pollable ops (subscribe-duration / block / resource-drop) do not:
//
//  1. Component-type metadata. A list-typed import cannot be inferred from the
//     core signature, so `component new` alone fails with "missing component
//     metadata for import of wasi:io/poll@0.2.0::poll". The pipeline below runs
//     `wasm-tools component embed -w fern` first, and cmd/fern/wit/deps/io/
//     poll.wit had to declare the function (it was a subset carrying only
//     pollable.block).
//  2. An exported cabi_realloc — the returned list<u32> is materialised in
//     GUEST memory. Without it: "module does not export a function named
//     cabi_realloc".
//  3. The self-host array layout, NOT native's. A self-host array pointer has
//     its count at p+0 and elements at p+8; native's buildWasmPollBody reads
//     count at p-4 / elements at p+0. Copying native verbatim made poll read a
//     garbage length and return immediately without blocking.
//
// The assertions are behavioural on purpose — that poll BLOCKS, and picks the
// right index — because every failure mode above produced a module that still
// composed and still exited 0 while doing nothing.
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
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	// build emits the WAT, embeds the `fern` world's component-type metadata,
	// composes, validates, and runs — returning stdout and the wall time (the
	// blocking assertion needs the latter).
	build := func(t *testing.T, name, src string) (string, time.Duration) {
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
		// The module must reach the IR path and call the helper — a bail to the
		// AST backend would silently pass the behavioural checks below only if
		// that backend also implemented poll, which it does not.
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

	// A 400ms timer, polled alone. poll must BLOCK for it — the bug that
	// motivated the array-layout comment above returned instantly instead — and
	// report index 0 as ready.
	t.Run("blocks_until_ready", func(t *testing.T) {
		out, elapsed := build(t, "poll_block", `function main(): i32 {
    var d: i64 = 400000000i64;
    var p: i32 = wasm_timer_pollable(d);
    var ps: i32[] = [p];
    var i: i32 = wasm_poll(ps);
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

	// Two timers. poll must return the index of whichever fires FIRST, and must
	// not wait for the slow one — so the position of the fast timer decides the
	// answer, and a helper that ignored the ready list would fail one of these.
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "fast_first_returns_0",
			src: `function main(): i32 {
    var fast: i64 = 10000000i64;
    var slow: i64 = 3000000000i64;
    var a: i32 = wasm_timer_pollable(fast);
    var b: i32 = wasm_timer_pollable(slow);
    var ps: i32[] = [a, b];
    var i: i32 = wasm_poll(ps);
    write("idx="); print_int(i); write("\n");
    return 0;
}`,
			want: "idx=0",
		},
		{
			name: "fast_second_returns_1",
			src: `function main(): i32 {
    var slow: i64 = 3000000000i64;
    var fast: i64 = 10000000i64;
    var a: i32 = wasm_timer_pollable(slow);
    var b: i32 = wasm_timer_pollable(fast);
    var ps: i32[] = [a, b];
    var i: i32 = wasm_poll(ps);
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
			// The slow timer is 3s; returning anywhere near it would mean poll
			// waited for ALL pollables rather than the first ready one.
			if elapsed > 2*time.Second {
				t.Errorf("poll returned after %v — it waited past the first ready pollable", elapsed)
			}
		})
	}
}
