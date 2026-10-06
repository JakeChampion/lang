package ir

import "testing"

// An `@import(...) async function` lowers to an ExternFunc with Async
// set — the AST→IR link of the colorless WASI Preview-3 async-import
// vertical (docs/WASI-PREVIEW3-ASYNC-PLAN.md). A plain `@import`
// stays Async=false.
func TestExternFuncAsyncPropagates(t *testing.T) {
	src := `@import("test:dep/d", "compute") async function dep(): i32;
@import("test:dep/d", "plain") function plain(): i32;
function main(): i32 { return dep() + plain(); }`
	p := lowerSource(t, src)

	got := map[string]bool{}
	for _, ef := range p.Externs {
		got[ef.Name] = ef.Async
	}
	if a, ok := got["dep"]; !ok || !a {
		t.Errorf("dep: Async = %v (present=%v), want true", got["dep"], ok)
	}
	if a, ok := got["plain"]; !ok || a {
		t.Errorf("plain: Async = %v (present=%v), want false", got["plain"], ok)
	}
}
