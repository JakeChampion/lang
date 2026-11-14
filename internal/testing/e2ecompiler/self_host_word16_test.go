package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two bytes of a little-endian word, from an array and from a string. A
// read past the end still aborts.
const word16Program = `@noinline
function le16(data: u8[], at: i32): i32 {
  return data[at] as i32 | data[at + 1] as i32 << 8;
}

@noinline
function text16(s: string, at: i32): i32 {
  return s[at] as i32 | s[at + 1] as i32 << 8;
}

function main(): i32 {
  let d: u8[] = [1u8, 2u8, 255u8, 128u8, 7u8];
  let sum = 0;
  for i in 0..4 {
    sum = sum * 7 + le16(d, i);
  }
  if (sum != 3607858) { return 1; }
  if (text16("ab\u{ff}", 1) != 50018) { return 2; }
  if (args().len() > 1) { return le16(d, 4); }
  return 0;
}
`

// A two-byte little-endian word of an array or a string is one zero-extending
// load after one bounds check, not two byte reads, a shift and an or.
func TestSelfHostWord16Load(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "word16.fern")
	if err := os.WriteFile(src, []byte(word16Program), 0o644); err != nil {
		t.Fatal(err)
	}
	load := map[string]string{"x86-64-linux": "movzwl (", "arm64-linux": "ldrh w", "wasm32-wasi": "i32.load16_u"}
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
			if stderr, code := cli.exitOfFile(t, src, target, nil); code != 0 {
				t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
			}
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"past-the-end"}); code != 134 {
				t.Errorf("%s: a read past the end exits %d, want the bounds abort 134\n%s", target, code, stderr)
			}
		})
	}
}
