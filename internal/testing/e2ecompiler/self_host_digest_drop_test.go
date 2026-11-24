package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Observe the eight chaining words after the last owner's drop and before
// the record goes back to the allocator. Inspect only this live allocation.
func TestSelfHostDigestDropClearsChainingWords(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wasmtime := e2eharness.Wasmtime(t)
	for _, tc := range []struct{ name, record, load, program string }{
		{"hmac", "Sha256", "i64.load32_u", "let tag = crypto.hmac_sha256([], []); return tag.len() - 32;"},
		{"pbkdf2", "Sha256", "i64.load32_u", "let Ok(tag) = crypto.pbkdf2_sha256([], [], 3, 40) else { return 1; }; return tag.len() - 40;"},
		{"argon2id-short", "Blake2b", "i64.load", argonDigestProgram(32)},
		{"argon2id-long", "Blake2b", "i64.load", argonDigestProgram(100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			program := "import \"std/crypto\";\nimport \"std/crypto/argon2id\";\nfunction main(): i32 { " + tc.program + " }"
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			path := cli.emit(t, src, "wasm32-wasi")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			wat := string(data)
			drop := "call $__sem_drop_crypto__" + tc.record + "\n    drop"
			release := wasmFuncBodies(wat, "$__sem_release_crypto__"+tc.record+" ")
			if len(release) != 1 || strings.Count(release[0], drop) != 1 {
				t.Fatal("missing digest record drop")
			}
			probe := drop + "\n    local.get 0\n    call $check_words"
			wat = strings.Replace(wat, release[0], strings.Replace(release[0], drop, probe, 1), 1)
			var hook strings.Builder
			hook.WriteString("(global $checked (mut i32) (i32.const 0))\n(func $check_words (param $p i32)\n")
			for slot := 1; slot <= 8; slot++ {
				fmt.Fprintf(&hook, "(if (i64.ne (%s offset=%d (local.get $p)) (i64.const 0)) (then unreachable))\n", tc.load, slot*8)
			}
			hook.WriteString("(global.set $checked (i32.const 1)))\n")
			main := regexp.MustCompile(`\(export "main" \(func (\$[^\s)]+)\)\)`).FindStringSubmatch(wat)
			if len(main) != 2 {
				t.Fatal("missing main export")
			}
			fmt.Fprintf(&hook, "(func (export \"check\") (result i32) (i32.or (call %s) (i32.eqz (global.get $checked)))))", main[1])
			if err := os.WriteFile(path, []byte(wat[:strings.LastIndex(wat, ")")]+hook.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(wasmtime, "run", "-W", "unknown-imports-trap", "--invoke", "check", path)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "0" {
				t.Fatalf("digest drop: %v, output %q\n%s", err, out, stderr.String())
			}
		})
	}
}

func argonDigestProgram(length int) string {
	return fmt.Sprintf(`let p = argon2id.Params { ...argon2id.defaults(), memory_kib: 32, lanes: 1, passes: 1, tag_len: %d };
let Ok(tag) = argon2id.key([], "testsalt".bytes(), p) else { return 1; };
return tag.len() - %d;`, length, length)
}
