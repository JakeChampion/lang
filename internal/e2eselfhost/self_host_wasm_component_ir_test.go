package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmComponentIRPath pins the ROUTING of the Component-Model emit
// modes, which no other component test can see: every wasm-component core used
// to come from the AST emitter unconditionally (emit_module_mode gated the IR
// leg on `!component`), so the self-hosted component path was a hard blocker on
// retiring wasm.fern. The sibling tests here (…ComponentStdout / …Eprint /
// …Exit) run the resulting component and would stay green if the routing
// silently reverted to AST, so they cannot guard it — this one does.
//
// Two things are asserted per program:
//
//   - which emitter produced the core. The IR emitter writes flat WAT with an
//     unnamed local group ("(local i32 …") and the AST emitter writes folded
//     WAT with named locals ($__retv_i32), so the two are distinguishable by
//     inspection of the emitted core.
//   - the core's IMPORT LIST, exactly and in order. The component framings
//     (watbin.component_full / component_full_io / _eprint / _exit) alias
//     imports positionally, so a core that imports a different set — or the
//     same set in a different order — composes into a component that fails to
//     validate. That contract is invisible in the emitted-and-run tests until
//     the whole compose pipeline is assembled, and it is the constraint that
//     decides which shapes the IR leg may serve at all.
//
// The out-of-subset half of the table is as load-bearing as the in-subset
// half: env / args / random / clock / read_file component shapes have preview2
// helper bodies only on the AST path (wasm.env_func_p2 and friends), so they
// MUST keep falling through. A gate that widened to admit them would emit a
// core calling helpers nothing defines.
func TestSelfHostWasmComponentIRPath(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// The no-I/O run core (emit_module_run) and the stdout run core
	// (emit_module_run_io) — the two component modes with an IR leg.
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_p2.fern"), []byte(p2Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run_p2.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	runBin := buildSelfHostBin(t, gcc, dir, "wasm_run_p2.fern", "wasm_run_p2")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")

	emit := func(t *testing.T, bin, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("emit failed for %q: %v", src, err)
		}
		return string(out)
	}

	for _, tc := range []struct {
		name    string
		io      bool // emit_module_run_io (mode 2) rather than emit_module_run (mode 1)
		source  string
		wantIR  bool
		imports []string
	}{
		// Mode 1 — no I/O at all. The framing (component_full) supplies no
		// imports, so an IR core here must be import-free.
		{"noio-const", false, `function main(): i32 { return 42; }`, true, nil},
		{"noio-arith", false, `function main(): i32 { var x: i32 = 5; var y: i32 = 5; return x - y; }`, true, nil},
		{"noio-array", false, `function main(): i32 { var xs: i32[] = [1, 2, 3]; return xs[0] + xs[2]; }`, true, nil},
		{"noio-string", false, `function main(): i32 { var s: string = "ab" + "cd"; return s.len(); }`, true, nil},
		// A no-I/O core may not exit either: mode 1 has no proc_exit to call
		// and no wasi:cli/exit to shim it over, so exit stays on the AST path
		// (where it is equally unwired — but that is the pre-existing shape,
		// not something the IR leg should newly emit a dangling call for).
		{"noio-exit-falls-back", false, `function main(): i32 { exit(0); return 0; }`, false, nil},

		// Mode 2 — stdout. The $fd_write shim serves every writer, so print /
		// write / print_int / putchar all ride the same two imports.
		{"io-write", true, `function main(): i32 { write("hi"); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		{"io-print-int", true, `function main(): i32 { print_int(42); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		{"io-putchar", true, `function main(): i32 { putchar(72); putchar(105); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		{"io-fstring", true, `function main(): i32 { var n: i32 = 21; write(f"answer={n * 2}"); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		// eprint reorders the trio (get-stderr first) to match
		// component_full_io_eprint, and keeps stdout imported even when the
		// program never writes to it.
		{"io-eprint", true, `function main(): i32 { eprint("boom"); write("out"); return 0; }`, true,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		{"io-eprint-only", true, `function main(): i32 { eprint("just-err"); return 0; }`, true,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		// exit adds wasi:cli/exit last (component_full_io_exit's shape); the IR
		// emits `call $proc_exit`, which the mode-2 shim defines over it.
		{"io-exit", true, `function main(): i32 { write("bye"); exit(0); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/exit@0.2.0 exit"}},

		// The preview2-backed builtins. Their helper bodies (*_p2) define the
		// same $__fern_* functions the IR already calls, so mode 2 swaps the
		// import + body and every call site is unchanged. Import order follows
		// the AST path's canonical interface order — random, wall-clock,
		// monotonic-clock — after the stdout pair, because the framings alias
		// positionally.
		{"io-random-i32", true, `function main(): i32 { if (random_i32() != 0) { write("r"); } return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:random/random@0.2.0 get-random-u64"}},
		{"io-random-bytes", true, `function main(): i32 { var b: i32[] = random_bytes(4); if (b.len() == 4) { write("b"); } return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:random/random@0.2.0 get-random-u64"}},
		{"io-clock-wall", true, `function main(): i32 { if (now_unix_ms() > 0) { write("w"); } return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/wall-clock@0.2.0 now"}},
		{"io-clock-mono", true, `function main(): i32 { if (monotonic_ns() > 0) { write("m"); } return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/monotonic-clock@0.2.0 now"}},
		// A no-I/O component has no import to satisfy the byte source, so the
		// gate refuses random there and it stays on the AST path. Note what
		// the AST fallback then emits: a PREVIEW1 random_get, which no
		// component framing can wire — its `if (io)` split treats "not io" as
		// "preview1 command core". That combination is unreachable through the
		// CLI (component_shape sends every random program to the io wrap, so
		// emit_module_run never sees one), which is why it has gone unnoticed;
		// the import list below records what happens today and is deliberately
		// NOT a contract worth preserving.
		{"noio-random-falls-back", false, `function main(): i32 { return random_i32() & 1; }`, false,
			[]string{"wasi_snapshot_preview1 random_get"}},

		// Out of subset — these must keep falling through to the AST emitter,
		// whose preview2 helper bodies are the only implementation they have.
		{"env-falls-back", true, `function main(): i32 { match (env("HOME")) { Some(v) => { write(v); }, None => { write("none"); } } return 0; }`, false,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-environment"}},
		{"args-falls-back", true, `function main(): i32 { var a: string[] = args(); write(a[0]); return 0; }`, false,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-arguments"}},
		// i64.to_string() is outside the per-function IR subset (equally so on
		// the preview1 command core), so this one falls back for a reason that
		// has nothing to do with component mode — worth pinning, since it is
		// the shape most likely to be misread as a component-gate rejection.
		{"clock-tostring-falls-back", true, `function main(): i32 { write(now_unix_ms().to_string()); return 0; }`, false,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/wall-clock@0.2.0 now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := runBin
			if tc.io {
				bin = ioBin
			}
			wat := emit(t, bin, tc.source)

			// Every component core exports main + _lang_run and none exports
			// _start, whichever emitter produced it.
			for _, want := range []string{`(export "main" (func $main))`, `(export "_lang_run" (func $_lang_run))`} {
				if !strings.Contains(wat, want) {
					t.Errorf("component core is missing %s", want)
				}
			}
			if strings.Contains(wat, `(export "_start"`) {
				t.Error("component core exports _start — that is the preview1 command entry")
			}

			gotIR := isIREmittedWAT(t, wat)
			if gotIR != tc.wantIR {
				t.Errorf("emitted via IR = %v, want %v", gotIR, tc.wantIR)
			}
			if got := watImports(wat); !equalStrs(got, tc.imports) {
				t.Errorf("imports =\n  %v\nwant\n  %v", got, tc.imports)
			}
		})
	}
}

// isIREmittedWAT reports which emitter produced a core: the IR path writes flat
// WAT with an unnamed local group, the AST path folded WAT with named locals
// ($__retv_i32, its per-function return slot). The two markers are mutually
// exclusive by construction, so disagreement means the discriminator itself has
// gone stale rather than the routing — worth failing loudly on.
func isIREmittedWAT(t *testing.T, wat string) bool {
	t.Helper()
	ir := strings.Contains(wat, "\n    (local i32")
	ast := strings.Contains(wat, "(local $__retv_i32 i32)")
	if ir == ast {
		t.Fatalf("cannot tell which emitter produced the core (flat-locals=%v, named-locals=%v)", ir, ast)
	}
	return ir
}

// watImports lists a core's imports as "module name", in emitted order — the
// order the component framings alias them by.
func watImports(wat string) []string {
	var out []string
	for _, line := range strings.Split(wat, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `(import "`) {
			continue
		}
		parts := strings.SplitN(line, `"`, 5)
		if len(parts) < 5 {
			continue
		}
		out = append(out, parts[1]+" "+parts[3])
	}
	return out
}

func equalStrs(a, b []string) bool {
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
