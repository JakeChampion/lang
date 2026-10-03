package e2eharness

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Closing native/P1 stdin makes a subsequent text read fail. P2 can open a
// new stdin stream resource, so it uses the same-handle raw-reader tests.
const IOTextReadErrorProgram = `import "std/io";
function main(): i32 {
    let r: Reader = stdin();
    match (r.close()) { Some(_) => { return 1; }, None => {} }
    match (io.read_all_stdin()) {
        Ok(_) => { return 2; },
        Err(e) => { match (e) { InvalidUtf8(_) => { return 3; }, _ => { return 0; } } },
    }
}
`

func IOTextProgram(call string) string {
	return strings.ReplaceAll(`import "std/io";
function main(): i32 {
    match (CALL) {
        Ok(text) => { write(text); return 0; },
        Err(e) => {
            match (e) {
                InvalidUtf8(path) => { if (path != "stdin") { return 66; } eprint("invalid-utf8:stdin"); return 65; },
                _ => { return 74; },
            }
        },
    }
}
`, "CALL", call)
}

func CheckIOText(t *testing.T, command func() *exec.Cmd, census bool, invalidExit int) {
	t.Helper()
	cases := []struct {
		name  string
		input []byte
		code  int
	}{
		{"empty", nil, 0},
		{"nul", []byte("a\x00b"), 0},
		{"unicode", []byte("é界𐐀\n"), 0},
		{"continuation", []byte{0x80}, 65},
		{"overlong", []byte{0xc0, 0x80}, 65},
		{"surrogate", []byte{0xed, 0xa0, 0x80}, 65},
		{"out of range", []byte{0xf4, 0x90, 0x80, 0x80}, 65},
		{"truncated", []byte{0xf0, 0x9f, 0x99}, 65},
		{"invalid middle", []byte("before\xffafter"), 65},
		{"truncated final chunk", append(bytes.Repeat([]byte{'a'}, 4095), 0xe2, 0x82), 65},
	}
	for _, scalar := range []string{"é", "界", "𐐀"} {
		for split := 1; split < len(scalar); split++ {
			input := append(bytes.Repeat([]byte{'a'}, 4096-split), []byte(scalar+"tail")...)
			cases = append(cases, struct {
				name  string
				input []byte
				code  int
			}{"split " + scalar + " at " + strconv.Itoa(split), input, 0})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := command()
			cmd.Stdin = bytes.NewReader(tc.input)
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err := cmd.Run()
			if tc.code == 0 {
				if err != nil {
					t.Fatalf("run: %v\n%s", err, diagnostic.String())
				}
				if !bytes.Equal(out.Bytes(), tc.input) {
					t.Fatalf("valid text changed: got %d bytes, want %d", out.Len(), len(tc.input))
				}
			} else {
				// WASI P2 CLI maps its failure result to exit1. The marker
				// proves the exact error arm still ran on that target.
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != invalidExit {
					t.Fatalf("run: %v, want exit %d\n%s", err, invalidExit, diagnostic.String())
				}
				if !strings.Contains(diagnostic.String(), "invalid-utf8:stdin") {
					t.Fatalf("wrong error variant\n%s", diagnostic.String())
				}
				if out.Len() != 0 {
					t.Fatalf("invalid input produced text: %q", out.Bytes())
				}
			}
			if census && (strings.Contains(diagnostic.String(), "fern-sanitizer:") || !strings.Contains(diagnostic.String(), "live_bytes=0")) {
				t.Fatalf("missing clean ownership census\n%s", diagnostic.String())
			}
		})
	}
}
