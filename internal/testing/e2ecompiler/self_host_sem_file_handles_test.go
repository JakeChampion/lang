package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func selfHostSemFileHandleSource(base string) string {
	return fmt.Sprintf(`function main(): i32 {
    match (open_writer(%[1]q + "/w.txt")) {
        Err(_) => { return 10; },
        Ok(w) => {
            match (w.write("first\n")) { Err(_) => { return 11; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 12; }, Ok(_) => {} }
        }
    }
    match (open_appender(%[1]q + "/w.txt")) {
        Err(_) => { return 20; },
        Ok(w) => {
            match (w.write("second\n")) { Err(_) => { return 21; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 22; }, Ok(_) => {} }
        }
    }
    match (open_exclusive(%[1]q + "/x.txt")) {
        Err(_) => { return 30; },
        Ok(w) => {
            match (w.write("only\n")) { Err(_) => { return 31; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 32; }, Ok(_) => {} }
        }
    }
    match (open_exclusive(%[1]q + "/x.txt")) {
        Ok(_) => { return 33; },
        Err(_) => {}
    }
    match (open_writer_with(%[1]q + "/f.txt", 1)) {
        Err(_) => { return 40; },
        Ok(w) => {
            match (w.write("flagged\n")) { Err(_) => { return 41; }, Ok(_) => {} }
            match (w.close()) { Err(_) => { return 42; }, Ok(_) => {} }
        }
    }
    match (open_reader(%[1]q + "/w.txt")) {
        Err(_) => { return 50; },
        Ok(r) => {
            match (r.read_chunk(4096)) {
                Err(_) => { return 51; },
                Ok(s) => { if (s != "first\nsecond\n") { return 52; } }
            }
            match (r.close()) { Err(_) => { return 53; }, Ok(_) => {} }
        }
    }
    match (open_reader_with(%[1]q + "/f.txt", 0)) {
        Err(_) => { return 60; },
        Ok(r) => {
            match (r.read_chunk(4096)) {
                Err(_) => { return 61; },
                Ok(line) => { if (line != "flagged\n") { return 62; } }
            }
            match (r.close()) { Err(_) => { return 63; }, Ok(_) => {} }
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

func TestSelfHostSemanticFileHandles(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the handle program opens host paths, so it runs only natively")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	base := t.TempDir()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostSemFileHandleSource(base)), 0o644); err != nil {
		t.Fatal(err)
	}
	got, report, _ := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, "x86-64-linux", "")
	if got != "0|" {
		t.Fatalf("answered %q, want %q\nreport: %s", got, "0|", report)
	}
	if n := semProducedCount(t, report); n < 1 {
		t.Fatalf("produced %d declarations:\n%s", n, report)
	}
}
