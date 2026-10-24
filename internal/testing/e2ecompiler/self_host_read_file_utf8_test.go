package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func readFileUTF8Cases() [][]byte {
	ascii := func(n int) []byte { return []byte(strings.Repeat("a", n)) }
	with := func(n, at int, b []byte) []byte {
		out := append(ascii(at), b...)
		return append(out, ascii(n-at)...)
	}
	var cases [][]byte
	for n := 0; n <= 70; n++ {
		cases = append(cases, ascii(n))
	}
	for _, b := range [][]byte{
		[]byte("é"), []byte("€"), []byte("🙂"),
		{0xff}, {0x80}, {0xc0, 0x80}, {0xe0, 0x80, 0x80}, {0xed, 0xa0, 0x80},
		{0xe2, 0x82}, {0xf0, 0x9f, 0x99}, {0xc3, 'a'},
	} {
		for at := 0; at <= 68; at++ {
			cases = append(cases, with(68, at, b))
		}
		for _, n := range []int{28, 29, 30, 31, 32, 60, 61, 62, 63, 64} {
			cases = append(cases, append(ascii(n), b...))
		}
	}
	wide := func(n int) []byte { return []byte(strings.Repeat("é", n)) }
	specials := [][]byte{
		[]byte("é"), []byte("€"), []byte("🙂"), []byte("\u0800"), []byte("\U00010000"), []byte("\U0010ffff"),
		{0xff}, {0x80}, {0xc0, 0x80}, {0xc1, 0xbf}, {0xe0, 0x9f, 0xbf}, {0xed, 0xa0, 0x80}, {0xf0, 0x8f, 0xbf, 0xbf},
		{0xf4, 0x90, 0x80, 0x80}, {0xf5, 0x80, 0x80, 0x80},
		{0xe2, 0x82}, {0xf0, 0x9f, 0x99}, {0xc3, 'a'}, {0xe2, 0x82, 0x82, 0x82},
	}
	for _, b := range specials {
		for at := 0; at <= 100; at++ {
			out := append(wide(at/2), []byte(strings.Repeat("a", at%2))...)
			out = append(out, b...)
			cases = append(cases, append(out, wide(60-at/2)...))
		}
	}
	for lead := 0xc0; lead <= 0xff; lead++ {
		for _, second := range []byte{0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0} {
			seq := []byte{byte(lead), second, 0x80, 0x80}
			for _, n := range []int{2, 3, 4} {
				out := append(wide(20), seq[:n]...)
				cases = append(cases, append(out, wide(20)...))
			}
		}
	}
	rng := rand.New(rand.NewSource(12105))
	runes := []rune{'a', 'é', '€', '日', '🙂', '\u07ff', '\uffff', '\U0010ffff'}
	for k := 0; k < 300; k++ {
		var b []byte
		for len(b) < 300 && rng.Intn(150) != 0 {
			b = utf8.AppendRune(b, runes[rng.Intn(len(runes))])
		}
		if len(b) > 0 && rng.Intn(3) != 0 {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		cases = append(cases, b)
	}
	for k := 0; k < 300; k++ {
		b := make([]byte, rng.Intn(100))
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
			if rng.Intn(20) == 0 {
				b[i] = byte(0x80 + rng.Intn(0x80))
			}
		}
		cases = append(cases, b)
	}
	return cases
}

func readFileUTF8Program(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	var want strings.Builder
	cases := readFileUTF8Cases()
	for i, b := range cases {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), b, 0o644); err != nil {
			t.Fatal(err)
		}
		if utf8.Valid(b) {
			want.WriteString("1")
		} else {
			want.WriteString("0")
		}
	}
	src := fmt.Sprintf(`import "std/i32";
function main(): i32 {
  let i: i32 = 0;
  let out: string = "";
  while (i < %d) {
    match (read_file(%q + i.to_string())) {
      Ok(_) => { out = out + "1"; },
      Err(e) => { match (e) { InvalidUtf8(_) => { out = out + "0"; }, _ => { out = out + "E"; } } }
    }
    i = i + 1;
  }
  print(out);
  return 0;
}
`, len(cases), dir+"/")
	return src, want.String() + "\n"
}

func TestSelfHostReadFileUTF8BlocksX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	src, want := readFileUTF8Program(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); code != 0 || out != want {
		t.Fatalf("exit %d\n got %q\nwant %q", code, out, want)
	}
}

func TestSelfHostReadFileUTF8BlocksArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	src, want := readFileUTF8Program(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d\n got %q\nwant %q", code, out, want)
	}
}
