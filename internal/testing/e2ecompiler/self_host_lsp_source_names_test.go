package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostLSPSourceNamesX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	const main = "function main(): i32 { let p: geo.Point = geo.make(); let own: Local__Name = Local__Name { value: 2 }; return p.value + own.value; }"
	writeTree(t, dir, map[string]string{
		"geometry.fern": "pub struct Point { value: i32 }\npub enum Kind { Text }\npub function make(): Point { return Point { value: 1 }; }\n",
		"main.fern":     "import \"./geometry\" as geo;\nstruct Local__Name { value: i32 }\n" + main + "\n",
	})
	alias := strings.Index(main, "geo.Point")
	own := strings.Index(main, "Local__Name")
	askLSP(t, s, dir, []string{"main.fern"}, []lspAsk{
		hoverAsk("alias", "main.fern", 2, alias+5, "struct geo.Point { value: i32 }", rng(2, alias, alias+len("geo.Point"))),
		hoverAsk("source-name", "main.fern", 2, own+3, "struct Local__Name { value: i32 }", rng(2, own, own+len("Local__Name"))),
	})
	probeLSP(t, s, dir, []string{"main.fern"}, []lspProbe{
		completeAt("locals", "main.fern", 2, strings.Index(main, "return ")+len("return "), func(t *testing.T, items []lspItem) {
			want := map[string]string{"p": "geo.Point", "own": "Local__Name"}
			for _, item := range items {
				if detail, ok := want[item.Label]; ok {
					if item.Detail != detail {
						t.Errorf("%s detail = %q, want %q", item.Label, item.Detail, detail)
					}
					delete(want, item.Label)
				}
			}
			if len(want) != 0 {
				t.Errorf("missing local completions: %v", want)
			}
		}),
	})
}
