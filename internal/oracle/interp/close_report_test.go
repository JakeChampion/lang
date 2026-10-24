package interp

import "testing"

func TestCloseReportsBadFdOnTheSecondClose(t *testing.T) {
	src := `function code_of(o: Result[void, IoError]): i32 {
    match (o) {
        Err(e) => {
            match (e) {
                Other(c, msg, _) => { if (msg == "Bad file descriptor") { return 7; } return 5; },
                _ => { return 4; }
            }
        },
        Ok(_) => { return 1; }
    }
}
function main(): i32 {
    let w: Writer = stdout();
    let first: i32 = code_of(w.close());
    let second: i32 = code_of(w.close());
    return first * 10 + second;
}`
	val, _ := runCapture(t, src)
	if val != "17" {
		t.Errorf("close reporting = %s, want 17 (first Ok, then Other with glibc's text); 11 = the failing close answered Ok", val)
	}
}

func TestCloseFileTwiceReportsBadFd(t *testing.T) {
	dir := t.TempDir()
	src := `function code_of(o: Result[void, IoError]): i32 {
    match (o) {
        Err(e) => {
            match (e) {
                Other(c, msg, _) => { if (msg == "Bad file descriptor") { return 7; } return 5; },
                _ => { return 4; }
            }
        },
        Ok(_) => { return 1; }
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
