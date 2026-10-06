package e2ecompiler

import "testing"

// A string is freed at its length's size class, so a runtime helper that
// wraps a buffer must hand back a block allocated at the string's length.
// read_file sized its buffer one byte past fstat's answer, and env and
// temp_dir kept the NUL a syscall needed, so each returned a string whose
// block sat a class above the one its free used whenever the length was a
// multiple of 8. Each case below lands on that boundary: an 8-byte file, an
// 8-byte variable, and temp_dir prefixes of every length from 1 to 8, one of
// which makes the path a multiple of 8. /proc/self/stat reports size 0, so
// its read grows the buffer and is copied to an exact block.
const runtimeStringClassSrc = `import "std/i32";

function main(): i32 {
    let path: string = "/tmp/fern_string_class_test.txt";
    match (write_file(path, "abcdefgh")) {
        Ok(_) => {},
        Err(_) => { return 1; }
    }
    match (read_file(path)) {
        Ok(s) => { if (s != "abcdefgh") { return 2; } },
        Err(_) => { return 3; }
    }
    remove_file(path);
    match (read_file("/proc/self/stat")) {
        Ok(s) => { if (s.len() == 0) { return 4; } },
        Err(_) => { return 5; }
    }
    match (env("FERN_STRING_CLASS_PROBE")) {
        Some(v) => { if (v != "abcdefgh") { return 6; } },
        None => { return 7; }
    }
    let pre: string = "";
    let i: i32 = 0;
    while (i < 8) {
        pre = pre + "p";
        match (temp_dir(pre)) {
            Ok(d) => { remove_dir(d); },
            Err(_) => { return 8; }
        }
        i = i + 1;
    }
    return 42;
}`

func TestSelfHostRuntimeStringsFreeAtTheirClass(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Setenv("FERN_STRING_CLASS_PROBE", "abcdefgh")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, runtimeStringClassSrc, target, "FERN_LEAKCHECK=1")
			if code != 42 {
				t.Fatalf("exited %d, want 42\n%s", code, stderr)
			}
			allocs, frees, live := parseLeakcheck(t, target, stderr)
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d: a runtime string was freed at a smaller class than its block", allocs, frees, live)
			}
		})
	}
}
