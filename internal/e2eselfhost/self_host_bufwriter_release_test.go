package e2eselfhost

import "testing"

// A BufWriter's buffer is freed when the last copy of the writer dies, not
// only by close() (#11201). `kept` is flushed and never closed, as a writer
// over stdout always is; `b` and `c` are two copies sharing one buffer, where
// the first copy's death must leave the second's buffer whole; `f` is closed,
// which must not free the buffer a second time. Each writes past its own
// threshold, so the buffer is pushed into and flushed in every case.
const bufWriterReleaseSrc = `import "std/io_buffered" as io;

function more(b: io.BufWriter, s: string): io.BufWriter { return b.write_string(s); }

function main(): i32 {
    let kept: io.BufWriter = io.buf_writer_new(stdout(), 8);
    kept = kept.write_string("one two three\n");
    kept = kept.write_string("four\n");
    kept = kept.flush();

    let b: io.BufWriter = io.buf_writer_new(stdout(), 64);
    let c: io.BufWriter = b;
    b = b.write_string("two\n");
    if (c.buffered() != 4) { return 1; }
    c = more(c, "kept\n");
    if (c.buffered() != 9) { return 2; }
    c = c.flush();
    if (c.buffered() != 0) { return 3; }

    let path: string = "/tmp/fern_bufwriter_release_test.txt";
    match (open_writer(path)) {
        Ok(w) => {
            let f: io.BufWriter = io.buf_writer_new(w, 4);
            f = f.write_string("abcdef");
            f = f.write_string("gh");
            match (f.close()) {
                Some(_) => { return 4; },
                None => {}
            }
        },
        Err(_) => { return 5; }
    }
    match (read_file(path)) {
        Ok(s) => {
            remove_file(path);
            if (s != "abcdefgh") { return 6; }
        },
        Err(_) => { return 7; }
    }
    return 42;
}`

func TestSelfHostBufWriterReleasesItsBuffer(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, bufWriterReleaseSrc, target, "FERN_LEAKCHECK=1")
			if code != 42 {
				t.Fatalf("exited %d, want 42 (1..3: the copies stopped sharing a buffer; 4..7: close lost bytes)\n%s", code, stderr)
			}
			allocs, frees, live := parseLeakcheck(t, target, stderr)
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d: a writer's buffer outlived every copy of it", allocs, frees, live)
			}
		})
	}
}

// The same program built under the sanitizer: freeing the buffer at the
// first copy's death, or again after close(), is a use-after-free there.
func TestSelfHostBufWriterReleaseSanitized(t *testing.T) {
	cli := buildSelfHostCLI(t)
	stderr, code := cli.exitOf(t, bufWriterReleaseSrc, "x86-64-linux", "FERN_SANITIZE=1")
	if code != 42 {
		t.Fatalf("exited %d under FERN_SANITIZE=1, want 42\n%s", code, stderr)
	}
}
