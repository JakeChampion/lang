package e2eharness

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const ioTextLineProgram = `import "std/io_buffered" as io;
import "std/utf8";
import "std/i32";
function error_kind(lr: io.LineReader): i32 {
    match (lr.error()) {
        None => { return 0; },
        Some(InvalidUtf8(path)) => { if (path != "") { return 3; } return 1; },
        Some(_) => { return 2; }
    }
    return 3;
}
function main(): i32 {
    let r: Reader = stdin();
    let lr: io.LineReader = io.line_reader_new(r, TERM, CHUNK);
    let held: string[] = [];
    let step: i32 = 0;
    while (true) {
        let next: (Option[string], io.LineReader) = (None, lr);
        if (MODE == 0 || (MODE == 2 && step % 2 == 0)) { next = lr.next_line(); }
        else { next = lr.next_chunk(); }
        lr = next.1;
        match (next.0) {
            None => { break; },
            Some(text) => {
                if (text.len() == 0 || !utf8.is_valid_utf8(text)) { return 4; }
                held = held.append(text);
            }
        }
        step = step + 1;
    }
    let status: i32 = error_kind(lr);
    let (line, after_line) = lr.next_line();
    match (line) { Some(_) => { return 5; }, None => {} }
    let (chunk, after_chunk) = after_line.next_chunk();
    match (chunk) { Some(_) => { return 6; }, None => {} }
    if (error_kind(after_chunk) != status) { return 7; }
    match (r.close()) { Some(_) => { return 8; }, None => {} }
    write(status.to_string() + ":");
    let pass: i32 = 0;
    while (pass < 2) {
        for text in held { write(text.len().to_string() + ":" + text); }
        pass = pass + 1;
    }
    return 0;
}
`

// Reuse the byte-reader target harness while checking the text contract.
func IOTextLineCases() []IOByteLineCase {
	var cases []IOByteLineCase
	for _, chunk := range []int{1, 2, 3, 4, 7, 4096} {
		for mode, name := range []string{"lines", "chunks", "alternating"} {
			cases = append(cases, ioTextLineCase(fmt.Sprintf("%s/%d", name, chunk), chunk, mode, 10))
		}
	}
	for mode, name := range []string{"lines", "chunks", "alternating"} {
		cases = append(cases, ioTextLineCase("nul-"+name, 7, mode, 0))
	}
	for _, closed := range []bool{false, true} {
		name, chunk := "negative size", -1
		if closed {
			name, chunk = "closed reader", 7
		}
		cases = append(cases, IOByteLineCase{Name: name, Source: IOTextLineReadErrorProgram(closed, chunk, 0, 0),
			Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
				checkIOByteLineOutput(t, command(), nil, nil, census)
			},
		})
	}
	return cases
}

func ioTextLineCase(name string, chunk, mode, term int) IOByteLineCase {
	return IOByteLineCase{
		Name:   name,
		Source: strings.NewReplacer("CHUNK", fmt.Sprint(chunk), "MODE", fmt.Sprint(mode), "TERM", fmt.Sprint(term)).Replace(ioTextLineProgram),
		Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
			checkIOTextLines(t, command, census, chunk, mode, byte(term))
		},
	}
}

func IOTextLineReadErrorProgram(closed bool, chunk, length, value int) string {
	return strings.NewReplacer("io.ByteLineReader", "io.LineReader", "io.byte_line_reader_new", "io.line_reader_new",
		"Option[u8[]]", "Option[string]", "next_line_bytes", "next_line", "next_chunk_bytes", "next_chunk").Replace(
		IOByteLineReadErrorProgram(closed, chunk, length, value))
}

