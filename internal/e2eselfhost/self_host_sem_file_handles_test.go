package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostSemFileHandleSource opens a file four ways — the three flag words
// `open_file` carries and the flags-word form — writes through each Writer,
// reads it back through a Reader, and reports its own failures by exit code.
// `open_exclusive` is asked twice on purpose: the second must refuse, which is
// the only evidence that the O_EXCL bit reached the syscall rather than the
// truncating word.
func selfHostSemFileHandleSource(base string) string {
	return fmt.Sprintf(`function main(): i32 {
    match (open_writer(%[1]q + "/w.txt")) {
        Err(_) => { return 10; },
        Ok(w) => {
            match (w.write("first\n")) { Some(_) => { return 11; }, None => {} }
            match (w.close()) { Some(_) => { return 12; }, None => {} }
        }
    }
    match (open_appender(%[1]q + "/w.txt")) {
        Err(_) => { return 20; },
        Ok(w) => {
            match (w.write("second\n")) { Some(_) => { return 21; }, None => {} }
            match (w.close()) { Some(_) => { return 22; }, None => {} }
        }
    }
    match (open_exclusive(%[1]q + "/x.txt")) {
        Err(_) => { return 30; },
        Ok(w) => {
            match (w.write("only\n")) { Some(_) => { return 31; }, None => {} }
            match (w.close()) { Some(_) => { return 32; }, None => {} }
        }
    }
    match (open_exclusive(%[1]q + "/x.txt")) {
        Ok(_) => { return 33; },
        Err(_) => {}
    }
    match (open_writer_with(%[1]q + "/f.txt", 1)) {
        Err(_) => { return 40; },
        Ok(w) => {
            match (w.write("flagged\n")) { Some(_) => { return 41; }, None => {} }
            match (w.close()) { Some(_) => { return 42; }, None => {} }
        }
    }
    match (open_reader(%[1]q + "/w.txt")) {
        Err(_) => { return 50; },
        Ok(r) => {
            match (r.read_chunk(4096)) {
                Err(_) => { return 51; },
                Ok(s) => { if (s != "first\nsecond\n") { return 52; } }
            }
            match (r.close()) { Some(_) => { return 53; }, None => {} }
        }
    }
    match (open_reader_with(%[1]q + "/f.txt", 0)) {
        Err(_) => { return 60; },
        Ok(r) => {
            match (r.read_line()) {
                None => { return 61; },
                Some(line) => { if (line != "flagged\n") { return 62; } }
            }
            match (r.close()) { Some(_) => { return 63; }, None => {} }
        }
    }
    match (open_reader(%[1]q + "/nope.txt")) {
        Ok(_) => { return 70; },
        Err(_) => {}
    }
    return 0;
}
`, base)
}

// TestSelfHostSemanticFileHandles drives the file half of the stream-handle
// family through the PRODUCTION consumer of the typed semantic pipeline. The
// stdin half is in semProductionPrograms, where it runs on all four targets;
// this one opens host paths, so it runs only where the binary runs natively.
//
// Both halves assert the same two things: the module produces WHOLE (a single
// refused declaration takes the program to the AST lowering, which is #9781's
// subject), and the produced program answers what the AST lowering answers.
func TestSelfHostSemanticFileHandles(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the handle program opens host paths, so it runs only natively")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, sem := range []bool{false, true} {
		// A fresh tree per leg: open_exclusive's second call must refuse
		// because the FIRST one created the file, not because the other leg
		// left it behind.
		base := t.TempDir()
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(selfHostSemFileHandleSource(base)), 0o644); err != nil {
			t.Fatal(err)
		}
		got, report, _ := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, "x86-64-linux", sem, "", "")
		if !sem {
			if got != "0|" {
				t.Fatalf("the AST lowering answered %q, want %q", got, "0|")
			}
			continue
		}
		if got != "0|" {
			t.Fatalf("FERN_SEM_IR answered %q, want %q\nreport: %s", got, "0|", report)
		}
		if strings.Contains(report, "the AST lowering stands") {
			t.Fatalf("the module did not produce whole:\n%s", report)
		}
		if n := semProducedCount(t, report); n < 1 {
			t.Fatalf("produced %d declarations:\n%s", n, report)
		}
	}
}
