package e2e

import "testing"

// Every string and array copy on the x86-64 SSA path goes through one helper,
// and every byte fill through another; both take a loop below 64 bytes and
// `rep` from there. The program below crosses every length from 0 to 70, the
// boundary either side of 64, and a few long ones, through concatenation,
// slicing, append, a u8 array (zero-filled at allocation) and a map (whose
// control bytes are filled), and prints a checksum of every result, which
// the flat build must reproduce byte for byte.
func TestX86_64SSACopiesEveryLengthWhole(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)
	src := `import "std/i32";
import "std/string";
import "core/map";
function sum(s: string): i32 {
  var t: i32 = 0;
  var i: i32 = 0;
  while (i < s.len()) { t = t + (s[i] as i32) * (i + 1); i = i + 1; }
  return t;
}
function alpha(n: i32): string {
  var s: string = "";
  var i: i32 = 0;
  while (i < n) { s = s + "abcdefghijklmnopqrstuvwxyz".slice_snap(i % 26, i % 26 + 1); i = i + 1; }
  return s;
}
function main(): i32 {
  var out: string = "";
  var n: i32 = 0;
  while (n <= 70 || n == 100 || n == 4096) {
    var a: string = alpha(n);
    var b: string = "|" + a + "|" + alpha(n / 2);
    var c: string = b.slice_snap(1, 1 + n);
    var arr: string[] = [];
    arr = arr.append(a);
    arr = arr.append(c);
    var bytes: u8[] = a.bytes();
    var z: u8[] = [];
    var k: i32 = 0;
    while (k < n) { z = z.append(bytes[k]); k = k + 1; }
    var m: Map[string, i32] = map_new(8);
    m = m.insert(a, n);
    out = out + n.to_string() + ":" + sum(a).to_string() + "," + sum(b).to_string() + "," + sum(c).to_string() + "," + sum(arr[1]).to_string() + "," + z.len().to_string() + "," + m.get_or(a, 0 - 1).to_string() + "\n";
    if (n == 70) { n = 100; } else if (n == 100) { n = 4096; } else if (n == 4096) { n = 5000; } else { n = n + 1; }
  }
  stdout().write(out);
  return out.len() % 251;
}
`
	ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, src, true)
	flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, src, false)
	if ssaOut != flatOut || ssaCode != flatCode {
		t.Errorf("the SSA build's copies differ from the flat build's:\nssa exit %d\n%s%s\nflat exit %d\n%s%s", ssaCode, ssaOut, ssaErr, flatCode, flatOut, flatErr)
	}
	if flatCode != len(flatOut)%251 || len(flatOut) < 500 {
		t.Errorf("the flat build did not produce the expected report (exit %d, %d bytes):\n%s%s", flatCode, len(flatOut), flatOut, flatErr)
	}
}
