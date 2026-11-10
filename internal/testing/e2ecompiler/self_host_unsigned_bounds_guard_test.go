package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unsignedBoundsGuardFunctions = `
@noinline function guarded(data: u8[], index: i32): i32 {
 if (index as u32 >= data.len() as u32) { return -1; }
 return data[index] as i32;
}
@noinline function reversed(data: u8[], index: i32): i32 {
 if (data.len() as u32 <= index as u32) { return -1; }
 return data[index] as i32;
}
@noinline function positive(data: u8[], index: i32): i32 {
 if (index as u32 < data.len() as u32) { return data[index] as i32; }
 return -1;
}
@noinline function greater(data: u8[], index: i32): i32 {
 if (data.len() as u32 > index as u32) { return data[index] as i32; }
 return -1;
}
@noinline function unrelated(data: u8[], index: i32, bound: i32): i32 {
 if (index as u32 >= bound as u32) { return -1; }
 return data[index] as i32;
}
@noinline function inclusive(data: u8[], index: i32): i32 {
 if (index as u32 > data.len() as u32) { return -1; }
 return data[index] as i32;
}
@noinline function wide(data: u8[], index: u64): i32 {
 if (index as u32 >= data.len() as u32) { return -1; }
 return data[(index >> 32u64) as i32] as i32;
}
`

func TestSelfHostUnsignedBoundsGuard(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const valid = `function main(): i32 {
 let data: u8[] = [7u8,17u8,33u8,255u8];
 for i in -8..12 {
  let expected = if (i < 0 || i >= 4) { -1 } else { data[i] as i32 };
  if (guarded(data,i) != expected || reversed(data,i) != expected || positive(data,i) != expected || greater(data,i) != expected) { return 1; }
 }
 let empty: u8[] = [];
 for i in [-2147483648,-1,0,1,2147483647] {
  if (guarded(empty,i) != -1 || reversed(empty,i) != -1 || positive(empty,i) != -1 || greater(empty,i) != -1) { return 2; }
 }
 if (guarded(data,-2147483648) != -1 || guarded(data,2147483647) != -1) { return 3; }
 if (unrelated(data,3,4) != 255 || inclusive(data,3) != 255 || wide(data,4294967296u64) != 17) { return 4; }
 return 0;
 }`
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, unsignedBoundsGuardFunctions+valid, target); code != 0 {
				t.Fatalf("unsigned guard behavior: exit %d\n%s", code, stderr)
			}
			for _, call := range []string{"unrelated([1u8],1,2)", "unrelated([1u8],-2,-1)", "inclusive([1u8],1)", "wide([1u8],4294967296u64)"} {
				t.Run(call, func(t *testing.T) {
					stderr, code := cli.exitOf(t, unsignedBoundsGuardFunctions+"function main(): i32 { return "+call+"; }", target)
					if code == 0 || !strings.Contains(stderr, "out of range") {
						t.Fatalf("missing bounds trap: exit %d\n%s", code, stderr)
					}
				})
			}
		})
	}
}

func TestSelfHostUnsignedBoundsGuardShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "unsigned_guard.fern")
	source := unsignedBoundsGuardFunctions + `function main(): i32 { let data: u8[] = [7u8]; return guarded(data,0)+reversed(data,0)+positive(data,0)+greater(data,0)+unrelated(data,0,1)+inclusive(data,0)+wide(data,0u64); }`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, name := range []string{"guarded", "reversed", "positive", "greater", "unrelated", "inclusive", "wide"} {
				start := strings.Index(text, "\n__fn_"+name+":\n")
				if start < 0 {
					t.Fatalf("missing %s", name)
				}
				body := text[start:]
				end := strings.Index(body, ".cfi_endproc")
				if end < 0 {
					t.Fatalf("missing boundary for %s", name)
				}
				body = body[:end]
				wantCheck := name == "unrelated" || name == "inclusive" || name == "wide"
				if strings.Contains(body, "__fern_oob_abort") != wantCheck {
					t.Errorf("%s unexpected bounds check state:\n%s", name, body)
				}
			}
		})
	}
}
