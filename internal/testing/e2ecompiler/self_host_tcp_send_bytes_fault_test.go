package e2ecompiler

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

// Execute both compilers' byte-array bridges against deterministic stream
// errors. Check accepted progress, exact bytes, chunk bounds and owned errors.
func TestSelfHostTCPSendBytesFaults(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	for _, compiler := range []struct{ name, cli, emit string }{
		{"primary", primary, "core-module"}, {"bootstrap", bootstrap, "command-module"},
	} {
		for _, size := range []int{0, 8193} {
			t.Run(fmt.Sprintf("%s/bytes%d", compiler.name, size), func(t *testing.T) {
				dir := t.TempDir()
				src, bin := filepath.Join(dir, "fault.fern"), filepath.Join(dir, "fault.wasm")
				program := fmt.Sprintf(`function main(): i32 {
    let data: u8[] = [];
    for i in 0..%d { data = data.append((i %% 251) as u8); }
    let n: i32 = tcp_send_bytes(0, data);
    if (data.len() != %d) { return -100; }
    for i in 0..data.len() { if (data[i] != (i %% 251) as u8) { return -101; } }
    return n;
}`, size, size)
				if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-target", "wasm32-wasi", "-emit", compiler.emit, "-o", bin, src}
				if compiler.name == "primary" {
					args = append(args, stdlib)
				}
				if out, err := exec.Command(compiler.cli, args...).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				wat, err := exec.Command("wasm-tools", "print", "--name-unnamed", bin).Output()
				if err != nil {
					t.Fatal(err)
				}
				for _, failAt := range []int{0, 1, 2, 3} {
					for _, errorTag := range []int{0, 1} {
						for _, handle := range []int{0, 7} {
							t.Run(fmt.Sprintf("fail%d/tag%d/handle%d", failAt, errorTag, handle), func(t *testing.T) {
								checkTCPSendBytesFault(t, string(wat), size, failAt, errorTag, handle)
							})
						}
					}
				}
			})
		}
	}
}

func checkTCPSendBytesFault(t *testing.T, wat string, size, failAt, errorTag, handle int) {
	t.Helper()
	imports := regexp.MustCompile(`(?m)^  \(import "wasi:io/[^\"]+" "([^\"]+)" \(func (\$[^\s]+)[^\n]*\n`)
	var defs strings.Builder
	writes, drops := 0, 0
	for _, m := range imports.FindAllStringSubmatch(wat, -1) {
		switch m[1] {
		case "[method]output-stream.blocking-write-and-flush":
			writes++
			fmt.Fprintf(&defs, `(func %s (param i32 i32 i32 i32) (local $i i32)
 (global.set $calls (i32.add (global.get $calls) (i32.const 1)))
 (if (i32.or (i32.eqz (local.get 2)) (i32.gt_u (local.get 2) (i32.const 4096))) (then unreachable))
 (block $done (loop $bytes
  (br_if $done (i32.ge_u (local.get $i) (local.get 2)))
  (if (i32.ne (i32.load8_u (i32.add (local.get 1) (local.get $i)))
    (i32.rem_u (i32.add (global.get $progress) (local.get $i)) (i32.const 251))) (then unreachable))
  (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $bytes)))
 (i32.store8 (local.get 3) (i32.const 0))
 (if (i32.eq (global.get $calls) (i32.const %d)) (then
  (i32.store8 (local.get 3) (i32.const 1))
  (i32.store8 offset=4 (local.get 3) (i32.const %d))
  (i32.store offset=8 (local.get 3) (i32.const %d))
  (global.set $owned (i32.const %d)) (return)))
 (global.set $progress (i32.add (global.get $progress) (local.get 2))))
`, m[2], failAt, errorTag, handle, 1-errorTag)
		case "[resource-drop]error":
			drops++
			fmt.Fprintf(&defs, `(func %s (param i32)
 (if (i32.ne (global.get $owned) (i32.const 1)) (then unreachable))
 (if (i32.ne (local.get 0) (i32.const %d)) (then unreachable))
 (global.set $owned (i32.const 0)))
`, m[2], handle)
		default:
			typeRef := regexp.MustCompile(`\(type ([^\s)]+)\)`).FindStringSubmatch(m[0])
			if len(typeRef) != 2 {
				t.Fatalf("unrecognized import %s", m[0])
			}
			fmt.Fprintf(&defs, "(func %s (type %s) unreachable)\n", m[2], typeRef[1])
		}
	}
	if writes != 1 || drops != 1 {
		t.Fatalf("expected one stream write and error drop, got %d and %d", writes, drops)
	}
	wat = imports.ReplaceAllString(wat, "")
	main := regexp.MustCompile(`\(export "main" \(func (\$[^\s)]+)\)\)`).FindStringSubmatch(wat)
	if len(main) != 2 {
		t.Fatal("compiled module does not export main")
	}
	result, progress, calls := size, size, (size+4095)/4096
	if size > 0 && failAt > 0 {
		progress, calls = (failAt-1)*4096, failAt
		result = progress
		if result == 0 {
			result = -1
		}
	}
	defs.WriteString(`(global $calls (mut i32) (i32.const 0))
(global $progress (mut i32) (i32.const 0))
(global $owned (mut i32) (i32.const 0))`)
	fmt.Fprintf(&defs, `(func (export "send_fault") (result i32)
 (if (i32.ne (call %s) (i32.const %d)) (then (return (i32.const 1))))
 (if (i32.ne (global.get $calls) (i32.const %d)) (then (return (i32.const 2))))
 (if (i32.ne (global.get $progress) (i32.const %d)) (then (return (i32.const 3))))
 (if (global.get $owned) (then (return (i32.const 4))))
 (i32.const 0))`, main[1], result, calls, progress)
	at := strings.LastIndex(wat, ")")
	path := filepath.Join(t.TempDir(), "injected.wat")
	if err := os.WriteFile(path, []byte(wat[:at]+defs.String()+wat[at:]), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wasmtime", "run", "--invoke", "send_fault", path).Output()
	if err != nil || strings.TrimSpace(string(out)) != "0" {
		t.Fatalf("fault oracle: %v\n%s", err, out)
	}
}
