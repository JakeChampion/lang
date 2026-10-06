package e2eharness

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
)

// ReaderTextFixture uses an independent UTF-8 oracle for exact physical
// chunks. Invalid reads consume their bytes; the following chunk must still
// be read correctly. The final oversized request exercises short-read storage.
type ReaderTextFixture struct {
	Source string
	Input  []byte
}

func MakeReaderTextFixture() ReaderTextFixture {
	chunks := [][]byte{
		[]byte("A\x00¢€𐐀\U0010ffff"),
		{0x80}, {0xbf}, {0xc0, 0x80}, {0xc1, 0xbf},
		{0xc2}, {0xdf}, {0xe0, 0x9f, 0xbf}, {0xed, 0xa0, 0x80},
		{0xe2, 0x82}, {0xf0, 0x8f, 0xbf, 0xbf}, {0xf4, 0x90, 0x80, 0x80},
		{0xf5, 0x80, 0x80, 0x80}, {0xff}, {0xf0, 0x9f, 0x99},
		// A valid scalar split across two physical reads is two refusals.
		{0xc3}, {0xa9},
		[]byte("text after rejection"), []byte("é€🙂\x00"),
		[]byte("tail\x00"),
	}
	var src strings.Builder
	src.WriteString(`function same(s: string, want: u8[]): boolean {
  if (s.len() != want.len()) { return false; }
  let i: i32 = 0;
  while (i < want.len()) { if (s[i] != want[i]) { return false; } i = i + 1; }
  return true;
}
function main(): i32 {
  let r = stdin();
  match (r.read_chunk(0 - 1)) { Ok(_) => { return 1; }, Err(_) => {} }
  match (r.read_chunk(0)) {
    Ok(s) => { if (s.len() != 0) { return 2; } },
    Err(e) => { match (e) { Interrupted => {}, _ => { return 3; } } }
  }
  let retained: string = "";
`)
	var input []byte
	for i, chunk := range chunks {
		if i == len(chunks)-1 {
			// Refusal consumes only the requested lead byte. The following
			// explicit raw read must preserve its continuation unchanged.
			input = append(input, 0xc3, 0xa9, 'Z')
			src.WriteString(`match (r.read_chunk(1)) {
    Ok(_) => { return 206; },
    Err(e) => { match (e) { InvalidUtf8(_) => {}, _ => { return 207; } } }
  }
  match (r.read_chunk_bytes(1)) {
    Ok(b) => { if (b.len() != 1 || b[0] != 169 as u8) { return 208; } },
    Err(_) => { return 209; }
  }
  match (r.read_chunk(1)) { Ok(s) => { if (s != "Z") { return 210; } }, Err(_) => { return 211; } }
`)
		}
		input = append(input, chunk...)
		n := len(chunk)
		if i == len(chunks)-1 {
			n = 4096
		}
		fmt.Fprintf(&src, "match (r.read_chunk(%d)) {\n", n)
		if utf8.Valid(chunk) {
			fmt.Fprintf(&src, "Ok(s) => { if (!same(s, %s)) { return %d; }", readerTextBytes(chunk), 10+i)
			if i == 0 {
				src.WriteString(" retained = s;")
			}
			fmt.Fprintf(&src, " }, Err(_) => { return %d; }\n", 40+i)
		} else {
			fmt.Fprintf(&src, "Ok(_) => { return %d; }, Err(e) => { match (e) { InvalidUtf8(p) => { if (p != \"\") { return %d; } }, _ => { return %d; } } }\n", 70+i, 100+i, 130+i)
		}
		src.WriteString("}\n")
	}
	fmt.Fprintf(&src, "if (!same(retained, %s)) { return 200; }\n", readerTextBytes(chunks[0]))
	src.WriteString(`match (r.read_chunk(9)) { Ok(s) => { if (s != "") { return 201; } }, Err(_) => { return 202; } }
  match (r.close()) { Some(_) => { return 203; }, None => {} }
  match (r.read_chunk(1)) { Ok(_) => { return 204; }, Err(_) => {} }
  match (r.read_chunk(0)) { Ok(_) => { return 205; }, Err(_) => {} }
  return 0;
}
`)
	return ReaderTextFixture{Source: src.String(), Input: input}
}

func readerTextBytes(data []byte) string {
	values := make([]string, len(data))
	for i, b := range data {
		values[i] = fmt.Sprintf("%d as u8", b)
	}
	return "[" + strings.Join(values, ", ") + "]"
}

func (f ReaderTextFixture) Check(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	cmd.Stdin = bytes.NewReader(f.Input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reader text: %v\n%s", err, out)
	}
	return string(out)
}
