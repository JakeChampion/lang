package playground

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// embedCodeRe pulls a template-literal Fern payload out of a docs page.
// Two spellings carry one: the JSX attribute `code={`…`}` (a
// <FernPlayground> or <Specimen> in MDX or in an .astro page) and the
// object property `code: `…“ (a landing-page tour entry). Snippets may
// escape a backtick but never interpolate, so "up to the next unescaped
// backtick" is an exact match.
var embedCodeRe = regexp.MustCompile("(?s)code(?:=\\{|:\\s*)`((?:\\\\.|[^`])*)`")

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
	siteDir := filepath.Join("..", "..", "..", "site", "src")
	if _, err := os.Stat(siteDir); err != nil {
		t.Skipf("site dir not found (%v); skipping", err)
	}

	// Both halves of the site: the Starlight docs collection (MDX) and
	// the bespoke pages that sit outside it (.astro). The landing page
	// is the second kind, and before it was covered here its snippets
	// had no gate at all.
	roots := []struct {
		dir    string
		suffix string
	}{
		{filepath.Join(siteDir, "content", "docs"), ".mdx"},
		{filepath.Join(siteDir, "pages"), ".astro"},
	}

	// Template literals can escape a backslash, backtick, or `$`; undo
	// those so the extracted source matches what the browser runs.
	deescape := strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`)

	seen := 0
	for _, root := range roots {
		if _, err := os.Stat(root.dir); err != nil {
			continue
		}
		err := filepath.Walk(root.dir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() || !strings.HasSuffix(path, root.suffix) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(siteDir, path)
			for i, m := range embedCodeRe.FindAllStringSubmatch(string(data), -1) {
				src := deescape.Replace(m[1])
				seen++
				t.Run(rel+"#"+itoa(i), func(t *testing.T) {
					if _, _, err := frontEnd(src, ""); err != nil {
						t.Errorf("Fern snippet in %s no longer type-checks:\n%v\n--- source ---\n%s", rel, err, src)
					}
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The tutorials and the landing page ship several snippets each; if
	// the extractor finds almost none the regex has drifted.
	if seen < 12 {
		t.Fatalf("only extracted %d embedded snippets; the regex likely drifted from the source", seen)
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
