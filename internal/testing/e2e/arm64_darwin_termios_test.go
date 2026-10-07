package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tools/tty"
)

// TestArm64DarwinTermios checks the arm64-darwin runtime's TIOCGETA,
// TIOCSETAW and TIOCSWINSZ against internal/tools/tty on one pseudo-terminal:
// the words the compiled program reads are the words Go reads, and what it
// writes back — ECHO cleared, both speeds 300, a 33x77 window — is what Go
// finds afterwards. Off Apple Silicon the build is what is checked.
func TestArm64DarwinTermios(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "termios.fern")
	prog := `import "std/i64";

function main(): i32 {
  match (termios_get(0)) {
    Err(_) => { return 10; },
    Ok(t) => {
      let line: string = "";
      let i: i32 = 0;
      while (i < t.len()) {
        line = line + t[i].to_string() + " ";
        i = i + 1;
      }
      print(line);
      let off: i64[] = t.with(3, t[3] & (0 - 1 - 8)).with(24, 300 as i64).with(25, 300 as i64);
      match (termios_set(0, 1, off)) { Err(_) => { return 11; }, Ok(_) => {} }
      match (termios_set(0, 1, [1 as i64])) { Ok(_) => { return 12; }, Err(_) => {} }
      match (set_window_size(0, 33, 77)) { Err(_) => { return 13; }, Ok(_) => {} }
      return 0;
    }
  }
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "termios")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	fd := int(slave.Fd())
	before, err := tty.Termios(fd)
	if err != nil {
		t.Fatalf("Termios: %v", err)
	}
	cmd := exec.Command(out)
	cmd.Stdin = slave
	o, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, o)
	}
	var want []string
	for _, w := range before {
		want = append(want, strconv.FormatInt(w, 10))
	}
	if got := strings.TrimSpace(string(o)); got != strings.Join(want, " ") {
		t.Errorf("termios_get read\n  %s\nGo read\n  %s", got, strings.Join(want, " "))
	}
	after, err := tty.Termios(fd)
	if err != nil {
		t.Fatalf("Termios after the run: %v", err)
	}
	if after[3] != before[3]&^8 || after[24] != 300 || after[25] != 300 {
		t.Errorf("after termios_set: lflag %#x speeds %d/%d, want %#x and 300/300",
			after[3], after[24], after[25], before[3]&^8)
	}
	if rows, cols, err := tty.WindowSize(fd); err != nil || rows != 33 || cols != 77 {
		t.Errorf("after set_window_size: %dx%d (%v), want 33x77", rows, cols, err)
	}
}
