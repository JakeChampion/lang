package e2eselfhost

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The programs the playground ships run on the self-host compiler.
//
// web/index.html's built-in examples, and the <FernPlayground> snippets the
// docs site embeds, are real Fern programs put in front of users: the page
// checks, interprets and compiles them through web/playground.wasm, which
// is examples/self_host/playground_run.fern. Each is driven here through the
// same driver, hosted natively, in every mode the page uses. The wasi:http
// example runs on the Go toolchain still (internal/wasm/playground), which
// gates it.
//
// Both sets guard the flip-class regression where a program used a bare
// prelude name and silently stopped compiling, which only a human loading
// the page would otherwise notice — and, for the interpreter, a construct
// the self-host evaluator does not implement, which the page shows as an
// empty output pane.

// templateLiteral undoes the escapes a JS or MDX template literal carries,
// so the extracted source is what the browser runs.
var templateLiteral = strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`)

// pageExamples returns web/index.html's `const examples = { … }` table.
func pageExamples(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Skipf("web/index.html not found (%v); skipping", err)
	}
	block := regexp.MustCompile(`(?s)const examples = \{(.*?)\n\};`).FindStringSubmatch(string(data))
	if block == nil {
		t.Fatal("could not locate `const examples = { … }` in web/index.html")
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile("(?s)(\\w+):\\s*`(.*?)`").FindAllStringSubmatch(block[1], -1) {
		out[m[1]] = templateLiteral.Replace(m[2])
	}
	if len(out) < 5 {
		t.Fatalf("only extracted %d examples; the regex likely drifted from index.html", len(out))
	}
	return out
}

// playgroundProgramRuns drives one program through the three modes the
// page uses on the cli world: -check must accept it, -interp must produce
// output and an answer (254 is the driver's "the evaluator could not run
// this"), and -emit core-module must produce a wasm binary.
func playgroundProgramRuns(t *testing.T, bin, src string) {
	t.Helper()
	if _, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-check"); code != 0 {
		t.Errorf("-check exited %d:\n%s", code, stderr)
	}
	out, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-interp")
	if code == 254 {
		t.Errorf("-interp could not evaluate the program (exit 254)\n%s", stderr)
	}
	if strings.Contains(stderr, "fern: interp:") {
		t.Errorf("-interp failed:\n%s", stderr)
	}
	if out == "" {
		t.Errorf("-interp printed nothing; the page would show an empty output pane")
	}
	mod, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-emit", "core-module")
	if code != 0 {
		t.Errorf("-emit core-module exited %d:\n%s", code, stderr)
	} else if !strings.HasPrefix(mod, "\x00asm") {
		t.Errorf("-emit core-module did not write a wasm binary (%d bytes)", len(mod))
	}
}

func TestSelfHostPlaygroundExamples(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	examples := pageExamples(t)
	bin := buildPlaygroundDriver(t)
	for name, src := range examples {
		if strings.Contains(src, "function handle") {
			continue
		}
		t.Run(name, func(t *testing.T) { playgroundProgramRuns(t, bin, src) })
	}
}

// embedCodeRe pulls the `code={`…`}` template-literal payload out of a
// <FernPlayground …/> usage in an MDX docs page. The snippets contain no
// backticks or `${}` interpolations (they're plain Fern source), so
// "everything up to the next backtick" is an exact match.
var embedCodeRe = regexp.MustCompile("(?s)code=\\{`(.*?)`\\}")

func TestSelfHostDocsPlaygroundEmbeds(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	docsDir := filepath.Join("..", "..", "site", "src", "content", "docs")
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("docs dir not found (%v); skipping", err)
	}
	type embed struct{ name, src string }
	var embeds []embed
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
			embeds = append(embeds, embed{rel + "#" + itoa(i), templateLiteral.Replace(m[1])})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The tutorial + landing pages ship several embeds each; if the
	// extractor finds almost none the regex has drifted from the MDX.
	if len(embeds) < 8 {
		t.Fatalf("only extracted %d <FernPlayground> embeds; the regex likely drifted from the MDX", len(embeds))
	}
	bin := buildPlaygroundDriver(t)
	for _, e := range embeds {
		t.Run(e.name, func(t *testing.T) { playgroundProgramRuns(t, bin, e.src) })
	}
}
