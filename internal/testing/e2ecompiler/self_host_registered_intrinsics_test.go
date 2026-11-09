package e2ecompiler

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// checker.is_registered_intrinsic skips the list for a name without the
// "__" prefix, so every name in REGISTERED_INTRINSICS must carry it.
func TestSelfHostRegisteredIntrinsicsStartWithUnderscores(t *testing.T) {
	src, err := os.ReadFile(e2eharness.RepoPath("compiler", "checker.fern"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "\nconst REGISTERED_INTRINSICS: string[] = [\n")
	if start < 0 {
		t.Fatal("compiler/checker.fern has no REGISTERED_INTRINSICS")
	}
	body := text[start:]
	body = body[:strings.Index(body, "\n];\n")]
	names := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(body, -1)
	if len(names) < 100 {
		t.Fatalf("found %d names, want the whole list", len(names))
	}
	for _, m := range names {
		if !strings.HasPrefix(m[1], "__") {
			t.Errorf("intrinsic %q does not start with __", m[1])
		}
	}
}
