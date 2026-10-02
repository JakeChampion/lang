package e2eselfhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Replace only the WASI boundary of a compiled program. The real lowering,
// byte-array layout, write loop, result boxing and caller ownership still run.
func TestSelfHostFileBytesFaults(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	cli, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "fault.fern")
	program := `function main(): i32 {
  var bytes: u8[] = [0 as u8, 255 as u8, 65 as u8, 128 as u8];
  var result = write_file_bytes("ignored", bytes);
  if (bytes.len() != 4 || bytes[1] != 255 || bytes[3] != 128) { return 20; }
  match (result) {
    Ok(_) => { return 0; },
    Err(e) => { match (e) {
      Other(p, _) => { if (p != "ignored") { return 21; } },
      PermissionDenied(p) => { if (p != "ignored") { return 22; } },
      _ => { return 23; }
    } return 1; }
  }
  return 24;
}`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, compiler := range []struct{ name, cli, emit string }{
		{"primary", cli, "core-module"}, {"bootstrap", bootstrap, "command-module"},
	} {
		t.Run(compiler.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "fault.wasm")
			args := []string{"-target", "wasm32-wasi", "-emit", compiler.emit, "-o", bin, src}
			if compiler.name == "primary" {
				args = append(args, stdlib)
			}
			cmd := exec.Command(compiler.cli, args...)
			if compiler.name == "primary" {
				cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1")
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			wat, err := exec.Command("wasm-tools", "print", "--name-unnamed", bin).Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name                            string
				openErr, closeErr               int
				writePrelude                    string
				result, calls, closed, progress int
			}{
				{name: "short writes", calls: 4, closed: 1, progress: 4},
				{name: "interrupted then short", writePrelude: `(if (i32.eq (global.get $file_calls) (i32.const 1)) (then (return (i32.const 27))))`, calls: 5, closed: 1, progress: 4},
				{name: "zero progress", writePrelude: `(i32.store (local.get 3) (i32.const 0)) (return (i32.const 0))`, result: 1, calls: 1, closed: 1},
				{name: "write error", writePrelude: `(return (i32.const 29))`, result: 1, calls: 1, closed: 1},
				{name: "error after partial write", writePrelude: `(if (i32.eq (global.get $file_calls) (i32.const 2)) (then (return (i32.const 29))))`, result: 1, calls: 2, closed: 1, progress: 1},
				{name: "close error", closeErr: 29, result: 1, calls: 4, closed: 1, progress: 4},
				{name: "open error", openErr: 2, result: 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					text := string(wat)
					var defs strings.Builder
					for name, body := range map[string]string{
						"path_open": fmt.Sprintf(`(param i32 i32 i32 i32 i32 i64 i64 i32 i32) (result i32)
  (i32.store (local.get 8) (i32.const 42)) (i32.const %d)`, tc.openErr),
						"fd_close": fmt.Sprintf(`(param i32) (result i32)
  (if (i32.ne (local.get 0) (i32.const 42)) (then unreachable))
  (global.set $file_closed (i32.add (global.get $file_closed) (i32.const 1))) (i32.const %d)`, tc.closeErr),
						"fd_write": fmt.Sprintf(`(param i32 i32 i32 i32) (result i32) (local $expected i32)
  (global.set $file_calls (i32.add (global.get $file_calls) (i32.const 1)))
  (if (i32.ne (local.get 0) (i32.const 42)) (then unreachable))
  (if (i32.ne (local.get 2) (i32.const 1)) (then unreachable))
  %s
  (if (i32.ne (i32.load offset=4 (local.get 1)) (i32.sub (i32.const 4) (global.get $file_progress))) (then unreachable))
  (if (i32.eq (global.get $file_progress) (i32.const 1)) (then (local.set $expected (i32.const 255))))
  (if (i32.eq (global.get $file_progress) (i32.const 2)) (then (local.set $expected (i32.const 65))))
  (if (i32.eq (global.get $file_progress) (i32.const 3)) (then (local.set $expected (i32.const 128))))
  (if (i32.ne (i32.load8_u (i32.load (local.get 1))) (local.get $expected)) (then unreachable))
  (global.set $file_progress (i32.add (global.get $file_progress) (i32.const 1)))
  (i32.store (local.get 3) (i32.const 1)) (i32.const 0)`, tc.writePrelude),
					} {
						re := regexp.MustCompile(`(?m)^  \(import "wasi_snapshot_preview1" "` + name + `" \(func (\$[^\s]+)[^\n]*\n`)
						matches := re.FindAllStringSubmatch(text, -1)
						if len(matches) != 1 {
							t.Fatalf("want one %s import, got %d", name, len(matches))
						}
						fmt.Fprintf(&defs, "(func %s %s)\n", matches[0][1], body)
						text = re.ReplaceAllString(text, "")
					}
					main := regexp.MustCompile(`\(export "main" \(func (\$[^\s)]+)\)\)`).FindStringSubmatch(text)
					if len(main) != 2 {
						t.Fatal("compiled module does not export main")
					}
					defs.WriteString(`(global $file_calls (mut i32) (i32.const 0))
(global $file_closed (mut i32) (i32.const 0))
(global $file_progress (mut i32) (i32.const 0))`)
					fmt.Fprintf(&defs, `(func (export "file_fault_test") (result i32)
 (if (i32.ne (call %s) (i32.const %d)) (then (return (i32.const 1))))
 (if (i32.ne (global.get $file_calls) (i32.const %d)) (then (return (i32.const 2))))
 (if (i32.ne (global.get $file_closed) (i32.const %d)) (then (return (i32.const 3))))
 (if (i32.ne (global.get $file_progress) (i32.const %d)) (then (return (i32.const 4))))
 (i32.const 0))`, main[1], tc.result, tc.calls, tc.closed, tc.progress)
					at := strings.LastIndex(text, ")")
					text = text[:at] + defs.String() + text[at:]
					path := filepath.Join(t.TempDir(), "injected.wat")
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					out, err := exec.CommandContext(ctx, "wasmtime", "run", "--invoke", "file_fault_test", path).Output()
					if err != nil || strings.TrimSpace(string(out)) != "0" {
						t.Fatalf("fault test: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
