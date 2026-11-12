package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A binary image is not UTF-8, so a function that makes one returns `u8[]`
// (#14441), and a function that reads one hashes the bytes (#15447). A
// `string` of an image needs `string_from_bytes_unchecked`, and these sources
// have no other use for it.
func TestImageWritersReturnBytes(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	for _, name := range []string{"elfimage.fern", "playground_run.fern", "rcmemo.fern"} {
		src, err := os.ReadFile(filepath.Join(root, "compiler", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "string_from_bytes_unchecked") {
			t.Errorf("compiler/%s makes a string from the bytes of an image", name)
		}
	}
	src, err := os.ReadFile(filepath.Join(root, "compiler", "fern.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"arm64_kernel_image", "arm64_linux_image", "x86_kernel_image", "arm64_elf_shared", "x86_elf_shared"} {
		head := regexp.MustCompile(`(?m)^function ` + fn + `\(.*\): (\S+) \{$`).FindSubmatch(src)
		if head == nil {
			t.Errorf("compiler/fern.fern has no function %s", fn)
			continue
		}
		if string(head[1]) != "u8[]" {
			t.Errorf("%s returns %s, want u8[]", fn, head[1])
		}
	}
}
