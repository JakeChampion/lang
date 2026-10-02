package e2eharness

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const ioByteLinePrelude = `import "std/io_buffered" as io;
import "std/i32";
function emit(line: u8[]): i32 {
    write(line.len().to_string() + ":");
    match (stdout().write_bytes(line)) { Some(_) => { return 1; }, None => {} }
    write("\n");
    return 0;
}
`

func IOByteLinesProgram(term, chunkSize int) string {
	return strings.NewReplacer("TERM", strconv.Itoa(term), "CHUNK", strconv.Itoa(chunkSize)).Replace(ioByteLinePrelude + `function main(): i32 {
    var r: Reader = stdin();
    var lr: io.ByteLineReader = io.byte_line_reader_new(r, TERM, CHUNK);
    var held: u8[][] = [];
    while (true) {
        var next: (Option[u8[]], io.ByteLineReader) = lr.next_line_bytes();
        lr = next.1;
        match (next.0) {
            Some(line) => {
                held = held.append(line);
                if (emit(line) != 0) { return 1; }
            },
            None => { break; },
        }
    }
    match (lr.error()) { Some(_) => { return 2; }, None => {} }
    var again: (Option[u8[]], io.ByteLineReader) = lr.next_line_bytes();
    match (again.0) { Some(_) => { return 3; }, None => {} }
    match (r.close()) { Some(_) => { return 4; }, None => {} }
    var i: i32 = 0;
    while (i < held.len()) {
        if (emit(held[i]) != 0) { return 5; }
        i = i + 1;
    }
    return 0;
}
`)
}

type IOByteLineCase struct {
	Name, Source string
	Check        func(*testing.T, func() *exec.Cmd, bool)
}

func IOByteLineCases() []IOByteLineCase {
	var cases []IOByteLineCase
	for _, config := range []struct{ term, chunk int }{{10, 1}, {10, 7}, {10, 4096}, {0, 7}, {128, 7}, {255, 7}} {
		cases = append(cases, IOByteLineCase{
			Name: fmt.Sprintf("%d/%d", config.term, config.chunk), Source: IOByteLinesProgram(config.term, config.chunk),
			Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
				CheckIOByteLines(t, command, byte(config.term), census)
			},
		})
	}
	cases = append(cases, IOByteLineCase{Name: "transitions", Source: ioByteLinePrelude + `function main(): i32 {
    var r: Reader = stdin();
    var lr: io.ByteLineReader = io.byte_line_reader_new(r, 10, 4);
    var held: u8[][] = [];
    var step: i32 = 0;
    while (step < 4) {
        var next: (Option[u8[]], io.ByteLineReader) = (None, lr);
        if (step == 0 || step == 2) { next = lr.next_line_bytes(); }
        else { next = lr.next_chunk_bytes(); }
        lr = next.1;
        match (next.0) {
            Some(bytes) => { held = held.append(bytes); if (emit(bytes) != 0) { return 1; } },
            None => { return 2; },
        }
        step = step + 1;
    }
    var end: (Option[u8[]], io.ByteLineReader) = lr.next_chunk_bytes();
    lr = end.1;
    match (end.0) { Some(_) => { return 3; }, None => {} }
    match (lr.error()) { Some(_) => { return 4; }, None => {} }
    match (r.close()) { Some(_) => { return 5; }, None => {} }
    var i: i32 = 0;
    while (i < held.len()) { if (emit(held[i]) != 0) { return 6; } i = i + 1; }
    return 0;
}`, Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
		checkIOByteLineOutput(t, command(), []byte{'a', '\n', 255, 0, 128, 'b', '\n', 'c'},
			bytes.Repeat([]byte("2:a\n\n2:\xff\x00\n3:\x80b\n\n1:c\n"), 2), census)
	}})
	for _, closed := range []bool{false, true} {
		name, chunk := "negative size", -1
		if closed {
			name, chunk = "closed reader", 7
		}
		cases = append(cases, IOByteLineCase{Name: name, Source: IOByteLineReadErrorProgram(closed, chunk, 0, 0),
			Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
				checkIOByteLineOutput(t, command(), nil, nil, census)
			},
		})
	}
	return cases
}

func IOByteLineReadErrorProgram(closed bool, chunk, length, value int) string {
	setup, closeAtEnd := "", "true"
	if closed {
		setup = "match (r.close()) { Some(_) => { return 1; }, None => {} }"
		closeAtEnd = "false"
	}
	return strings.NewReplacer("SETUP", setup, "CHUNK", strconv.Itoa(chunk), "LENGTH", strconv.Itoa(length),
		"VALUE", strconv.Itoa(value), "CLOSE", closeAtEnd).Replace(`import "std/io_buffered" as io;
function main(): i32 {
    var r: Reader = stdin();
    SETUP
    var lr: io.ByteLineReader = io.byte_line_reader_new(r, 10, CHUNK);
    var first: (Option[u8[]], io.ByteLineReader) = lr.next_line_bytes();
    lr = first.1;
    match (first.0) {
        Some(bytes) => {
            if (LENGTH == 0 || bytes.len() != LENGTH) { return 2; }
            var i: i32 = 0;
            while (i < bytes.len()) { if (bytes[i] as i32 != VALUE) { return 3; } i = i + 1; }
        },
        None => { if (LENGTH != 0) { return 4; } },
    }
    match (lr.error()) {
        Some(e) => { match (e) { Other(_, _) => {}, _ => { return 5; } } },
        None => { return 6; },
    }
    var again: (Option[u8[]], io.ByteLineReader) = lr.next_chunk_bytes();
    lr = again.1;
    match (again.0) { Some(_) => { return 7; }, None => {} }
    var last: (Option[u8[]], io.ByteLineReader) = lr.next_line_bytes();
    match (last.0) { Some(_) => { return 8; }, None => {} }
    match (last.1.error()) { Some(_) => {}, None => { return 9; } }
    if (CLOSE) { match (r.close()) { Some(_) => { return 10; }, None => {} } }
    return 0;
}`)
}

func CheckIOByteLines(t *testing.T, command func() *exec.Cmd, term byte, census bool) {
	t.Helper()
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	cases := []struct {
		name  string
		input []byte
	}{
		{"empty", nil},
		{"empty records", []byte{term, term}},
		{"unterminated", []byte("tail")},
		{"binary", all},
		{"unicode", []byte("é界𐐀\nlast")},
		{"long record", append(bytes.Repeat([]byte{'x'}, 8193), term)},
		{"long tail", bytes.Repeat([]byte{0xfe}, 8193)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var framed bytes.Buffer
			for _, line := range bytes.SplitAfter(tc.input, []byte{term}) {
				if len(line) == 0 {
					continue
				}
				fmt.Fprintf(&framed, "%d:", len(line))
				framed.Write(line)
				framed.WriteByte('\n')
			}
			want := bytes.Repeat(framed.Bytes(), 2)
			checkIOByteLineOutput(t, command(), tc.input, want, census)
		})
	}
}

func checkIOByteLineOutput(t *testing.T, cmd *exec.Cmd, input, want []byte, census bool) {
	t.Helper()
	cmd.Stdin = bytes.NewReader(input)
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, diagnostic.String())
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("records changed: got %d bytes, want %d", out.Len(), len(want))
	}
	if census && (strings.Contains(diagnostic.String(), "fern-sanitizer:") || !strings.Contains(diagnostic.String(), "live_bytes=0")) {
		t.Fatalf("missing clean ownership census\n%s", diagnostic.String())
	}
}
