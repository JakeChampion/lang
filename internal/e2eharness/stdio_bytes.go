package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func WriteStdioByteFixture(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	lib, err := os.ReadFile("../../coreutils/lib/gnu.fern")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gnu.fern"), lib, 0o644); err != nil {
		t.Fatal(err)
	}
	source := `import "std/string";
import "./gnu";
function copy_bytes(data: u8[], lo: i32, hi: i32): u8[] {
  let h: usize = buf_new(hi - lo);
  buf_push_bytes_range(h, data, lo, hi);
  let bytes: u8[] = buf_take_bytes(h);
  buf_free(h);
  return bytes;
}
function main(): i32 {
  let argv: string[] = args();
  let mode: string = argv[1];
  let size: i32 = gnu.parse_number(argv[2]).0 as i32;
  if (mode == "closed-empty" || mode == "closed-flush") {
    stdout().close();
  }
  let out: gnu.Stdio = gnu.stdio_new();
  if (mode == "unicode") {
    out = out.fwrite("x".repeat(size) + "€🙂");
    out.close_stdout();
    return 0;
  }
  if (mode == "shared") {
    out = out.fwrite("seed");
    let held: gnu.Stdio = out;
    out = out.fwrite_bytes([255 as u8]);
    out = out.fflush();
    let fork: gnu.Stdio = held.fwrite("fork");
    fork = fork.fflush();
    fork.close_stdout();
    return 0;
  }
  if (mode == "closed-empty") {
    out = out.fwrite_bytes_range([255 as u8], 1, 0);
    out.close_stdout();
    return 0;
  }
  if (mode == "closed-flush") {
    out = out.fwrite_bytes([255 as u8]);
    out = out.fflush();
    out = out.fwrite_bytes([128 as u8]);
    out = out.fflush();
    match (out.err) {
      Some(e) => { eprint(gnu.io_error_text(e)); return 1; },
      None => { return 2; }
    }
  }
  let input: Reader = stdin();
  let h: usize = buf_new(0);
  while (true) {
    match (input.read_chunk_bytes(65536)) {
      Ok(chunk) => {
        if (chunk.len() == 0) { break; }
        buf_push_bytes_range(h, chunk, 0, chunk.len());
      },
      Err(_) => { buf_free(h); input.close(); return 3; }
    }
  }
  input.close();
  let data: u8[] = buf_take_bytes(h);
  buf_free(h);
  let at: i32 = 0;
  while (at < data.len()) {
    let end: i32 = at + size;
    if (end > data.len()) { end = data.len(); }
    if (mode == "range") {
      out = out.fwrite_bytes_range(data, at + 1, end - 1);
    } else {
      let piece: u8[] = copy_bytes(data, at, end);
      if (mode == "clamped") {
        out = out.fwrite_bytes_range(piece, -4, piece.len() + 4);
      } else {
        out = out.fwrite_bytes(piece);
      }
    }
    at = end;
  }
  out.close_stdout();
  return 0;
}
`
	path := filepath.Join(dir, "stdio-bytes.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func RunStdioByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	type testCase struct {
		mode, diagnostic string
		size, status     int
		data, want       []byte
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	cases := []testCase{
		{mode: "shared", want: []byte("seed\xffseedfork")},
		{mode: "closed-empty"},
		{mode: "closed-flush", status: 1, diagnostic: "Bad file descriptor\n"},
	}
	// Exercise both sides of the small-append/bulk-copy boundary with every byte.
	for _, size := range []int{63, 64, 65, 127, 128, 129} {
		cases = append(cases, testCase{mode: "bytes", size: size, data: all, want: all})
	}
	for _, n := range []int{0, 1, 255, 4095, 4096, 4097, 8191, 8192, 8193, 65535, 65536, 65537, 262145} {
		data := bytes.Repeat(all, (n+255)/256)[:n]
		for _, size := range []int{7, 4096, 4097, 65536} {
			for _, mode := range []string{"bytes", "range", "clamped"} {
				want := data
				if mode == "range" {
					want = nil
					for at := 0; at < len(data); at += size {
						end := min(at+size, len(data))
						if end-at > 2 {
							want = append(want, data[at+1:end-1]...)
						}
					}
				}
				cases = append(cases, testCase{mode: mode, size: size, data: data, want: want})
			}
		}
	}
	for _, n := range []int{0, 56, 57, 58, 120, 121, 122, 4094, 4095, 4096, 8190, 8191, 8192, 65535} {
		cases = append(cases, testCase{mode: "unicode", size: n, want: []byte(strings.Repeat("x", n) + "€🙂")})
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/input%d/chunk%d", tc.mode, len(tc.data), tc.size), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			argv := append(append([]string{}, runner...), bin, tc.mode, fmt.Sprint(tc.size))
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			// The fixture assembles input before selecting write ranges, so valid
			// short reads cannot change the operation being tested.
			path := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			input, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			cmd.Stdin = input
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err = cmd.Run()
			if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.status || !bytes.Equal(out.Bytes(), tc.want) {
				t.Fatalf("run: %v; got %d output bytes, want %d; stderr=%s", err, out.Len(), len(tc.want), diagnostic.Bytes())
			}
			if census != nil {
				census(t, diagnostic.String())
			}
			var clean strings.Builder
			for _, line := range strings.SplitAfter(diagnostic.String(), "\n") {
				if !strings.HasPrefix(line, "leakcheck:") && !strings.HasPrefix(line, "fern-sanitizer: leak ") {
					clean.WriteString(line)
				}
			}
			if clean.String() != tc.diagnostic {
				t.Fatalf("stderr=%q, want %q", clean.String(), tc.diagnostic)
			}
		})
	}
}
