package arm64

import (
	"strings"
	"testing"
)

// open_reader_with / open_writer_with translate Fern's flags word at run
// time, so the words they OR in are the target's: Linux and XNU share no
// value (O_CREAT is 64 and 0x200, O_NONBLOCK 2048 and 0x4, ...), and the
// two bits XNU has no word for — O_DIRECT, O_NOATIME — are refused there
// rather than ORed as a Linux number XNU would read as something else.
// Textual for the same reason as darwin_open_flags_test.go: the Linux
// words are legal, different modes on XNU, so a wrong one opens rather
// than fails.

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
		name    string
		opts    Options
		want    []string // one `orr` per honoured bit, in bit order
		refused []string // one `tbnz` per refused bit
		absent  []string
	}{
		{"linux", Options{},
			[]string{
				"tbz w22, #0,", "orr w2, w2, #64 // O_CREAT",
				"tbz w22, #1,", "orr w2, w2, #2048 // O_NONBLOCK",
				"tbz w22, #2,", "orr w2, w2, #128 // O_EXCL",
				"tbz w22, #3,", "orr w2, w2, #65536 // O_DIRECT",
				"tbz w22, #4,", "orr w2, w2, #16384 // O_DIRECTORY",
				"tbz w22, #5,", "orr w2, w2, #4096 // O_DSYNC",
				// O_SYNC is 0x101000, two bits apart: two bitmask immediates.
				"tbz w22, #6,", "orr w2, w2, #4096 // O_SYNC", "orr w2, w2, #1048576 // O_SYNC",
				"tbz w22, #7,", "orr w2, w2, #262144 // O_NOATIME",
				"tbz w22, #8,", "orr w2, w2, #256 // O_NOCTTY",
				"tbz w22, #9,", "orr w2, w2, #32768 // O_NOFOLLOW",
			},
			nil,
			[]string{"tbnz w22", "__fern_alloc_box"}},
		{"darwin", Options{Darwin: true},
			[]string{
				"tbz w22, #0,", "orr w2, w2, #512 // O_CREAT",
				"tbz w22, #1,", "orr w2, w2, #4 // O_NONBLOCK",
				"tbz w22, #2,", "orr w2, w2, #2048 // O_EXCL",
				"tbz w22, #4,", "orr w2, w2, #1048576 // O_DIRECTORY",
				"tbz w22, #5,", "orr w2, w2, #4194304 // O_DSYNC",
				"tbz w22, #6,", "orr w2, w2, #128 // O_SYNC",
				"tbz w22, #8,", "orr w2, w2, #131072 // O_NOCTTY",
				"tbz w22, #9,", "orr w2, w2, #256 // O_NOFOLLOW",
			},
			[]string{"tbnz w22, #3,", "tbnz w22, #7,", "mov w1, #5"},
			[]string{"tbz w22, #3,", "tbz w22, #7,"}},
	} {
		asm := compile(t, openWithSrc, tc.opts)
		for _, sym := range []string{"__fern_open_reader_with", "__fern_open_writer_with"} {
			body := helperBody(asm, sym)
			if body == "" {
				t.Fatalf("%s: %s not emitted; the test cannot guard a helper that is absent", tc.name, sym)
			}
			at := 0
			for _, want := range tc.want {
				i := strings.Index(body[at:], want)
				if i < 0 {
					t.Errorf("%s %s lacks %q after offset %d", tc.name, sym, want, at)
					continue
				}
				at += i + len(want)
			}
			for _, want := range tc.refused {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s lacks the refusal %q", tc.name, sym, want)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(body, bad) {
					t.Errorf("%s %s contains %q, which it must not", tc.name, sym, bad)
				}
			}
		}
		if !strings.Contains(helperBody(asm, "__fern_open_writer_with"), "mov w2, #1") {
			t.Errorf("%s: the writer does not ask for O_WRONLY", tc.name)
		}
	}
}
