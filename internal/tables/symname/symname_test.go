package symname

import "testing"

// Round-tripping is what lets debug info describe the source while the binary
// carries the mangled symbol: DW_AT_name is Source(sym), and it has to give
// back exactly what the program was written with — including the names that
// already start with an underscore run, where a sloppy trim would eat one.
func TestFnSourceRoundTrip(t *testing.T) {
	for _, name := range []string{
		"main", "cs", "r16", "qword", "and",
		"__fern_alloc", "__method_MapIter_key", "__closure_drop_f",
		"sort__sort_strings_asc_ci$wrap0", "fn_", "__fn_x",
	} {
		sym := Fn(name)
		if sym == name {
			t.Errorf("Fn(%q) left the name unmangled", name)
		}
		got, ok := Source(sym)
		if !ok || got != name {
			t.Errorf("Source(Fn(%q)) = %q, %v; want %q, true", name, got, ok, name)
		}
	}
}

// A name the backends emit outside the Fern-function namespace — a runtime
// helper, the process entry, an internal label — must not read back as a Fern
// function, or debug info would rename it and the emitter's two namespaces
// would not be separable after the fact.
func TestSourceRejectsNonFunctionSymbols(t *testing.T) {
	for _, sym := range []string{
		"__fern_alloc", "__memcpy", "__c_call1", "_start", "_main",
		"__closure_cell_f", ".LStr_0", "main", "",
	} {
		if got, ok := Source(sym); ok {
			t.Errorf("Source(%q) = %q, true; want it left alone", sym, got)
		}
	}
}
