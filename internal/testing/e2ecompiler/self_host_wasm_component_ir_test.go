package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmComponentIRPath(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	copySelfHostDriver(t, dir, "wasm_ir.fern", "semlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_p2.fern"), []byte(p2Driver), 0o644); err != nil {
		t.Fatalf("write wasm_run_p2.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_run_io.fern"), []byte(p2IODriver), 0o644); err != nil {
		t.Fatalf("write wasm_run_io.fern: %v", err)
	}
	runBin := buildSelfHostBin(t, gcc, dir, "wasm_run_p2.fern", "wasm_run_p2")
	ioBin := buildSelfHostBin(t, gcc, dir, "wasm_run_io.fern", "wasm_run_io")
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
		io      bool
		source  string
		imports []string
	}{
		{"noio-const", false, `function main(): i32 { return 42; }`, nil},
		{"noio-arith", false, `function main(): i32 { let x: i32 = 5; let y: i32 = 5; return x - y; }`, nil},
		{"noio-array", false, `function main(): i32 { let xs: i32[] = [1, 2, 3]; return xs[0] + xs[2]; }`, nil},
		{"noio-string", false, `function main(): i32 { let s: string = "ab" + "cd"; return s.len(); }`, nil},
		{"noio-wide-local", false, `function main(): i32 { let n: i64 = 7; if (n > 0) { return 0; } return 1; }`, nil},
		{"noio-exit-refused", false, `function main(): i32 { exit(0); return 0; }`, nil},

		{"io-write", true, `function main(): i32 { write("hi"); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		{"io-putchar", true, `function main(): i32 { putchar(72); putchar(105); return 0; }`,
			[]string{"wasi:cli/stdout@0.2.0 get-stdout", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush"}},
		{"io-fstring", true, "import \"std/i32\";\nfunction main(): i32 { let n: i32 = 21; write(f\"answer={n * 2}\"); return 0; }",
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		{"io-eprint", true, `function main(): i32 { eprint("boom"); write("out"); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		{"io-eprint-only", true, `function main(): i32 { eprint("just-err"); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout"}},
		{"io-exit", true, `function main(): i32 { write("bye"); exit(0); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:cli/exit@0.2.0 exit"}},

		{"io-random-i32", true, `function main(): i32 { if (random_i32() != 0) { write("r"); } return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:random/random@0.2.0 get-random-u64"}},
		{"io-random-bytes", true, `function main(): i32 { let b: u8[] = random_bytes(4); if (b.len() == 4) { write("b"); } return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:random/random@0.2.0 get-random-u64"}},
		{"io-clock-wall", true, `function main(): i32 { if (now_unix_ms() > 0) { write("w"); } return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:clocks/wall-clock@0.2.0 now"}},
		{"io-clock-mono", true, `function main(): i32 { if (monotonic_ns() > 0) { write("m"); } return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:clocks/monotonic-clock@0.2.0 now"}},
		{"noio-random-refused", false, `function main(): i32 { return random_i32() & 1; }`, nil},

		{"io-env", true, `function main(): i32 { match (env("HOME")) { Some(v) => { write(v); }, None => { write("none"); } } return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:cli/environment@0.2.0 get-environment"}},
		{"io-args", true, `function main(): i32 { let a: string[] = args(); write(a[0]); return 0; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:cli/environment@0.2.0 get-arguments"}},

		{"io-read-file", true, `function main(): i32 { match (read_file("in.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 2; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:filesystem/preopens@0.2.0 get-directories", "wasi:filesystem/types@0.2.0 [method]descriptor.open-at", "wasi:filesystem/types@0.2.0 [resource-drop]descriptor", "wasi:filesystem/types@0.2.0 [method]descriptor.read-via-stream", "wasi:io/streams@0.2.0 [method]input-stream.blocking-read", "wasi:io/streams@0.2.0 [resource-drop]input-stream"}},
		{"io-write-file", true, `function main(): i32 { match (write_file("o.txt", "x")) { Err(e) => { return 1; }, Ok(_) => { return 0; } } return 2; }`,
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:filesystem/preopens@0.2.0 get-directories", "wasi:filesystem/types@0.2.0 [method]descriptor.open-at", "wasi:filesystem/types@0.2.0 [resource-drop]descriptor", "wasi:filesystem/types@0.2.0 [method]descriptor.write-via-stream", "wasi:io/streams@0.2.0 [resource-drop]output-stream"}},

		{"clock-tostring", true, "import \"std/i64\";\nfunction main(): i32 { write(now_unix_ms().to_string()); return 0; }",
			[]string{"wasi:cli/stderr@0.2.0 get-stderr", "wasi:io/streams@0.2.0 [method]output-stream.blocking-write-and-flush", "wasi:cli/stdout@0.2.0 get-stdout", "wasi:clocks/wall-clock@0.2.0 now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := runBin
			if tc.io {
				bin = ioBin
			}
			if refusedRows[tc.name] {
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
				wantImports = append([]string{"wasi:io/error@0.2.0 [resource-drop]error"}, wantImports...)
			}
			if got := watImports(wat); !equalStrs(got, wantImports) {
				t.Errorf("imports =\n  %v\nwant\n  %v", got, wantImports)
			}
		})
	}
}

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

var refusedRows = map[string]bool{
	"noio-exit-refused":   true,
	"noio-random-refused": true,
}

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
