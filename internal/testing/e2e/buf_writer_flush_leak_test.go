package e2e

import "testing"

const bufWriterFlushSrc = `import "std/io_buffered";
function main(): i32 {
    let b: io_buffered.BufWriter = io_buffered.buf_writer_new(stdout(), 64);
    let i: i32 = 0;
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
