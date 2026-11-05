//go:build linux || darwin

package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriterSetTimesOracle(t *testing.T) {
	dir := t.TempDir()
	file, moved := filepath.Join(dir, "file"), filepath.Join(dir, "moved")
	source := fmt.Sprintf(`function main(): i32 {
      match (open_writer(%q)) {
        Err(_) => { return 1; },
        Ok(w) => {
          match (w.write("kept")) { Err(_) => { return 2; }, Ok(_) => {} }
          match (w.set_times(1650000000, 123456789, 1650000001, 987654321, 0)) { Err(_) => { return 3; }, Ok(_) => {} }
          match (rename(%q, %q)) { Err(_) => { return 4; }, Ok(_) => {} }
          match (w.set_times(1, 2, 1700000000, 333444555, 2)) { Err(_) => { return 5; }, Ok(_) => {} }
          match (w.stat()) {
            Err(_) => { return 6; },
            Ok(s) => {
              if (s.atime != 1650000000i64 || s.atime_nsec != 123456789i64 || s.mtime != 1700000000i64 || s.mtime_nsec != 333444555i64) { return 7; }
            }
          }
          unsafe { w.close_unchecked(); }
          match (w.set_times(1, 2, 1, 2, 0)) { Ok(_) => { return 8; }, Err(_) => {} }
        }
      }
      return 0;
    }`, file, file, moved)
	if got := evalProgramValue(t, source); got != Number(0) {
		t.Fatalf("program result=%v, want 0", got)
	}
	stat, err := os.Stat(moved)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(1700000000, 333444555); !stat.ModTime().Equal(want) {
		t.Fatalf("mtime=%v, want %v", stat.ModTime(), want)
	}
	if got, err := os.ReadFile(moved); err != nil || string(got) != "kept" {
		t.Fatalf("contents=%q, error=%v", got, err)
	}
}
