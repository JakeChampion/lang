package arm64

import (
	"strings"
	"testing"
)

// open_reader_with / open_writer_with translate Fern's flags word at run
// time, so the words they OR in are the target's: O_CREAT is 64 on Linux
// and 0x200 on XNU, O_NONBLOCK 2048 and 0x4. Textual for the same reason
// as darwin_open_flags_test.go: the Linux words are legal, different
// modes on XNU, so a wrong one opens rather than fails.

const openWithSrc = `function main(): i32 {
    match (open_writer_with("/tmp/fern-oww", 3)) {
        Ok(w) => {},
        Err(e) => {}
    }
    match (open_reader_with("/tmp/fern-orw", 2)) {
        Ok(r) => {},
        Err(e) => {}
    }
    return 0;
}`

func TestArm64OpenWithFlagWords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  Options
		creat string
		nb    string
	}{
		{"linux", Options{}, "orr w2, w2, #64", "orr w2, w2, #2048"},
		{"darwin", Options{Darwin: true}, "orr w2, w2, #512", "orr w2, w2, #4"},
	} {
		asm := compile(t, openWithSrc, tc.opts)
		for _, sym := range []string{"__fern_open_reader_with", "__fern_open_writer_with"} {
			body := helperBody(asm, sym)
			if body == "" {
				t.Fatalf("%s: %s not emitted; the test cannot guard a helper that is absent", tc.name, sym)
			}
			for _, want := range []string{tc.creat, tc.nb} {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s lacks %q", tc.name, sym, want)
				}
			}
		}
		if !strings.Contains(helperBody(asm, "__fern_open_writer_with"), "mov w2, #1") {
			t.Errorf("%s: the writer does not ask for O_WRONLY", tc.name)
		}
	}
}
