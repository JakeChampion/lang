package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
