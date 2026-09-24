package e2e

import "testing"

// BufWriter.flush hands its buffer to __buf_writer_put, whose match over
// `b.w.write(s)` has a `Some(e)` arm that moves the error into a rebuilt
// BufWriter. That move refused the scrutinee reclaim for the whole match, so
// the None box every successful write returns leaked once per flush (#9180).
// What survives is the stdout handle, sentinel-headered by design, and the
// writer's own buffer, still live at exit.
const bufWriterFlushSrc = `import "std/io_buffered";
function main(): i32 {
    var b: io_buffered.BufWriter = io_buffered.buf_writer_new(stdout(), 64);
    var i: i32 = 0;
    while (i < 200) {
        b = b.write_string("x");
        b = b.flush();
        i = i + 1;
    }
    return 0;
}`

func checkBufWriterFlush(t *testing.T, stderr string, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("exit %d, want 0\n%s", exit, stderr)
	}
	allocs, frees, _ := parseLeakCheckLine(t, stderr)
	if allocs-frees > 3 {
		t.Fatalf("allocs=%d frees=%d: more than the handle and the live writer survive 200 flushes (#9180)", allocs, frees)
	}
}

func TestX86_64BufWriterFlushLeakFree(t *testing.T) {
	_, stderr, exit := runLeakCheckX86_64(t, bufWriterFlushSrc)
	checkBufWriterFlush(t, stderr, exit)
}

func TestArm64BufWriterFlushLeakFree(t *testing.T) {
	_, stderr, exit := runLeakCheckArm64(t, bufWriterFlushSrc)
	checkBufWriterFlush(t, stderr, exit)
}

func TestWASMBufWriterFlushLeakFree(t *testing.T) {
	_, stderr, exit := runLeakCheckWasm(t, bufWriterFlushSrc, false)
	checkBufWriterFlush(t, stderr, exit)
}
