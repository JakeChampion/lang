package e2eharness

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
)

const ReadLineTextProgram = `import "std/io";

function emit_length(n: i32): void {
  if (n >= 10) { emit_length(n / 10); }
  putchar(48 + n % 10);
}
function emit(s: string): void {
  emit_length(s.len());
  putchar(10);
  write(s);
}
function main(): i32 {
  let r = stdin();
  let held: string = "";
  let count: i32 = 0;
  let done: boolean = false;
  while (!done) {
    match (io.read_line(r)) {
      Ok(Some(s)) => {
        if (count == 0) { held = s; }
        emit(s);
        count = count + 1;
      },
      Ok(None()) => { done = true; },
      Err(_) => { return 6; }
    }
  }
  match (io.read_line(r)) { Ok(None()) => {}, _ => { return 1; } }
  match (r.close()) { Err(_) => { return 3; }, Ok(_) => {} }
  match (io.read_line(r)) { Err(_) => {}, _ => { return 4; } }
  print("retained");
  emit(held);
  return 0;
}
`

func CheckReadLineText(t *testing.T, command func() *exec.Cmd, census func(*testing.T, string)) {
	t.Helper()
	cases := []struct{ name, input string }{
		{"empty", ""},
		{"small and unterminated", "first\x00é\nsecond🙂\n\nunterminated€"},
		{"long ASCII", strings.Repeat("a", 32768) + "\nnext\n"},
		{"long Unicode", strings.Repeat("aé中🙂", 4097) + "\nnext\n"},
	}
	for _, limit := range []int{256, 4096} {
		for _, n := range []int{limit - 1, limit, limit + 1} {
			cases = append(cases, struct{ name, input string }{fmt.Sprintf("ASCII/%d", n), strings.Repeat("a", n) + "\nnext\n"})
		}
		for _, scalar := range []string{"¢", "€", "🙂"} {
			for split := 1; split < len(scalar); split++ {
				line := strings.Repeat("a", limit-split) + scalar + "\n"
				cases = append(cases, struct{ name, input string }{
					fmt.Sprintf("scalar/%d/%d/%d", limit, len(scalar), split), line + "second\n" + line + "tail\x00é",
				})
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !utf8.ValidString(tc.input) {
				t.Fatal("fixture must be valid UTF-8")
			}
			var want bytes.Buffer
			first := ""
			for i, line := range strings.SplitAfter(tc.input, "\n") {
				if line == "" {
					continue
				}
				if i == 0 {
					first = line
				}
				fmt.Fprintf(&want, "%d\n%s", len(line), line)
			}
			fmt.Fprintf(&want, "retained\n%d\n%s", len(first), first)
			cmd := command()
			cmd.Stdin = strings.NewReader(tc.input)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("io.read_line: %v\n%s", err, stderr.Bytes())
			}
			if !bytes.Equal(stdout.Bytes(), want.Bytes()) {
				t.Fatalf("complete line frames differ: got %d bytes, want %d; output valid UTF-8=%v", stdout.Len(), want.Len(), utf8.Valid(stdout.Bytes()))
			}
			if census != nil {
				census(t, stderr.String())
			}
		})
	}
}
