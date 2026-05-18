package lsp

import (
	"testing"
)

func documentSymbolsFor(src string) []documentSymbol {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runDocumentSymbols(s.docs["file:///t"], "file:///t")
}

func hasSymbol(syms []documentSymbol, name string) (documentSymbol, bool) {
	for _, s := range syms {
		if s.Name == name {
			return s, true
		}
	}
	return documentSymbol{}, false
}

func TestDocumentSymbols_TopLevelDecls(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\n" +
		"enum Color { Red, Green }\n" +
		"function main(): i32 { return 0; }\n"
	got := documentSymbolsFor(src)
	if _, ok := hasSymbol(got, "Point"); !ok {
		t.Errorf("expected Point in symbols, got %+v", got)
	}
	if _, ok := hasSymbol(got, "Color"); !ok {
		t.Errorf("expected Color in symbols, got %+v", got)
	}
	if _, ok := hasSymbol(got, "main"); !ok {
		t.Errorf("expected main in symbols, got %+v", got)
	}
}

func TestDocumentSymbols_StructHasFieldChildren(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\nfunction main(): i32 { return 0; }\n"
	got := documentSymbolsFor(src)
	point, ok := hasSymbol(got, "Point")
	if !ok {
		t.Fatal("Point missing from symbols")
	}
	if len(point.Children) != 2 {
		t.Errorf("expected 2 field children, got %d", len(point.Children))
	}
}

func TestDocumentSymbols_EnumHasVariantChildren(t *testing.T) {
	src := "enum Color { Red, Green, Blue }\nfunction main(): i32 { return 0; }\n"
	got := documentSymbolsFor(src)
	c, ok := hasSymbol(got, "Color")
	if !ok {
		t.Fatal("Color missing from symbols")
	}
	if len(c.Children) != 3 {
		t.Errorf("expected 3 variant children, got %d", len(c.Children))
	}
}

func TestDocumentSymbols_SkipsInternalMangledNames(t *testing.T) {
	// Stdlib pulls in lots of __method_* and __map_* helpers.
	// The outline should NOT include them.
	src := "function main(): i32 { return 0; }\n"
	got := documentSymbolsFor(src)
	for _, s := range got {
		if len(s.Name) > 2 && s.Name[:2] == "__" {
			t.Errorf("internal name %q leaked into outline", s.Name)
		}
	}
}
