package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The remaining binary operators overload on composites too — `%`→rem,
// `&`→bitand, `|`→bitor, `^`→bitxor, `<<`→shl, `>>`→shr — alongside the
// `+ - * /` (add/sub/mul/div) and `== <` (eq/cmp) overloads. Final value
// chained through all six is 2 (17 % 5). See #2706.
const bitwiseOverloadSrc = `struct F { b: i32 }
function (self: F) rem(o: F): F { return F { b: self.b % o.b }; }
function (self: F) bitand(o: F): F { return F { b: self.b & o.b }; }
function (self: F) bitor(o: F): F { return F { b: self.b | o.b }; }
function (self: F) bitxor(o: F): F { return F { b: self.b ^ o.b }; }
function (self: F) shl(o: F): F { return F { b: self.b << o.b }; }
function (self: F) shr(o: F): F { return F { b: self.b >> o.b }; }
function main(): i32 {
    var a: F = F { b: 12 };          // 1100
    var b: F = F { b: 10 };          // 1010
    var c: F = a & b;                // 1000 = 8
    c = a | b;                       // 1110 = 14
    c = a ^ b;                       // 0110 = 6
    c = F { b: 3 } << F { b: 1 };    // 6
    c = F { b: 12 } >> F { b: 1 };   // 6
    c = F { b: 17 } % F { b: 5 };    // 2
    return c.b;
}
`

func TestInterpBitwiseOverload(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(bitwiseOverloadSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Errorf("exit = %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64BitwiseOverload(t *testing.T) {
	out, code := compileAndRunX86_64(t, bitwiseOverloadSrc)
	if code != 2 {
		t.Errorf("exit = %d, want 2\n%s", code, out)
	}
}

func TestArm64BitwiseOverload(t *testing.T) {
	out, code := compileAndRunArm64(t, bitwiseOverloadSrc)
	if code != 2 {
		t.Errorf("exit = %d, want 2\n%s", code, out)
	}
}

func TestWASMBitwiseOverload(t *testing.T) {
	if code := runWasm(t, bitwiseOverloadSrc); code != 2 {
		t.Errorf("wasm exit = %d, want 2", code)
	}
}
