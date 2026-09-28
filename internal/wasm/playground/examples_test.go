package playground

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPlaygroundHttpExamplesCompile compiles every wasi:http example from
// web/index.html through the entry point the browser calls for that world
// (CompileHttpComponent). The other examples run on the self-host compiler
// and are gated by TestSelfHostPlaygroundExamples in internal/e2eselfhost.
// The examples are real Fern programs shipped to users; post-prelude they
// must declare their imports like any other program. This guards the
// flip-class regression where the examples used bare prelude names and
// silently stopped compiling (only the Playwright suite caught it).
func TestPlaygroundHttpExamplesCompile(t *testing.T) {
	// internal/wasm/playground -> repo root is three levels up.
	htmlPath := filepath.Join("..", "..", "..", "web", "index.html")
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Skipf("web/index.html not found (%v); skipping", err)
	}
	html := string(data)

	block := regexp.MustCompile(`(?s)const examples = \{(.*?)\n\};`).FindStringSubmatch(html)
	if block == nil {
		t.Fatal("could not locate `const examples = { … }` in web/index.html")
	}
	entry := regexp.MustCompile("(?s)(\\w+):\\s*`(.*?)`")
	matches := entry.FindAllStringSubmatch(block[1], -1)
	if len(matches) == 0 {
		t.Fatal("no examples extracted")
	}

	deescape := strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`)
	seen := 0
	for _, m := range matches {
		name, src := m[1], deescape.Replace(m[2])
		if !strings.Contains(src, "function handle") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if _, err := CompileHttpComponent(src); err != nil {
				t.Errorf("playground example %q no longer compiles:\n%v", name, err)
			}
		})
		seen++
	}
	if seen < 1 {
		t.Fatalf("no wasi:http example extracted; the regex likely drifted from index.html")
	}
}
