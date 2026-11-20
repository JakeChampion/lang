package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Check the array while it still belongs to the program, immediately before
// its last reference is released. The fixture uses a distinct record length.
func TestSelfHostRefusedAEADRecordErased(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	for _, cipher := range []string{"aes_gcm", "chacha20poly1305"} {
		t.Run(cipher, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			program := `import "std/crypto/aead";
import "std/crypto/` + cipher + `";
function main(): i32 {
  let Ok(k) = ` + cipher + `.key(__alloc_u8(32)) else { return 1; };
  let record = __alloc_u8(113).with(0, 91 as u8);
  let result = k.open_in_place(__alloc_u8(12), 0 as u64, record, 0, 1, 1, 96);
  return match (result) { Ok(_) => 1, Err(e) => match (e) { aead.Forged => 0, _ => 1 } };
}`
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			path := cli.emit(t, src, "wasm32-wasi")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			wat := string(data)
			bodies := wasmFuncBodies(wat, "$__fern_arr_dec ")
			const loadRC = "(local.set $rc (i32.load (local.get $base)))"
			if len(bodies) != 1 || strings.Count(bodies[0], loadRC) != 1 {
				t.Fatal("cannot locate array release")
			}
			const inspect = `(if (i32.and (i32.eq (local.get $rc) (i32.const 1))
				(i32.eq (i32.load (local.get $a)) (i32.const 113)))
				(then (call $check_record (local.get $a))))`
			wat = strings.Replace(wat, bodies[0], strings.Replace(bodies[0], loadRC, loadRC+inspect, 1), 1)
			const hook = `(global $checked (mut i32) (i32.const 0))
(func $check_record (param $a i32) (local $i i32)
  (loop $bytes
    (if (i32.load8_u offset=8 (i32.add (local.get $a) (local.get $i))) (then unreachable))
    (local.set $i (i32.add (local.get $i) (i32.const 1)))
    (br_if $bytes (i32.lt_u (local.get $i) (i32.const 113))))
  (global.set $checked (i32.const 1)))`
			main := regexp.MustCompile(`\(export "main" \(func (\$[^\s)]+)\)\)`).FindStringSubmatch(wat)
			if len(main) != 2 {
				t.Fatal("cannot locate main")
			}
			end := strings.LastIndex(wat, ")")
			entry := `(func (export "check") (result i32)
			  (i32.or (call ` + main[1] + `) (i32.eqz (global.get $checked))))`
			if err := os.WriteFile(path, []byte(wat[:end]+hook+entry+wat[end:]), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(wasmtime, "run", "-W", "unknown-imports-trap", "--invoke", "check", path)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "0" {
				t.Fatalf("refused record erasure: %v, output %q\n%s", err, out, stderr.String())
			}
		})
	}
}
