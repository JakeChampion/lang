package arm64

import (
	"strings"
	"testing"
)

// Linux and XNU share none of the three open(2) bits these helpers set:
// O_CREAT is 0100 vs 0x200, O_TRUNC 01000 vs 0x400, O_APPEND 02000 vs 0x8.
// Emitting Linux's word on Darwin therefore does not fail — it asks for a
// DIFFERENT, legal mode:
//
//	577  (O_WRONLY|O_CREAT|O_TRUNC on Linux) = O_WRONLY|O_ASYNC|O_CREAT on XNU
//	1089 (O_WRONLY|O_CREAT|O_APPEND)         = O_WRONLY|O_ASYNC|O_TRUNC on XNU
//
// So an overwrite kept the old file's tail, and an APPEND emptied the file it
// opened. #6042 translated the flag words on the self-host path; the native
// backend kept the Linux constants, and `atFdCwd` being translated beside them
// is what made the omission hard to see.
//
// This is textual because the semantics need a macOS host — the behavioural
// half is TestArm64DarwinWriteFileTruncates in internal/e2e, which the
// macos-15 lane executes.

const openFlagsSrc = `function main(): i32 {
    match (open_writer("/tmp/fern-ow")) {
        Ok(w) => {},
        Err(e) => {}
    }
    match (open_appender("/tmp/fern-oa")) {
        Ok(w) => {},
        Err(e) => {}
    }
    match (write_file("/tmp/fern-wf", "hi")) {
        Ok(v) => {},
        Err(e) => {}
    }
    return 0;
}`

func TestArm64DarwinOpenFlagsAreXNUs(t *testing.T) {
	asm := compile(t, openFlagsSrc, Options{Darwin: true})

	for _, c := range []struct {
		sym, want, reject, why string
	}{
		{"__fern_open_writer", "#1537", "#577",
			"open_writer asks XNU for O_ASYNC|O_CREAT — it creates without truncating, so " +
				"overwriting a longer file leaves its trailing bytes"},
		{"__fern_open_appender", "#521", "#1089",
			"open_appender sets XNU's O_TRUNC (0x400) — it EMPTIES the file it opens " +
				"instead of appending to it"},
		{"__fern_write_file", "#1537", "#577",
			"write_file asks XNU for O_ASYNC|O_CREAT — it creates without truncating, so " +
				"rewriting a file with shorter content leaves the old tail"},
	} {
		body := helperBody(asm, c.sym)
		if body == "" {
			t.Fatalf("%s not emitted; the test cannot guard a helper that is absent", c.sym)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("arm64-darwin %s does not use the XNU flag word %s: %s", c.sym, c.want, c.why)
		}
		if strings.Contains(body, c.reject) {
			t.Errorf("arm64-darwin %s still emits the LINUX flag word %s: %s", c.sym, c.reject, c.why)
		}
	}
}

// The Linux words must not move while the Darwin ones are being pinned.
func TestArm64LinuxOpenFlagsUnchanged(t *testing.T) {
	asm := compile(t, openFlagsSrc, Options{})
	for _, c := range [][2]string{
		{"__fern_open_writer", "#577"},
		{"__fern_open_appender", "#1089"},
		{"__fern_write_file", "#577"},
	} {
		if body := helperBody(asm, c[0]); body == "" || !strings.Contains(body, c[1]) {
			t.Errorf("arm64-linux %s no longer emits %s", c[0], c[1])
		}
	}
}