func checkIOTextLines(t *testing.T, command func() *exec.Cmd, census bool, chunk, mode int, term byte) {
	t.Helper()
	valid := [][]byte{nil, []byte("\n"), []byte("a\x00b\nlast"), []byte("¢€𐐀\n終"), []byte("a\n𐐀z\n¢\n€"), []byte("𐐀\x00€\x00¢")}
	for prefix := 0; prefix < 8; prefix++ {
		valid = append(valid, []byte(strings.Repeat("x", prefix)+"¢€𐐀\n𐐀€¢"))
	}
	if chunk == 4096 {
		valid = append(valid, bytes.Repeat([]byte("¢€𐐀"), 2049))
	}
	invalid := [][]byte{{0x80}, {0xff}, {0xc0, 0xaf}, {0xc1, 0xbf}, {0xe0, 0x80, 0xaf},
		{0xed, 0xa0, 0x80}, {0xf0, 0x80, 0x80, 0xaf}, {0xf4, 0x90, 0x80, 0x80},
		{0xf5, 0x80, 0x80, 0x80}, {0xc2, 'a'}, {0xe2, 0x82, 'a'}, {0xf0, 0x9f, 0x8c, 'a'},
		{0xf0, 0x9f, 0x8c, 0x8d, 0x80}, {0x80, 0xe2}, {0xe2, 0x82, 0xac, 0x80}}
	for _, scalar := range []string{"¢", "€", "𐐀"} {
		for cut := 1; cut < len(scalar); cut++ {
			invalid = append(invalid, []byte(scalar[:cut]))
		}
	}
	for i, input := range valid {
		t.Run(fmt.Sprintf("valid/%d", i), func(t *testing.T) {
			checkIOTextLineResult(t, command(), input, false, census, mode, term)
		})
	}
	for i, bad := range invalid {
		for _, prefix := range []string{"", "ok\n𐐀"} {
			input := append([]byte(prefix), bad...)
			t.Run(fmt.Sprintf("invalid/%d/%d", i, len(prefix)), func(t *testing.T) {
				checkIOTextLineResult(t, command(), input, true, census, mode, term)
			})
		}
	}
}

var ioTextLineCensus = regexp.MustCompile(`(?m)^leakcheck: allocs=([0-9]+) frees=([0-9]+) live_bytes=([0-9]+)$`)

func checkIOTextLineResult(t *testing.T, cmd *exec.Cmd, input []byte, invalid, census bool, mode int, term byte) {
	t.Helper()
	cmd.Stdin = bytes.NewReader(input)
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, diagnostic.String())
	}
	status := []byte("0:")
	if invalid {
		status = []byte("1:")
	}
	got := out.Bytes()
	if !bytes.HasPrefix(got, status) {
		t.Fatalf("status: got %q, want prefix %q", got, status)
	}
	got = got[len(status):]
	if len(got)%2 != 0 || !bytes.Equal(got[:len(got)/2], got[len(got)/2:]) {
		t.Fatalf("retained text changed: %q", got)
	}
	framed := got[:len(got)/2]
	var records [][]byte
	var joined []byte
	for len(framed) > 0 {
		colon := bytes.IndexByte(framed, ':')
		if colon < 0 {
			t.Fatalf("missing record length: %q", framed)
		}
		n, err := strconv.Atoi(string(framed[:colon]))
		framed = framed[colon+1:]
		if err != nil || n <= 0 || n > len(framed) {
			t.Fatalf("bad record length: %d, %v", n, err)
		}
		record := framed[:n]
		if !utf8.Valid(record) {
			t.Fatalf("invalid text record: %q", record)
		}
		records = append(records, record)
		joined = append(joined, record...)
		framed = framed[n:]
	}
	if !bytes.HasPrefix(input, joined) || (!invalid && !bytes.Equal(input, joined)) {
		t.Fatalf("text changed: input %q, output %q", input, joined)
	}
	if mode == 0 {
		var want [][]byte
		for _, line := range bytes.SplitAfter(input, []byte{term}) {
			if !utf8.Valid(line) {
				break
			}
			if len(line) > 0 {
				want = append(want, line)
			}
		}
		if len(records) != len(want) {
			t.Fatalf("got %d records, want %d", len(records), len(want))
		}
		for i := range want {
			if !bytes.Equal(records[i], want[i]) {
				t.Fatalf("record %d: got %q, want %q", i, records[i], want[i])
			}
		}
	}
	if census {
		counts := ioTextLineCensus.FindAllStringSubmatch(diagnostic.String(), -1)
		if strings.Contains(diagnostic.String(), "fern-sanitizer:") || len(counts) != 1 || counts[0][1] != counts[0][2] || counts[0][3] != "0" {
			t.Fatalf("missing balanced ownership census\n%s", diagnostic.String())
		}
	}
}
