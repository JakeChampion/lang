package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const constantIntegerWordsSource = `
@noinline function unsigned_word(k: i32): u32 {
 let xs: u32[] = [0xffff_ffffu32, 0x80000000u32, 4_294_967_295u32, 2147483648u32,
  18446744073709551615u64 as u32, 0x100000005u64 as u32, 0o377u32, 0b101010u32];
 return xs[k];
}
@noinline function signed_word(k: i32): i32 {
 let xs: i32[] = [0xffffffffu64 as i32, 0x80000000u64 as i32,
  18446744073709551615u64 as i32, 4294967297u64 as i32];
 return xs[k];
}
struct Narrow { a: u8, b: i32, c: u32 }
@noinline function read_fields(r: Narrow): i32 {
 if (r.a != 52u8 || r.b != -1 || r.c != 4294967295u32) { return 1; }
 return 0;
}
@noinline function fresh(v: i32): u32 {
 let xs: u32[] = [0xffffffffu32, 0x80000000u32];
 let before = xs[0];
 unsafe { __store_i32(xs as usize + 8 as usize, v); }
 if (before != 4294967295u32) { return 0u32; }
 return xs[0];
}
function main(): i32 {
 let expected: u32[] = [4294967295u32,2147483648u32,4294967295u32,2147483648u32,4294967295u32,5u32,255u32,42u32];
 for i in 0..8 { if (unsigned_word(i) != expected[i]) { return 1; } }
 let signed: i32[] = [-1,-2147483648,-1,1];
 for i in 0..4 { if (signed_word(i) != signed[i]) { return 2; } }
 let fields = Narrow { a: 0x1234u64 as u8, b: 0xffffffffu64 as i32, c: 18446744073709551615u64 as u32 };
 if (read_fields(fields) != 0) { return 3; }
 let original: u32[] = [0xffffffffu32,0x80000000u32];
 let changed = original.with(0,7u32);
 if (original[0] != 4294967295u32 || original[1] != 2147483648u32 || changed[0] != 7u32 || changed[1] != 2147483648u32) { return 4; }
 if (fresh(7) != 7u32 || fresh(9) != 9u32) { return 5; }
 return 0;
}
`

func TestSelfHostConstantIntegerWords(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, constantIntegerWordsSource, target); code != 0 {
				t.Fatalf("constant integer representation: exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostConstantIntegerWordsShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "integer_words.fern")
	if err := os.WriteFile(src, []byte(constantIntegerWordsSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, name := range []string{"unsigned_word", "signed_word"} {
				body := asmFuncBody(t, text, "__fn_"+name)
				if strings.Contains(body, "__fern_arr_box") {
					t.Errorf("%s allocates its constant table:\n%s", name, body)
				}
			}
			if body := asmFuncBody(t, text, "__fn_fresh"); !strings.Contains(body, "__fern_arr_box") {
				t.Errorf("raw writable literal must remain fresh:\n%s", body)
			}
		})
	}
}
