package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSelfHostWasmComponentIRPath pins the ROUTING of the Component-Model emit
// modes, which no other component test can see: every wasm-component core used
// to come from the AST emitter unconditionally (emit_module_mode gated the IR
// leg on `!component`), so the self-hosted component path was a hard blocker on
// retiring wasm.fern, which has since happened (#3457). The sibling tests here (…ComponentStdout / …Eprint /
// …Exit) run the resulting component and would stay green if the routing
// silently reverted to AST, so they cannot guard it — this one does.
//
// Two things are asserted per program:
//
//   - which emitter produced the core. The IR emitter writes flat WAT with an
//     UNNAMED local group ("(local i64 i32 …") and the AST emitter writes
//     folded WAT with NAMED locals ($__retv_i32), so the two are
//     distinguishable by inspection of the emitted core. The discriminator
//     keys on the group being unnamed rather than on its first type: a
//     function whose first local is wide emits "(local i64 …", which a
//     "(local i32" probe misses entirely.
//   - the core's IMPORT LIST, exactly and in order. The component framings
//     (watbin.component_full / component_full_io / _eprint / _exit) alias
//     imports positionally, so a core that imports a different set — or the
//     same set in a different order — composes into a component that fails to
//     validate. That contract is invisible in the emitted-and-run tests until
//     the whole compose pipeline is assembled, and it is the constraint that
//     decides which shapes the IR leg may serve at all.
//
// The out-of-subset rows are as load-bearing as the in-subset ones: each pins a
// shape that must KEEP falling through, because admitting it would emit a core
// calling helpers nothing defines. Every WASI category component_shape knows
// has now migrated — stdout/stderr/exit, clock, random, env, args, and finally
// the filesystem pair — so what remains out of subset is random without I/O,
// which has no import any component framing can wire.
func TestSelfHostWasmComponentIRPath(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern")
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
		// A WIDE first local. This lowers like any other, but it emits
		// "(local i64 i32 …" rather than "(local i32 …", which is exactly the
		// shape a first-type-specific discriminator misses — kept as a row so
		// the probe itself stays honest.
		{"noio-wide-local", false, `function main(): i32 { var n: i64 = 7; if (n > 0) { return 0; } return 1; }`, true, nil},
		// A no-I/O core may not exit: mode 1 has no proc_exit to call and no
		// wasi:cli/exit to shim it over. This used to fall back to the AST
		// emitter, where exit was equally unwired — a core with a dangling
		// call. With that emitter gone (#3457) the gate's decline is a hard
		// error, which is the right answer for a program mode 1 cannot express
		// (see refusedRows). Unreachable through the CLI either way:
		// component_shape sends an exit-using program to the io wrap (shape 14).
		{"noio-exit-refused", false, `function main(): i32 { exit(0); return 0; }`, false, nil},

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
		// gate refuses random there. It used to bail to the AST emitter,
		// which emitted a PREVIEW1 random_get no component framing can wire —
		// its `if (io)` split treated "not io" as "preview1 command core". The
		// old row recorded that output while saying in as many words that it was
		// "deliberately NOT a contract worth preserving"; deleting the emitter
		// (#3457) turns it into a refusal, which is what it should always have
		// been. Unreachable through the CLI (component_shape sends every random
		// program to the io wrap).
		{"noio-random-refused", false, `function main(): i32 { return random_i32() & 1; }`, false, nil},

		// env / args read a preview2 LIST, so their cores also export
		// cabi_realloc — the guest allocator the canonical ABI materialises the
		// list into, without which `component new` rejects the module.
		{"io-env", true, `function main(): i32 { match (env("HOME")) { Some(v) => { write(v); }, None => { write("none"); } } return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-environment"}},
		{"io-args", true, `function main(): i32 { var a: string[] = args(); write(a[0]); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-arguments"}},
		// arg_at shares the get-arguments import with args() — a program using
		// only arg_at still pulls it in, and one using both imports it once.
		{"io-arg-at", true, `function main(): i32 { write(arg_at(1)); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-arguments"}},
		{"io-args-and-arg-at", true, `function main(): i32 { var a: string[] = args(); write(a[0]); write(arg_at(1)); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-arguments"}},

		// The filesystem pair, last of component_shape's categories to move.
		// Their mode-2 bodies box a real IoError variant rather than the raw
		// wasi error code the AST fallback stored (#5795), and fs sits
		// last in the import order, which is the slot component_full_io_fs /
		// _fs_write / _fs_rw alias.
		{"io-read-file", true, `function main(): i32 { match (read_file("in.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 2; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:filesystem/preopens@0.2.0 get-directories", "wasi:filesystem/types@0.2.0 [method]descriptor.open-at", "wasi:filesystem/types@0.2.0 [method]descriptor.read-via-stream", "wasi:io/streams@0.2.0 [method]input-stream.blocking-read"}},
		{"io-write-file", true, `function main(): i32 { match (write_file("o.txt", "x")) { Err(e) => { return 1; }, Ok(_) => { return 0; } } return 2; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:filesystem/preopens@0.2.0 get-directories", "wasi:filesystem/types@0.2.0 [method]descriptor.open-at", "wasi:filesystem/types@0.2.0 [method]descriptor.write-via-stream"}},

		// now_unix_ms() is an i64, so this composes the clock import with the
		// wide `.to_string()` formatter ($__fern_i64_to_str, #5826) — the last
		// per-function IR gap a component core hit.
		{"clock-tostring", true, `function main(): i32 { write(now_unix_ms().to_string()); return 0; }`, true,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/wall-clock@0.2.0 now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := runBin
			if tc.io {
				bin = ioBin
			}
			if refusedRows[tc.name] {
				// The gate declines this shape and there is no AST emitter to
				// fall through to, so the driver must refuse rather than emit a
				// core whose imports the framing cannot satisfy.
				out, code := emitRefusable(t, runner, bin, tc.source)
				if code == 0 || len(out) != 0 {
					t.Fatalf("driver exited %d with %d bytes, want a refusal", code, len(out))
				}
				return
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
// irLocalGroup matches the IR emitter's unnamed local group, whatever its
// first type is.
var irLocalGroup = regexp.MustCompile(`\n    \(local (i32|i64|f32|f64)[ )]`)

func isIREmittedWAT(t *testing.T, wat string) bool {
	t.Helper()
	// An unnamed local group — any leading type. Keying on "(local i32"
	// specifically would miss a function whose first local is wide (i64/f64
	// user locals sort ahead of the i32 scratch slots), and since the AST
	// marker is absent too that shows up as the "cannot tell" fatal below
	// rather than as a wrong answer — but it is still a false alarm.
	ir := irLocalGroup.MatchString(wat)
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

// refusedRows names the table rows whose shape the component gate DECLINES. They
// are kept as rows rather than deleted because the decline is the contract: each
// is a program mode 1 cannot express, and before #3457 each fell through to the
// AST emitter and produced a core the framing could not wire.
var refusedRows = map[string]bool{
	"noio-exit-refused":   true,
	"noio-random-refused": true,
}

// emitRefusable runs a component driver expecting it to REFUSE, returning stdout
// and the exit code instead of fataling on a non-zero exit the way `emit` does.
func emitRefusable(t *testing.T, runner []string, bin, src string) ([]byte, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	out, _ := cmd.Output()
	return out, cmd.ProcessState.ExitCode()
}
