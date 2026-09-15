package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
)

// The Reader/Writer handle family on `-backend ssa -target x86-64-linux`.
// Until these landed the backend had no handle surface at all, so any program
// that opened a file or wrote to a stream was refused outright — which is what
// kept coreutils/sort.fern from building under it (#8822).
//
// Each case asserts against the DEFAULT x86-64 backend rather than against a
// literal: the helpers exist to behave the way the shipping emitter does, and
// a hand-written expectation would only pin what this file's author believed.
// The comparison is on stdout, stderr and exit status together, since the fd a
// write reaches is exactly the kind of thing a wrong helper gets subtly wrong
// — an early version of `stdout` stored the heap pointer where the fd belonged
// and silently wrote nothing while still exiting 0.
var x86SSAHandleCases = []struct {
	name string
	src  string
}{
	{
		name: "std_streams_reach_their_descriptors",
		src: `function main(): i32 {
  stdout().write("out-line\n");
  stderr().write("err-line\n");
  return 7;
}`,
	},
	{
		name: "exit_carries_its_status",
		src: `function main(): i32 {
  stdout().write("before\n");
  exit(42);
  return 0;
}`,
	},
	{
		// Also the only cover for open_reader's SUCCESS path: the missing-path
		// case below exercises only the Err arm, and a helper that never
		// returned Ok would pass that one.
		name: "open_writer_creates_a_file_open_reader_finds",
		src: `function main(): i32 {
  match (open_writer("handles_out.txt")) {
    Ok(w) => {
      w.write("written\n");
      w.close();
    },
    Err(e) => {
      return 1;
    },
  }
  match (open_reader("handles_out.txt")) {
    Ok(r) => {
      r.close();
      stdout().write("reopened\n");
      return 0;
    },
    Err(e) => {
      return 2;
    },
  }
}`,
	},
	{
		name: "open_reader_missing_path_is_not_found",
		src: `function main(): i32 {
  match (open_reader("no_such_file_here.txt")) {
    Ok(f) => {
      return 1;
    },
    Err(e) => {
      match (e) {
        NotFound(p) => {
          stdout().write("NotFound:" + p + "\n");
          return 0;
        },
        _ => {
          stdout().write("other\n");
          return 2;
        },
      }
    },
  }
}`,
	},
	{
		name: "read_chunk_reads_then_reports_eof",
		src: `function main(): i32 {
  match (open_writer("rc_in.txt")) {
    Ok(w) => {
      w.write("0123456789abcdef");
      w.close();
    },
    Err(e) => {
      return 1;
    },
  }
  match (open_reader("rc_in.txt")) {
    Ok(r) => {
      var n1: i32 = 0;
      match (r.read_chunk(65536)) {
        Ok(s) => {
          stdout().write("got:" + s + "\n");
          n1 = s.len();
        },
        Err(e) => {
          return 2;
        },
      }
      if (n1 != 16) {
        return 3;
      }
      match (r.read_chunk(65536)) {
        Ok(s2) => {
          if (s2.len() != 0) {
            return 4;
          }
          stdout().write("eof-empty\n");
        },
        Err(e) => {
          return 5;
        },
      }
      r.close();
      return 0;
    },
    Err(e) => {
      return 6;
    },
  }
}`,
	},
	{
		name: "writer_close_reports_a_bad_descriptor",
		src: `function main(): i32 {
  match (open_writer("close_twice.txt")) {
    Ok(w) => {
      w.close();
      match (w.close()) {
        Some(e) => {
          stdout().write("second close failed\n");
          return 0;
        },
        None => {
          stdout().write("second close succeeded\n");
          return 0;
        },
      }
    },
    Err(e) => {
      return 1;
    },
  }
}`,
	},
}

func TestX86_64SSAHandleFamilyMatchesDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSAHandleCases {
		t.Run(c.name, func(t *testing.T) {
			ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, c.src, true)
			flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, c.src, false)

			if ssaCode != flatCode {
				t.Errorf("exit status: ssa=%d flat=%d", ssaCode, flatCode)
			}
			if ssaOut != flatOut {
				t.Errorf("stdout differs:\n ssa=%q\nflat=%q", ssaOut, flatOut)
			}
			if ssaErr != flatErr {
				t.Errorf("stderr differs:\n ssa=%q\nflat=%q", ssaErr, flatErr)
			}
		})
	}
}

// read_chunk sizes its buffer from the REQUEST and only learns what it owns
// after the read, so it hands the rest back by lowering the heap cursor. At end
// of input it owns nothing at all, and without that rewind every probe strands
// a full 64 KiB block.
//
// The observable is ARENA EXHAUSTION, not peak RSS. RSS does not move: the
// reservation is MAP_NORESERVE and a stranded block is never written, so the
// cursor runs away while the resident set stays in single-digit megabytes — an
// RSS bound passes just as happily on the leaking helper, which is how the
// first version of this test managed to be worthless. The cursor is what moves,
// and this backend has no __heap_bump_bytes() for a program to read it with
// (the gate the arm64 sibling uses), so the test drives it into the wall
// instead: 300,000 probes at 64 KiB is 19 GiB against a 16 GiB arena.
//
// Verified in both directions before being committed — as it stands the program
// exits 0, and with the end-of-input rewind removed it exits
// ExitArenaExhausted with `fern: out of memory (heap arena exhausted)`.
func TestX86_64SSAReadChunkKeepsOnlyWhatItRead(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
  match (open_writer("rs.txt")) {
    Ok(w) => { w.write("abc"); w.close(); },
    Err(e) => { return 1; },
  }
  match (open_reader("rs.txt")) {
    Ok(r) => {
      var i: i32 = 0;
      while (i < 300000) {
        match (r.read_chunk(65536)) {
          Ok(s) => { },
          Err(e) => { return 5; },
        }
        i = i + 1;
      }
      r.close();
      return 0;
    },
    Err(e) => { return 6; },
  }
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-backend", "ssa", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}

	cmd := runX86Bin(runner, bin)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatal("probe did not start")
	}
	switch code := cmd.ProcessState.ExitCode(); code {
	case 0:
	case nativex86_64.ExitArenaExhausted:
		t.Fatalf("exit=%d: read_chunk stranded its buffer on every end-of-input probe and ran the arena out\n%s", code, out)
	default:
		t.Fatalf("exit=%d, want 0 (1/5/6 = the program's own fixture checks)\n%s", code, out)
	}
}

// buildRunX86 compiles src for x86-64 — through the SSA backend when ssa is
// set, otherwise the default emitter — and runs it in a fresh directory, so a
// case that writes a fixture cannot see the other build's copy of it.
func buildRunX86(t *testing.T, fern, runner, src string, ssa bool) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src+"\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "prog")
	args := []string{"-target", "x86-64-linux"}
	if ssa {
		args = append(args, "-backend", "ssa")
	}
	args = append(args, "-o", bin, srcPath)
	if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile (ssa=%v): %v\n%s", ssa, err, out)
	}

	cmd := runX86Bin(runner, bin)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("run (ssa=%v): process did not start", ssa)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}
