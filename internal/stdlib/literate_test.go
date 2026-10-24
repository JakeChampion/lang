package stdlib

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLiterateModulesRetainLogicalNamesAndSourcePositions(t *testing.T) {
	names, err := Modules()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		module, err := ReadModule(name)
		if err != nil {
			t.Fatal(err)
		}
		if module.DocPath == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if module.DocPath != name+".fern.md" || !strings.HasPrefix(module.DocSource, "# ") {
				t.Fatalf("document provenance: %#v", module)
			}
			if ModuleKey(name) != "stdlib://"+name+".fern" {
				t.Fatalf("logical module key changed: %q", ModuleKey(name))
			}
			document := strings.Split(module.DocSource, "\n")
			for line, code := range strings.Split(strings.TrimSuffix(module.Code, "\n"), "\n") {
				if line >= len(module.LineMap) {
					t.Fatalf("source line %d has no document position", line+1)
				}
				position := module.LineMap[line]
				if position.Lit < 1 || position.Lit > len(document) || document[position.Lit-1] != code {
					t.Fatalf("source line %d (%q) maps to document line %d", line+1, code, position.Lit)
				}
			}
			for _, spelling := range []string{name, name + ".fern", name + ".fern.md"} {
				code, ok := Resolve(spelling)
				if !ok || code != module.Code {
					t.Fatalf("Resolve(%q) does not return the tangled source", spelling)
				}
			}
		})
	}
}

func TestModuleDiscoveryIncludesLiterateSourcesOnce(t *testing.T) {
	modules, err := Modules()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, name := range modules {
		if found[name] || strings.HasSuffix(name, ".fern") || strings.HasSuffix(name, ".md") {
			t.Fatalf("invalid or repeated module name: %q", name)
		}
		found[name] = true
		if _, ok := Resolve(name); !ok {
			t.Errorf("discovered module %q does not resolve", name)
		}
	}
	for _, name := range []string{"std/option", "std/result", "std/csv", "std/io", "core/int"} {
		if !found[name] {
			t.Errorf("module discovery omitted %q", name)
		}
	}
}

func TestLiterateSourceErrorsAndPlainSourcePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, plain, document, wantError string
	}{
		{"plain-wins", "pub const VALUE: i32 = 1;\n", "invalid document", ""},
		{"undefined-chunk", "", "```fern\n<<*>>=\n<<missing>>\n```\n", "undefined chunk"},
		{"multiple-modules", "", "```fern file=a.fern\nx\n```\n```fern file=b.fern\ny\n```\n", "must define one module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{"std/example.fern.md": {Data: []byte(tc.document)}}
			if tc.plain != "" {
				files["std/example.fern"] = &fstest.MapFile{Data: []byte(tc.plain)}
			}
			module, err := readModule(files, "std/example.fern")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), "std/example.fern.md") || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got %v, want a document error containing %q", err, tc.wantError)
				}
			} else if err != nil || module.Code != tc.plain || module.DocPath != "" {
				t.Fatalf("plain source: %#v, %v", module, err)
			}
		})
	}
	if _, err := ReadModule("./local"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("local path: %v", err)
	}
}
