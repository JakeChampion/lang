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

// The same family one level down: fstatat's FLAGS word. Darwin's
// AT_SYMLINK_NOFOLLOW is 0x20 where Linux's is 0x100, and XNU rejects an
// unknown flag bit with EINVAL instead of ignoring it — so the Linux
// constant did not degrade `lstat` into a follow, it failed every call,
// existing path or not. A directory walk was impossible on the target
// while both the x86-64 and arm64-linux legs stayed green.
//
// AT_REMOVEDIR and AT_EACCESS were already translated beside it, which is
// what made this one hard to see.
const lstatFlagSrc = `function main(): i32 {
    match (lstat("/tmp/fern-ls")) {
        Ok(s) => {},
        Err(e) => {}
    }
    match (stat("/tmp/fern-st")) {
        Ok(s) => {},
        Err(e) => {}
    }
    return 0;
}`

func TestArm64DarwinStatFlagsAreXNUs(t *testing.T) {
	asm := compile(t, lstatFlagSrc, Options{Darwin: true})
	body := helperBody(asm, "__fern_lstat")
	if body == "" {
		t.Fatal("__fern_lstat not emitted; the test cannot guard a helper that is absent")
	}
	if !strings.Contains(body, "#32") {
		t.Error("arm64-darwin __fern_lstat does not pass XNU's AT_SYMLINK_NOFOLLOW (0x20): every lstat returns EINVAL, so no directory walk can classify an entry")
	}
	if strings.Contains(body, "#256") {
		t.Error("arm64-darwin __fern_lstat still passes the LINUX AT_SYMLINK_NOFOLLOW (0x100), which XNU rejects with EINVAL")
	}
}

// The Linux word must not move while the Darwin one is being pinned.
func TestArm64LinuxStatFlagsUnchanged(t *testing.T) {
	asm := compile(t, lstatFlagSrc, Options{})
	if body := helperBody(asm, "__fern_lstat"); body == "" || !strings.Contains(body, "#256") {
		t.Error("arm64-linux __fern_lstat no longer passes AT_SYMLINK_NOFOLLOW (0x100)")
	}
}
