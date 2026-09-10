package e2e

import (
	"fmt"
	"testing"
)

// Every path-taking fs helper NUL-terminates its path into a heap copy, and
// read_dir / remove_dir_all / temp_dir add working buffers of their own
// (a 1 MiB dirent buffer; a 1 KiB one plus a child path and a Result box
// per entry; a path scratch). Each helper frees what it allocated once the
// syscall has read it, so a loop over the bundle leaves the census exactly
// balanced (#9001).
//
// The paths are literals so that only the helpers' own memory is measured:
// a heap string handed to an fs helper is kept alive on x86-64 by the
// single-word retention rule (an IoError can name the path), and an
// IoError or FileStat box is immortal by design (rcresults.go), so the
// bundle takes only success paths and calls neither stat nor lstat.
func fsHelperBundleSrc(rounds int) string {
	return fmt.Sprintf(`function step(): i32 {
  match (create_dir_all("/tmp/fern_fs_scratch_gate/d/sub")) { Ok(_) => {}, Err(_) => { return 10; } }
  match (write_file("/tmp/fern_fs_scratch_gate/d/sub/f.txt", "hello")) { Ok(_) => {}, Err(_) => { return 11; } }
  match (read_file("/tmp/fern_fs_scratch_gate/d/sub/f.txt")) { Ok(s) => { if (s.len() != 5) { return 12; } }, Err(_) => { return 13; } }
  match (access("/tmp/fern_fs_scratch_gate/d/sub/f.txt", 4)) { Ok(_) => {}, Err(_) => { return 14; } }
  match (create_symlink("/tmp/fern_fs_scratch_gate/d/sub/f.txt", "/tmp/fern_fs_scratch_gate/d/lnk")) { Ok(_) => {}, Err(_) => { return 17; } }
  match (read_link("/tmp/fern_fs_scratch_gate/d/lnk")) { Ok(t) => { if (t.len() == 0) { return 20; } }, Err(_) => { return 21; } }
  match (create_link("/tmp/fern_fs_scratch_gate/d/sub/f.txt", "/tmp/fern_fs_scratch_gate/d/hard")) { Ok(_) => {}, Err(_) => { return 22; } }
  match (read_dir("/tmp/fern_fs_scratch_gate/d")) { Ok(names) => { if (names.len() != 3) { return 23; } }, Err(_) => { return 24; } }
  match (remove_file("/tmp/fern_fs_scratch_gate/d/lnk")) { Ok(_) => {}, Err(_) => { return 25; } }
  match (remove_file("/tmp/fern_fs_scratch_gate/d/hard")) { Ok(_) => {}, Err(_) => { return 26; } }
  match (remove_dir_all("/tmp/fern_fs_scratch_gate/d")) { Ok(_) => {}, Err(_) => { return 27; } }
  match (create_dir("/tmp/fern_fs_scratch_gate/d", 493)) { Ok(_) => {}, Err(_) => { return 28; } }
  match (remove_dir("/tmp/fern_fs_scratch_gate/d")) { Ok(_) => {}, Err(_) => { return 29; } }
  return 0;
}
function main(): i32 {
  var i: i32 = 0;
  while (i < %d) {
    var r: i32 = step();
    if (r != 0) { return r; }
    i = i + 1;
  }
  match (remove_dir_all("/tmp/fern_fs_scratch_gate")) { Ok(_) => {}, Err(_) => { return 8; } }
  return 0;
}`, rounds)
}

func checkFsHelperBundleCensus(t *testing.T, target string, rounds int, stderr string, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("%s %d rounds: exit %d, want 0; stderr: %s", target, rounds, exit, stderr)
	}
	a, f, live := parseLeakCheckLine(t, stderr)
	t.Logf("%s %d rounds: allocs=%d frees=%d live_bytes=%d", target, rounds, a, f, live)
	if a != f || live != 0 {
		t.Errorf("%s %d rounds: allocs=%d frees=%d live_bytes=%d, want a balanced census: "+
			"an fs helper strands its path copy or a working buffer", target, rounds, a, f, live)
	}
}

func TestX86_64FsHelpersFreeTheirScratch(t *testing.T) {
	for _, rounds := range []int{20, 40} {
		_, stderr, exit := runLeakCheckX86_64(t, fsHelperBundleSrc(rounds))
		checkFsHelperBundleCensus(t, "x86-64", rounds, stderr, exit)
	}
}

func TestArm64FsHelpersFreeTheirScratch(t *testing.T) {
	for _, rounds := range []int{20, 40} {
		_, stderr, exit := runLeakCheckArm64(t, fsHelperBundleSrc(rounds))
		checkFsHelperBundleCensus(t, "arm64", rounds, stderr, exit)
	}
}
