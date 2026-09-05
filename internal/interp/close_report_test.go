package interp

import "testing"

// A close the kernel would refuse answers EBADF, not None (#8569).
//
// The interpreter never closes the host's own stdio — the streams outlive the
// program it is running — but it still has to answer a SECOND close the way
// every compiled backend does, because that is the whole of gnulib's
// close_stream contract and so of `prog >&-`'s exit status. Answering None
// twice also made the interpreter useless as the oracle for the compiled
// backends' close reporting, which is how this was found.
func TestCloseReportsBadFdOnTheSecondClose(t *testing.T) {
	src := `function code_of(o: Option[IoError]): i32 {
    match (o) {
        Some(e) => {
            match (e) {
                Other(c, msg) => { if (msg == "Bad file descriptor") { return 7; } return 5; },
                _ => { return 4; }
            }
        },
        None => { return 1; }
    }
}
function main(): i32 {
    var w: Writer = stdout();
    var first: i32 = code_of(w.close());
    var second: i32 = code_of(w.close());
    return first * 10 + second;
}`
	val, _ := runCapture(t, src)
	if val != "17" {
		t.Errorf("close reporting = %s, want 17 (first None, then Other with glibc's text); 11 = the failing close answered None", val)
	}
}

// The same rule for a file: closing it twice is EBADF, not an interpreter
// error. The interpreter drops its host handle on the first close, and a
// program that closes twice has done nothing illegal — the kernel simply says
// the descriptor is not open.
func TestCloseFileTwiceReportsBadFd(t *testing.T) {
	dir := t.TempDir()
	src := `function code_of(o: Option[IoError]): i32 {
    match (o) {
        Some(e) => {
            match (e) {
                Other(c, msg) => { if (msg == "Bad file descriptor") { return 7; } return 5; },
                _ => { return 4; }
            }
        },
        None => { return 1; }
    }
}
function main(): i32 {
    match (open_writer("` + dir + `/probe.txt")) {
        Ok(w) => { return code_of(w.close()) * 10 + code_of(w.close()); },
        Err(e) => { return 99; }
    }
}`
	val, _ := runCapture(t, src)
	if val != "17" {
		t.Errorf("close reporting = %s, want 17 (99 = the file would not open)", val)
	}
}
