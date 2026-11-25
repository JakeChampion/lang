package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The two bytes of a little-endian word, from an array and from a string. A
// read past the end still aborts.
const word16Program = `@noinline
function le16(data: u8[], cur: i32, best: i32): i32 {
  let at = cur + best - 1;
  return data[at] as i32 | data[at + 1] as i32 << 8;
}

@noinline
function text16(s: string, at: i32): i32 {
  return s[at] as i32 | s[at + 1] as i32 << 8;
}

@noinline
function le32(data: u8[], at: i32): u32 {
  return data[at] as u32 | data[at + 1] as u32 << 8 | data[at + 2] as u32 << 16 | data[at + 3] as u32 << 24;
}

@noinline
function mix64(data: u8[], cur: i32, best: i32): u64 {
  let at = cur + best - 1;
  let w = data[at] as u64 | data[at + 1] as u64 << 8 | data[at + 2] as u64 << 16 | data[at + 3] as u64 << 24
    | data[at + 4] as u64 << 32 | data[at + 5] as u64 << 40 | data[at + 6] as u64 << 48 | data[at + 7] as u64 << 56;
  return w ^ 5u64;
}

function main(): i32 {
  let d: u8[] = [1u8, 2u8, 255u8, 128u8, 7u8, 9u8, 11u8, 13u8, 200u8];
  let sum = 0;
  for i in 0..4 {
    sum = sum * 7 + le16(d, i, 1);
  }
  if (sum != 3607858) { return 1; }
  if (text16("ab\u{ff}", 1) != 50018) { return 2; }
  if (le32(d, 1) != 125894402u32) { return 3; }
  if (mix64(d, 1, 1) != 14415190115691331335u64) { return 4; }
  if (le16(d, -2147483647, -2147483647 - 1) != 513) { return 5; }
  if (args().len() > 1) { return le16(d, 8, 1); }
  return 0;
}
`

// The load of a word of an array has the offset of the first element in its
// address: one instruction on x86-64, and no add of 8 on arm64.
var wordAddress = map[string]map[string]string{
	"x86-64-linux": {
		"le16":  `movzwl 8\(%r\w+,%r\w+\), %\w+`,
		"le32":  `movslq 8\(%r\w+,%r\w+\), %r\w+`,
		"mix64": `movq 8\(%r\w+,%r\w+\), %r\w+`,
	},
	"arm64-linux": {
		"le16":  `ldrh w\d+, \[x\d+, #8\]`,
		"le32":  `ldrsw x\d+, \[x\d+, #8\]`,
		"mix64": `ldr x\d+, \[x\d+, #8\]`,
	},
}

var addOfEight = map[string]*regexp.Regexp{
	"x86-64-linux": regexp.MustCompile(`(addq \$8|leaq 8\(%r\w+\)), %r`),
	"arm64-linux":  regexp.MustCompile(`add x\d+, x\d+, #8\b`),
}

// A two-byte little-endian word of an array or a string is one zero-extending
// load after one bounds check, not two byte reads, a shift and an or.
func TestSelfHostWord16Load(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "word16.fern")
	if err := os.WriteFile(src, []byte(word16Program), 0o644); err != nil {
		t.Fatal(err)
	}
	load := map[string]string{"x86-64-linux": "movzwl ", "arm64-linux": "ldrh w", "wasm32-wasi": "i32.load16_u"}
	byteRead := map[string]string{"x86-64-linux": "movzbl", "arm64-linux": "ldrb", "wasm32-wasi": "i32.load8_u"}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			text, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"le16", "text16"} {
				var body string
				if target == "wasm32-wasi" {
					bodies := wasmFuncBodies(string(text), "$"+name+" ")
					if len(bodies) != 1 {
						t.Fatalf("%d bodies of $%s in the wat", len(bodies), name)
					}
					body = bodies[0]
				} else {
					body = asmFuncBody(t, string(text), "__fn_"+name+".r")
				}
				if strings.Count(body, load[target]) != 1 {
					t.Errorf("%s: %s has %d of %q, want one:\n%s", target, name, strings.Count(body, load[target]), load[target], body)
				}
				if strings.Contains(body, byteRead[target]) {
					t.Errorf("%s: %s still reads a byte (%q):\n%s", target, name, byteRead[target], body)
				}
			}
			for name, want := range wordAddress[target] {
				body := asmFuncBody(t, string(text), "__fn_"+name+".r")
				if !regexp.MustCompile(want).MatchString(body) {
					t.Errorf("%s: %s lacks %q:\n%s", target, name, want, body)
				}
				if name != "le32" {
					check := map[string]string{"x86-64-linux": "cmpq ", "arm64-linux": "cmp "}
					if strings.Count(body, check[target]) != 1 {
						t.Errorf("%s: %s needs one grouped bounds comparison:\n%s", target, name, body)
					}
					if target == "x86-64-linux" && !regexp.MustCompile(`leal -1\(%r\w+,%r\w+\), %\w+`).MatchString(body) {
						t.Errorf("%s: %s lacks the wrapping index calculation:\n%s", target, name, body)
					}
				}
				if addOfEight[target].MatchString(body) {
					t.Errorf("%s: %s adds 8 to the array before the load:\n%s", target, name, body)
				}
			}
			if stderr, code := cli.exitOfFile(t, src, target, nil); code != 0 {
				t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
			}
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"past-the-end"}); code != 134 {
				t.Errorf("%s: a read past the end exits %d, want the bounds abort 134\n%s", target, code, stderr)
			}
		})
	}
}
