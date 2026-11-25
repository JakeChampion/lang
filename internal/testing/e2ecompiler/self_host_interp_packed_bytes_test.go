package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host interpreter keeps the bytes of an __alloc_u8 array and of a
// byte builtin's result packed. Each step reads or writes that store.
const interpPackedBytesSrc = `function fill(b: u8[], v: u8): u8[] {
  let out = b;
  for i in 0..out.len() { out = out.with(i, v); }
  return out;
}
function main(): i32 {
  let a = __alloc_u8(4);
  let b = a;
  a = a.with(0, 9 as u8);
  a = a.append(255 as u8);
  if (b[0] != 0 || b.len() != 4) { return 1; }
  if (a[0] != 9 || a[4] != 255 || a.len() != 5) { return 2; }
  let s = a[0:2];
  if (s.len() != 2 || s[0] != 9) { return 3; }
  let sum: i32 = 0;
  for x in a { sum = sum + x as i32; }
  if (sum != 264) { return 4; }
  let f = fill(b, 7 as u8);
  if (f[3] != 7 || b[3] != 0) { return 5; }
  let t = a.truncate(1);
  if (t.len() != 1 || a.len() != 5) { return 6; }
  let text = unsafe { string_from_bytes_unchecked(a.with(0, 104 as u8).with(1, 105 as u8).truncate(2)) };
  if (text != "hi") { return 7; }
  let back = text.as_bytes();
  if (back.len() != 2 || back[1] != 105) { return 8; }
  print("ok");
  return 0;
}
`

// A u8[] of 256 MiB, which is past the element limit of a compiled Value[].
// The Go oracle holds 16 bytes for each element (#17245), so only the
// self-host interpreter runs it.
const interpLargeBytesSrc = `function main(): i32 {
  let a = __alloc_u8(268435456);
  a = a.with(268435455, 7 as u8);
  print(a.len().to_string() + " " + a[268435455].to_string() + " " + a[0].to_string());
  return 0;
}
`

func TestSelfHostInterpPackedBytes(t *testing.T) {
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	small := filepath.Join(dir, "packed.fern")
	large := filepath.Join(dir, "large.fern")
	if err := os.WriteFile(small, []byte(interpPackedBytesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(large, []byte(interpLargeBytesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	self := e2eharness.SelfHostCLI(t)
	for _, cli := range []string{buildLangBinForInterp(t), self} {
		if out, err := exec.Command(cli, "-interp", small, stdlib).CombinedOutput(); err != nil || string(out) != "ok\n" {
			t.Fatalf("%s -interp: %v\n%s", cli, err, out)
		}
	}
	if out, err := exec.Command(self, "-interp", large, stdlib).CombinedOutput(); err != nil || string(out) != "268435456 7 0\n" {
		t.Fatalf("self-host -interp of a 256 MiB u8[]: %v\n%s", err, out)
	}
}
