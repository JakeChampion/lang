package e2ecompiler

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
// is compiler/playground_run.fern. Each is driven here through the
// same driver, hosted natively, in every mode the page uses on its world: a
// handler program's Run is locked out on the page, so it is built for the
// http world's two forms instead.
//
// Both sets guard the flip-class regression where a program used a bare
// prelude name and silently stopped compiling, which only a human loading
// the page would otherwise notice — and, for the interpreter, a construct
// the self-host evaluator does not implement, which exits 254 with the
// evaluator's reason on stderr.

// templateLiteral undoes the escapes a JS or MDX template literal carries,
// so the extracted source is what the browser runs.
var templateLiteral = strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`)

// pageExamples returns web/index.html's `const examples = { … }` table.
func pageExamples(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "index.html"))
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
// page uses on the cli world: -check must accept it, -interp must answer
// (254 is the driver's "the evaluator could not run this", with its reason
// on stderr), and -emit core-module must produce a wasm binary. A program
// may print nothing, as a filter given no input does; what it prints is its
// own business, and the page shows its exit code either way.
func playgroundProgramRuns(t *testing.T, bin, src string) {
	t.Helper()
	if _, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-check"); code != 0 {
		t.Errorf("-check exited %d:\n%s", code, stderr)
	}
	_, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-interp")
	if code == 254 {
		t.Errorf("-interp could not evaluate the program (exit 254)\n%s", stderr)
	}
	if strings.Contains(stderr, "fern: interp:") {
		t.Errorf("-interp failed:\n%s", stderr)
	}
	mod, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-emit", "core-module")
	if code != 0 {
		t.Errorf("-emit core-module exited %d:\n%s", code, stderr)
	} else if !strings.HasPrefix(mod, "\x00asm") {
		t.Errorf("-emit core-module did not write a wasm binary (%d bytes)", len(mod))
	}
}

// playgroundHandlerBuilds drives a wasi:http handler through the two forms
// the page uses on that world: Run (wasm) instantiates the core module
// against web/wasi-http-shim.js, Build component offers the component.
func playgroundHandlerBuilds(t *testing.T, bin, src string) {
	t.Helper()
	for _, form := range [][]string{{"-emit", "core-module"}, {"-emit", "component"}} {
		args := append([]string{"-target", "wasm32-wasi-http"}, form...)
		out, stderr, code := runPlayground(t, bin, t.TempDir(), src, args...)
		if code != 0 {
			t.Errorf("%s exited %d:\n%s", strings.Join(args, " "), code, stderr)
		} else if !strings.HasPrefix(out, "\x00asm") {
			t.Errorf("%s did not write a wasm binary (%d bytes)", strings.Join(args, " "), len(out))
		}
	}
}

// playgroundProgramWorks runs a page program the way the page would: a
// handler on the http world, anything else on the cli world.
func playgroundProgramWorks(t *testing.T, bin, src string) {
	t.Helper()
	if strings.Contains(src, "function handle") {
		playgroundHandlerBuilds(t, bin, src)
		return
	}
	playgroundProgramRuns(t, bin, src)
}

func TestSelfHostPlaygroundExamples(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	examples := pageExamples(t)
	bin := buildPlaygroundDriver(t)
	handlers := 0
	for name, src := range examples {
		if strings.Contains(src, "function handle") {
			handlers++
		}
		t.Run(name, func(t *testing.T) { playgroundProgramWorks(t, bin, src) })
	}
	if handlers == 0 {
		t.Error("no wasi:http example on the page; the http world's forms went unexercised")
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
	docsDir := filepath.Join("..", "..", "..", "site", "src", "content", "docs")
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
		t.Run(e.name, func(t *testing.T) { playgroundProgramWorks(t, bin, e.src) })
	}
}
