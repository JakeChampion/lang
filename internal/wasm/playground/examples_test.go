package playground

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPlaygroundExamplesCompile compiles every built-in example from
// web/index.html through the same entry points the browser calls
// (CompileCoreWasm / CompileComponent). The examples are real Fern
// programs shipped to users; post-prelude they must declare their
// imports like any other program. This guards the flip-class
// regression where the examples used bare prelude names and silently
// stopped compiling (only the Playwright suite caught it).
func TestPlaygroundExamplesCompile(t *testing.T) {
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
		t.Run(name, func(t *testing.T) {
			var err error
			if strings.Contains(src, "function handle") {
				_, err = CompileComponent(src, "wasm32-wasi-http")
			} else {
				_, err = CompileCoreWasm(src)
			}
			if err != nil {
				t.Errorf("playground example %q no longer compiles:\n%v", name, err)
			}
		})
		seen++
	}
	if seen < 5 {
		t.Fatalf("only extracted %d examples; the regex likely drifted from index.html", seen)
	}
}
