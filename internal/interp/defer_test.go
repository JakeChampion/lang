package interp

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// evalProgramCapture runs `main` like evalProgram, but wires a buffer
// to i.Stdout so deferred print() side effects become observable
// (the TestPrintBuiltin capture pattern, factored out for the defer
// tests). Returns main's value and everything written to stdout.
func evalProgramCapture(t *testing.T, src string) (Value, string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, ok := i.Funcs["main"]; !ok {
		t.Fatalf("program has no main")
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	return v, buf.String()
}

// TestInterpDeferRunsAtExit — a `defer print(...)` fires when the
// enclosing function exits, *after* the body has finished running.
// The trailing "cleanup" line (printed last, after "body") is the
// observable proof the deferred expression was held until exit.
func TestInterpDeferRunsAtExit(t *testing.T) {
	_, out := evalProgramCapture(t, `function main(): void {
		defer print("cleanup");
		print("body");
	}`)
	if out != "body\ncleanup\n" {
		t.Errorf("defer-at-exit: stdout = %q, want \"body\\ncleanup\\n\"", out)
	}
}

// TestInterpDeferLIFO — multiple defers run last-registered-first.
// Registering a, b, c and printing "body" first must yield the body
// line followed by c, b, a (LIFO unwind of the defer frame).
func TestInterpDeferLIFO(t *testing.T) {
	_, out := evalProgramCapture(t, `function main(): void {
		defer print("a");
		defer print("b");
		defer print("c");
		print("body");
	}`)
	if out != "body\nc\nb\na\n" {
		t.Errorf("defer LIFO: stdout = %q, want \"body\\nc\\nb\\na\\n\"", out)
	}
}

// TestInterpDeferUnreachedIsNoop — a defer inside a conditional that
// never fires is never registered, so its expression never runs. The
// `if (false)` branch is skipped, so only the body line appears.
func TestInterpDeferUnreachedIsNoop(t *testing.T) {
	_, out := evalProgramCapture(t, `function main(): void {
		if (false) { defer print("never"); }
		print("body");
	}`)
	if out != "body\n" {
		t.Errorf("unreached defer: stdout = %q, want \"body\\n\" (defer must not fire)", out)
	}
}

// TestInterpDeferOnEarlyReturn — a defer registered before an early
// `return` still runs on that return path. The "cleanup" line proves
// the deferred expr fired even though control left via the first
// return; "unreached" must never print.
func TestInterpDeferOnEarlyReturn(t *testing.T) {
	v, out := evalProgramCapture(t, `function main(): i32 {
		defer print("cleanup");
		if (true) { return 1; }
		print("unreached");
		return 0;
	}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Errorf("early return: got %v, want 1", v)
	}
	if out != "cleanup\n" {
		t.Errorf("defer on early return: stdout = %q, want \"cleanup\\n\"", out)
	}
}

// TestInterpDeferReturnValueComputedFirst — the return value is
// captured before defers run, matching Go's "a defer can't change a
// non-named return value" semantics. The deferred call mutates a[0]
// to 99, but helper already evaluated `a[0]` (== 5) for its return
// slot, so main observes 5, not 99.
func TestInterpDeferReturnValueComputedFirst(t *testing.T) {
	v, _ := evalProgramCapture(t, `function set0(a: i32[]): void { a[0] = 99; }
	function helper(): i32 {
		var a: i32[] = [5];
		defer set0(a);
		return a[0];
	}
	function main(): i32 {
		return helper();
	}`)
	if n, ok := v.(Number); !ok || n != 5 {
		t.Errorf("return-before-defer: got %v, want 5 (defer mutation must not affect return value)", v)
	}
}
