package symname

import "testing"

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
