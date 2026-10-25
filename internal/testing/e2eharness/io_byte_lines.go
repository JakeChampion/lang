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
    match (stdout().write_bytes(line)) { Err(_) => { return 1; }, Ok(_) => {} }
    write("\n");
    return 0;
}
`

func IOByteLinesProgram(term, chunkSize int) string {
	return strings.NewReplacer("TERM", strconv.Itoa(term), "CHUNK", strconv.Itoa(chunkSize)).Replace(ioByteLinePrelude + `function main(): i32 {
    let r: Reader = stdin();
    let lr: io.ByteLineReader = io.byte_line_reader_new(r, TERM, CHUNK);
    let held: u8[][] = [];
    while (true) {
        let next: (Option[u8[]], io.ByteLineReader) = (None, lr);
        match (lr.next_line_bytes()) { Ok(read) => { next = read; }, Err(_) => { return 2; } }
        lr = next.1;
        match (next.0) {
            Some(line) => {
                held = held.append(line);
                if (emit(line) != 0) { return 1; }
            },
            None => { break; },
        }
    }
    match (lr.next_line_bytes()) {
        Ok(again) => { match (again.0) { Some(_) => { return 3; }, None => {} } },
        Err(_) => { return 2; }
    }
    match (r.close()) { Err(_) => { return 4; }, Ok(_) => {} }
    let i: i32 = 0;
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
    let r: Reader = stdin();
    let lr: io.ByteLineReader = io.byte_line_reader_new(r, 10, 4);
    let held: u8[][] = [];
    let step: i32 = 0;
    while (step < 4) {
        let read: Result[(Option[u8[]], io.ByteLineReader), IoError] = Ok((None, lr));
        if (step == 0 || step == 2) { read = lr.next_line_bytes(); }
        else { read = lr.next_chunk_bytes(); }
        let next: (Option[u8[]], io.ByteLineReader) = (None, lr);
        match (read) { Ok(got) => { next = got; }, Err(_) => { return 4; } }
        lr = next.1;
        match (next.0) {
            Some(bytes) => { held = held.append(bytes); if (emit(bytes) != 0) { return 1; } },
            None => { return 2; },
        }
        step = step + 1;
    }
    match (lr.next_chunk_bytes()) {
        Ok(end) => { match (end.0) { Some(_) => { return 3; }, None => {} } },
        Err(_) => { return 4; }
    }
    match (r.close()) { Err(_) => { return 5; }, Ok(_) => {} }
    let i: i32 = 0;
    while (i < held.len()) { if (emit(held[i]) != 0) { return 6; } i = i + 1; }
    return 0;
}`, Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
		checkIOByteLineOutput(t, command(), []byte{'a', '\n', 255, 0, 128, 'b', '\n', 'c'},
			bytes.Repeat([]byte("2:a\n\n2:\xff\x00\n3:\x80b\n\n1:c\n"), 2), census)
	}})
	cases = append(cases, IOByteLineCase{Name: "skip", Source: ioByteLinePrelude + `function main(): i32 {
    let r: Reader = stdin();
    let lr: io.ByteLineReader = io.byte_line_reader_new(r, 10, 4);
    let n: i32 = 0;
    while (true) {
        if (n % 2 == 1) {
            let sk: (boolean, io.ByteLineReader) = (false, lr);
            match (lr.skip_line()) { Ok(skipped) => { sk = skipped; }, Err(_) => { return 3; } }
            lr = sk.1;
            if (!sk.0) { break; }
            write("skip\n");
        } else {
            let next: (Option[u8[]], io.ByteLineReader) = (None, lr);
            match (lr.next_line_bytes()) { Ok(read) => { next = read; }, Err(_) => { return 3; } }
            lr = next.1;
            match (next.0) {
                Some(line) => { if (emit(line) != 0) { return 1; } },
                None => { break; },
            }
        }
        n = n + 1;
    }
    match (lr.skip_line()) {
        Ok(again) => { if (again.0) { return 2; } },
        Err(_) => { return 3; }
    }
    match (r.close()) { Err(_) => { return 4; }, Ok(_) => {} }
    return 0;
}`, Check: func(t *testing.T, command func() *exec.Cmd, census bool) {
		checkIOByteLineOutput(t, command(), []byte("a\nbbbbbbbbb\nccc\ndddddddddd"),
			[]byte("2:a\n\nskip\n4:ccc\n\nskip\n"), census)
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
		setup = "match (r.close()) { Err(_) => { return 1; }, Ok(_) => {} }"
		closeAtEnd = "false"
	}
	return strings.NewReplacer("SETUP", setup, "CHUNK", strconv.Itoa(chunk), "LENGTH", strconv.Itoa(length),
		"VALUE", strconv.Itoa(value), "CLOSE", closeAtEnd).Replace(`import "std/io_buffered" as io;
function read_failed(r: Result[(Option[u8[]], io.ByteLineReader), IoError]): boolean {
    match (r) { Err(Other(_, _, _)) => { return true; }, _ => { return false; } }
    return false;
}
function main(): i32 {
    let r: Reader = stdin();
    SETUP
    let lr: io.ByteLineReader = io.byte_line_reader_new(r, 10, CHUNK);
    let first: Result[(Option[u8[]], io.ByteLineReader), IoError] = lr.next_line_bytes();
    if (LENGTH == 0) {
        if (!read_failed(first)) { return 2; }
    } else {
        match (first) {
            Ok(got) => {
                lr = got.1;
                match (got.0) {
                    Some(bytes) => {
                        if (bytes.len() != LENGTH) { return 3; }
                        let i: i32 = 0;
                        while (i < bytes.len()) { if (bytes[i] as i32 != VALUE) { return 4; } i = i + 1; }
                    },
                    None => { return 5; },
                }
            },
            Err(_) => { return 6; }
        }
        // The failure behind the partial record is answered next, without another read.
        if (!read_failed(lr.next_chunk_bytes())) { return 7; }
        if (!read_failed(lr.next_line_bytes())) { return 8; }
    }
    if (CLOSE) { match (r.close()) { Err(_) => { return 10; }, Ok(_) => {} } }
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
