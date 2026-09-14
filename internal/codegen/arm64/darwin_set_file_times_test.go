package arm64

import (
	"strings"
	"testing"
)

// XNU's attribute list has no UTIME_NOW: a now bit reads gettimeofday and
// names the value, and the kernel then treats it as any value, which only
// the file's owner may write. Apple's libc sets FSOPT_UTIMES_NULL (0x40)
// in the setattrlist options when both halves are now, and the kernel
// then asks for write access instead, as Linux does for UTIME_NOW. This
// is textual because the semantics need a file the caller can write and
// does not own, which the macos-15 lane, running as the owner, cannot
// observe.

const setFileTimesSrc = `function main(): i32 {
    match (set_file_times("/tmp/fern-sft", 1, 2, 3, 4, 24)) {
        Ok(v) => {},
        Err(e) => {}
    }
    return 0;
}`

func TestArm64DarwinSetFileTimesBothNowIsUtimesNull(t *testing.T) {
	asm := compile(t, setFileTimesSrc, Options{Darwin: true})
	body := helperBody(asm, "__fern_set_file_times")
	if body == "" {
		t.Fatal("__fern_set_file_times not emitted; the test cannot guard a helper that is absent")
	}
	for _, want := range []string{
		"mov x16, #116",     // gettimeofday, the value a now bit names
		"and x9, x23, #30",  // both now bits and neither omit bit …
		"cmp x9, #24",       // … is exactly 24
		"orr x5, x5, #0x40", // FSOPT_UTIMES_NULL into the options
		"mov x16, #524",     // setattrlistat
	} {
		if !strings.Contains(body, want) {
			t.Errorf("arm64-darwin __fern_set_file_times lacks %q: a bare touch on a writable file the caller does not own is refused with EPERM", want)
		}
	}
}

// Linux spells now as UTIME_NOW in the timespec and has no options word
// to set; the sentinel must not turn into a clock read there.
func TestArm64LinuxSetFileTimesNowIsUtimeNow(t *testing.T) {
	asm := compile(t, setFileTimesSrc, Options{})
	body := helperBody(asm, "__fern_set_file_times")
	if body == "" {
		t.Fatal("__fern_set_file_times not emitted; the test cannot guard a helper that is absent")
	}
	if !strings.Contains(body, "#1073741823") {
		t.Error("arm64-linux __fern_set_file_times never writes UTIME_NOW (1<<30 - 1)")
	}
	if strings.Contains(body, "#0x40") {
		t.Error("arm64-linux __fern_set_file_times sets an XNU options bit")
	}
}
