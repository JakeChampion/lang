package playground

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// embedCodeRe pulls the `code={`…`}` template-literal payload out of a
// <FernPlayground …/> usage in an MDX docs page. The snippets contain
// no backticks or `${}` interpolations (they're plain Fern source), so
// "everything up to the next backtick" is an exact match.
var embedCodeRe = regexp.MustCompile("(?s)code=\\{`(.*?)`\\}")

// TestDocsPlaygroundEmbedsCompile type-checks every <FernPlayground>
// snippet embedded in the docs site (site/src/content/docs/**.mdx)
// through the same front end the playground runs (modload → checker →
// monomorph). The embeds autorun in-browser via the interpreter, whose
// correctness gate is exactly this front end: post-prelude a snippet
// sees only what it `import`s, so an embed that calls `(n).to_string()`
// without `import "std/i32";`, or uses the retired bare-name
// `assert_*` test prelude, no longer type-checks and silently shows an
// error instead of output.
//
// This is the docs-side sibling of TestPlaygroundExamplesCompile (which
// guards web/index.html's built-in examples): the same flip-class
// regression bit the docs embeds, and only a human loading the page
// would have noticed.
func TestDocsPlaygroundEmbedsCompile(t *testing.T) {
	// internal/wasm/playground -> repo root is three levels up.
	docsDir := filepath.Join("..", "..", "..", "site", "src", "content", "docs")
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("docs dir not found (%v); skipping", err)
	}

	// MDX template literals can escape a backslash, backtick, or `$`;
	// undo those so the extracted source matches what the browser runs.
	deescape := strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`)

	seen := 0
	err := filepath.Walk(docsDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || !strings.HasSuffix(path, ".mdx") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(docsDir, path)
		for i, m := range embedCodeRe.FindAllStringSubmatch(string(data), -1) {
			src := deescape.Replace(m[1])
			seen++
			t.Run(rel+"#"+itoa(i), func(t *testing.T) {
				if _, _, err := frontEnd(src, ""); err != nil {
					t.Errorf("FernPlayground embed in %s no longer type-checks:\n%v\n--- source ---\n%s", rel, err, src)
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The tutorial + landing pages ship several embeds each; if the
	// extractor finds almost none the regex has drifted from the MDX.
	if seen < 8 {
		t.Fatalf("only extracted %d <FernPlayground> embeds; the regex likely drifted from the MDX", seen)
	}
}

// itoa avoids pulling strconv in for a single small index.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
