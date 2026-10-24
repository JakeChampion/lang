package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var templateLiteral = strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`)

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

func playgroundHandlerBuilds(t *testing.T, bin, src string) {
	t.Helper()
	for _, form := range [][]string{{"-emit", "core-module"}, nil} {
		args := append([]string{"-target", "wasm32-wasi-http"}, form...)
		out, stderr, code := runPlayground(t, bin, t.TempDir(), src, args...)
		if code != 0 {
			t.Errorf("%s exited %d:\n%s", strings.Join(args, " "), code, stderr)
		} else if !strings.HasPrefix(out, "\x00asm") {
			t.Errorf("%s did not write a wasm binary (%d bytes)", strings.Join(args, " "), len(out))
		}
	}
}

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
	if len(embeds) < 8 {
		t.Fatalf("only extracted %d <FernPlayground> embeds; the regex likely drifted from the MDX", len(embeds))
	}
	bin := buildPlaygroundDriver(t)
	for _, e := range embeds {
		t.Run(e.name, func(t *testing.T) { playgroundProgramWorks(t, bin, e.src) })
	}
}
