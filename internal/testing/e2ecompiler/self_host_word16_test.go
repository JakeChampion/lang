package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The two bytes of a word in either order, from an array, a string and a
// byte view. A read past the end still aborts.
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
function be16(data: u8[], at: i32): i32 {
  return data[at] as i32 << 8 | data[at + 1] as i32;
}

@noinline
function be16s(s: string, at: i32): i32 {
  return s[at] as i32 << 8 | s[at + 1] as i32;
}

@noinline
function be16v(data: [u8], at: i32): i32 {
  return data[at] as i32 << 8 | data[at + 1] as i32;
}

@noinline
function le16v(data: [u8], at: i32): i32 {
  return data[at] as i32 | data[at + 1] as i32 << 8;
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
  if (be16(d, 1) != 767) { return 6; }
  if (be16s("ab\u{ff}", 1) != 25283) { return 7; }
  if (be16v(d, 2) != 65408) { return 8; }
  if (le16v(d, 2) != 33023) { return 9; }
  if (be16v("a\u{7f}b".as_bytes(), 1) != 32610) { return 10; }
  if (args().len() > 1) { return le16(d, 8, 1) + be16v(d, 8); }
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

// A two-byte word of an array, a string or a view is one zero-extending load
// after one bounds check, not two byte reads, a shift and an or.
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
			for _, name := range []string{"le16", "text16", "be16", "be16s", "be16v", "le16v"} {
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

// A helper that reads a word splices into each of its callers, from a string
// as from a view. The view of an array field is then read from the array,
// with no test of the kind of the view and no count of the record.
const word16SpliceProgram = `struct Table { data: u8[], at: i32 }

function word(b: [u8], at: i32): i32 {
  return b[at] as i32 << 8 | b[at + 1] as i32;
}

function digits(t: string, i: i32): i32 {
  return t[i] as i32 << 8 | t[i + 1] as i32;
}

@noinline
function head(t: Table, i: i32): i32 {
  return word(t.data, t.at + i) + word(t.data, t.at + i + 2) + word(t.data, t.at + i + 4);
}

@noinline
function pairs(t: string): i32 {
  return digits(t, 0) + digits(t, 2) + digits(t, 4);
}

function main(): i32 {
  let t = Table { data: [0u8, 1u8, 2u8, 3u8, 4u8, 5u8, 6u8, 7u8], at: 1 };
  if (head(t, 0) != 2316) { return 1; }
  if (pairs("0123456") != 38553) { return 2; }
  return 0;
}
`

func TestSelfHostWord16Splice(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "word16splice.fern")
	if err := os.WriteFile(src, []byte(word16SpliceProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	load := map[string]string{"x86-64-linux": "movzwl ", "arm64-linux": "ldrh w"}
	viewTest := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`andq \$2, %r`),
		"arm64-linux":  regexp.MustCompile(`and x\d+, x\d+, #2\b`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			text, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			asm := string(text)
			for _, helper := range []string{"word", "digits"} {
				if strings.Contains(asm, "\n__fn_"+helper+":") {
					t.Errorf("%s: %s is not spliced into its callers", target, helper)
				}
			}
			for _, name := range []string{"head", "pairs"} {
				body := asmFuncBody(t, asm, "__fn_"+name+".r")
				if n := strings.Count(body, load[target]); n != 3 {
					t.Errorf("%s: %s has %d of %q, want three:\n%s", target, name, n, load[target], body)
				}
				if viewTest[target].MatchString(body) {
					t.Errorf("%s: %s tests the kind of a view:\n%s", target, name, body)
				}
				if strings.Contains(body, "box_dec") || strings.Contains(body, "__sem_drop") {
					t.Errorf("%s: %s counts its argument:\n%s", target, name, body)
				}
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOfFile(t, src, target, nil); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
