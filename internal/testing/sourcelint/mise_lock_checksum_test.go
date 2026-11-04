package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pinned mise installs with --locked, which refuses a platform entry
// that records a URL and no checksum.
func TestMiseLockRecordsAChecksumForEveryPlatform(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "mise.lock"))
	if err != nil {
		t.Fatalf("read mise.lock: %v", err)
	}
	header := ""
	hasURL, hasChecksum := false, false
	check := func() {
		if strings.Contains(header, `"platforms.`) && hasURL && !hasChecksum {
			t.Errorf("mise.lock: %s records a url and no checksum; add its checksum for that platform", header)
		}
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			check()
			header, hasURL, hasChecksum = line, false, false
			continue
		}
		hasURL = hasURL || strings.HasPrefix(line, "url =")
		hasChecksum = hasChecksum || strings.HasPrefix(line, "checksum =")
	}
	check()
}
