package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmComponentIRPath pins the ROUTING of the Component-Model emit
// modes: every wasm-component core comes from the IR emitter (#3457). The
// sibling tests here (…ComponentStdout / …Eprint / …Exit) run the resulting
// component and cannot see which emitter produced it — this one does.
//
// Two things are asserted per program:
//
//   - which emitter produced the core. The IR emitter writes flat WAT with an
//     UNNAMED local group ("(local i64 i32 …"). The discriminator keys on the
//     group being unnamed rather than on its first type: a function whose
//     first local is wide emits "(local i64 …", which a "(local i32" probe
//     misses entirely.
//   - the core's IMPORT LIST, exactly and in order. The component framings
//     (watbin.component_full / component_full_io / _eprint / _exit) alias
//     imports positionally, so a core that imports a different set — or the
//     same set in a different order — composes into a component that fails to
//     validate. That contract is invisible in the emitted-and-run tests until
//     the whole compose pipeline is assembled, and it is the constraint that
//     decides which shapes the IR leg may serve at all.
//
// The out-of-subset rows are as essential as the in-subset ones: each pins a
// shape the gate must DECLINE, because admitting it would emit a core calling
// helpers nothing defines. Every WASI category component_shape knows lowers —
// stdout/stderr/exit, clock, random, env, args, and the filesystem pair — so
// what remains out of subset is exit or random without I/O, which has no
// import any component framing can wire.
func TestSelfHostWasmComponentIRPath(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	// The no-I/O run core and the stdout run core
	// (wasm_ir.emit_module_mode_or_error_sub with io false / true).
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_p2.fern"), []byte(p2Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run_p2.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	runBin := buildSelfHostBin(t, gcc, dir, "wasm_run_p2.fern", "wasm_run_p2")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")
	// A row that imports the stdlib compiles through asm_load_run, as the CLI
	// would frame it; the inline drivers load no imports.
	std := newWasmStdlibLoader(t)

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
		io      bool // the stdout run core (mode 2) rather than the no-I/O one (mode 1)
		source  string
		imports []string
	}{
		// Mode 1 — no I/O at all. The framing (component_full) supplies no
		// imports, so an IR core here must be import-free.
		{"noio-const", false, `function main(): i32 { return 42; }`, nil},
		{"noio-arith", false, `function main(): i32 { let x: i32 = 5; let y: i32 = 5; return x - y; }`, nil},
		{"noio-array", false, `function main(): i32 { let xs: i32[] = [1, 2, 3]; return xs[0] + xs[2]; }`, nil},
		{"noio-string", false, `function main(): i32 { let s: string = "ab" + "cd"; return s.len(); }`, nil},
		// A WIDE first local, emitted as "(local i64 i32 …".
		{"noio-wide-local", false, `function main(): i32 { let n: i64 = 7; if (n > 0) { return 0; } return 1; }`, nil},
		// A no-I/O core may not exit: mode 1 has no proc_exit to call and no
		// wasi:cli/exit to shim it over, so the gate declines it and the driver
		// refuses (see refusedRows). Unreachable through the CLI: component_shape
		// sends an exit-using program to the io wrap (shape 14).
		{"noio-exit-refused", false, `function main(): i32 { exit(0); return 0; }`, nil},

		// Mode 2 — stdout. The $__fern_fd_write shim serves every writer, so print /
		// write / putchar all use the same two imports.
		{"io-write", true, `function main(): i32 { write("hi"); return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		{"io-putchar", true, `function main(): i32 { putchar(72); putchar(105); return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		{"io-fstring", true, "import \"std/i32\";\nfunction main(): i32 { let n: i32 = 21; write(f\"answer={n * 2}\"); return 0; }",
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		// eprint reorders the trio (get-stderr first) to match
		// component_full_io_eprint, and keeps stdout imported even when the
		// program never writes to it.
		{"io-eprint", true, `function main(): i32 { eprint("boom"); write("out"); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		{"io-eprint-only", true, `function main(): i32 { eprint("just-err"); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		// exit adds wasi:cli/exit last (component_full_io_exit's shape); the IR
		// emits `call $__fern_proc_exit`, which the mode-2 shim defines over it.
		{"io-exit", true, `function main(): i32 { write("bye"); exit(0); return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/exit@0.2.0 exit"}},

		// The preview2-backed builtins. Their helper bodies (*_p2) define the
		// same $__fern_* functions the IR already calls, so mode 2 swaps the
		// import + body and every call site is unchanged. Import order is the
		// canonical interface order — random, wall-clock, monotonic-clock — after
		// the stdout pair, because the framings alias positionally.
		{"io-random-i32", true, `function main(): i32 { if (random_i32() != 0) { write("r"); } return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:random/random@0.2.0 get-random-u64"}},
		{"io-random-bytes", true, `function main(): i32 { let b: u8[] = random_bytes(4); if (b.len() == 4) { write("b"); } return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:random/random@0.2.0 get-random-u64"}},
		{"io-clock-wall", true, `function main(): i32 { if (now_unix_ms() > 0) { write("w"); } return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/wall-clock@0.2.0 now"}},
		{"io-clock-mono", true, `function main(): i32 { if (monotonic_ns() > 0) { write("m"); } return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/monotonic-clock@0.2.0 now"}},
		// A no-I/O component has no import to satisfy the byte source, so the
		// gate refuses random there. Unreachable through the CLI (component_shape
		// sends every random program to the io wrap).
		{"noio-random-refused", false, `function main(): i32 { return random_i32() & 1; }`, nil},

		// env / args read a preview2 LIST, so their cores also export
		// cabi_realloc — the guest allocator the canonical ABI materialises the
		// list into, without which `component new` rejects the module.
		{"io-env", true, `function main(): i32 { match (env("HOME")) { Some(v) => { write(v); }, None => { write("none"); } } return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-environment"}},
		{"io-args", true, `function main(): i32 { let a: string[] = args(); write(a[0]); return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/environment@0.2.0 get-arguments"}},

		// The filesystem pair, last of component_shape's categories to move.
		// Their mode-2 bodies box a real IoError variant rather than a raw wasi
		// error code (#5795), and fs sits last in the import order, which is the
		// slot component_full_io_fs / _fs_write / _fs_rw alias.
		{"io-read-file", true, `function main(): i32 { match (read_file("in.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 2; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:filesystem/preopens@0.2.0 get-directories", "wasi:filesystem/types@0.2.0 [method]descriptor.open-at", "wasi:filesystem/types@0.2.0 [resource-drop]descriptor", "wasi:filesystem/types@0.2.0 [method]descriptor.read-via-stream", "wasi:io/streams@0.2.0 [method]input-stream.blocking-read", "wasi:io/streams@0.2.0 [resource-drop]input-stream"}},
		{"io-write-file", true, `function main(): i32 { match (write_file("o.txt", "x")) { Err(e) => { return 1; }, Ok(_) => { return 0; } } return 2; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:filesystem/preopens@0.2.0 get-directories", "wasi:filesystem/types@0.2.0 [method]descriptor.open-at", "wasi:filesystem/types@0.2.0 [resource-drop]descriptor", "wasi:filesystem/types@0.2.0 [method]descriptor.write-via-stream", "wasi:io/streams@0.2.0 [resource-drop]output-stream"}},

		// now_unix_ms() is an i64, so this composes the clock import with the
		// wide `.to_string()` formatter ($__fern_i64_to_str, #5826) — the last
		// per-function IR gap a component core hit.
		{"clock-tostring", true, "import \"std/i64\";\nfunction main(): i32 { write(now_unix_ms().to_string()); return 0; }",
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:clocks/wall-clock@0.2.0 now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := runBin
			if tc.io {
				bin = ioBin
			}
			if refusedRows[tc.name] {
				// The gate declines this shape, so the driver must refuse rather than
				// emit a core whose imports the framing cannot satisfy.
				out, code := emitRefusable(t, runner, bin, tc.source)
				if code == 0 || len(out) != 0 {
					t.Fatalf("driver exited %d with %d bytes, want a refusal", code, len(out))
				}
				return
			}
			var wat string
			if strings.Contains(tc.source, "import \"") {
				wat = string(std.emit(t, tc.source, "-emit", "component-core"))
			} else {
				wat = emit(t, bin, tc.source)
			}

			// Every component core exports main + _lang_run and none exports
			// _start, whichever emitter produced it.
			for _, want := range []string{`(export "main" (func $main))`, `(export "_lang_run" (func $__fern_lang_run))`} {
				if !strings.Contains(wat, want) {
					t.Errorf("component core is missing %s", want)
				}
			}
			if strings.Contains(wat, `(export "_start"`) {
				t.Error("component core exports _start — that is the preview1 command entry")
			}

			wantImports := tc.imports
			if tc.io {
				// The write shim owns last-operation-failed handles and
				// must release them before returning its I/O error.
				wantImports = append([]string{"wasi:io/error@0.2.0 [resource-drop]error"}, wantImports...)
			}
			if got := watImports(wat); !equalStrs(got, wantImports) {
				t.Errorf("imports =\n  %v\nwant\n  %v", got, wantImports)
			}
		})
	}
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
// is a program mode 1 cannot express, and the driver must refuse it rather than
// produce a core the framing could not wire.
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
