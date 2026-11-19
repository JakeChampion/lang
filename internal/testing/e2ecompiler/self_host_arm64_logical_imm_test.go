package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const arm64LogicalImmProgram = `@noinline function pack(x: i64, y: i64): i64 {
    return (x & (0i64 - 256i64)) | (y & 0xffi64) | 0x100i64;
}

@noinline function flip(x: i64): i64 {
    return x ^ 0x5555555555555555i64;
}

// 0x12345 is not a bitmask, so it stays in a register.
@noinline function odd_mask(x: i64): i64 {
    return x & 0x12345i64;
}

@noinline function narrow(x: u32): u32 {
    return (x & 0xfffffff0u32) | 3u32;
}

function main(): i32 {
    let a: i64 = pack(0x1234i64, 0xabcdi64);
    let b: i64 = flip(a);
    let c: i64 = odd_mask(0xfffffi64);
    let d: u32 = narrow(0xdeadbeefu32);
    if (a != 0x13cdi64) { return 1; }
    if (b != (0x13cdi64 ^ 0x5555555555555555i64)) { return 2; }
    if (c != 0x12345i64) { return 3; }
    if (d != 0xdeadbee3u32) { return 4; }
    return 0;
}
`

// On arm64 an `and`, an `or` or an `xor` with a constant that is a bitmask
// immediate has the constant in the instruction. A constant that is not a
// bitmask stays in a register.
func TestSelfHostArm64LogicalImmediate(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "logical_imm.fern")
	if err := os.WriteFile(src, []byte(arm64LogicalImmProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		if strings.HasPrefix(tg.target, "arm64") {
			asmPath := filepath.Join(dir, "logical_imm-"+tg.target+".s")
			h.compileWith(t, tg, src, asmPath, "-emit", "asm")
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string][]string{
				"__fn_pack":     {`and x\d+, x\d+, #-256`, `and x\d+, x\d+, #255`, `orr x\d+, x\d+, #256`},
				"__fn_narrow":   {`and x\d+, x\d+, #4294967280`, `orr x\d+, x\d+, #3`},
				"__fn_odd_mask": {`and x\d+, x\d+, x\d+`},
			} {
				fn := functionListing(string(asm), name)
				for _, re := range want {
					if !regexp.MustCompile(re).MatchString(fn) {
						t.Errorf("%s: %s has no `%s`:\n%s", tg.target, name, re, fn)
					}
				}
			}
		}
		bin := filepath.Join(dir, "logical_imm-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 0 {
			t.Errorf("%s: exit = %d, want 0", tg.target, code)
		}
	}
}
