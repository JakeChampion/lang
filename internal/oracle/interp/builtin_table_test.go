package interp

import (
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// stdlibOverrides are the Builtins entries that answer for a stdlib function
// rather than a builtin: its body uses raw-memory operations the interpreter
// cannot model, so the interpreter registers the function by its bare and its
// module-mangled name.
var stdlibOverrides = map[string]bool{
	"int_to_string":            true,
	"int__int_to_string":       true,
	"int____int_to_string_u64": true,
}

// TestInterpImplementsEveryCheckerBuiltin holds the interpreter's builtin
// table to the checker's, in both directions: a builtin the checker accepts
// and the interpreter lacks stops an oracle run with "undefined function", and
// one the interpreter answers that the checker does not know is unreachable.
// `__`-prefixed names are internals with no surface spelling.
func TestInterpImplementsEveryCheckerBuiltin(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	builtins := New().Builtins
	var missing, unknown []string
	for name := range info.FuncSigs {
		// The checker renames config_get to env before a program runs.
		if name == "main" || name == "config_get" || strings.HasPrefix(name, "__") {
			continue
		}
		if builtins[name] == nil {
			missing = append(missing, name)
		}
	}
	for name := range builtins {
		if strings.HasPrefix(name, "__") || stdlibOverrides[name] {
			continue
		}
		if info.FuncSigs[name] == nil {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	if len(missing) > 0 {
		t.Errorf("builtins the checker accepts and the interpreter does not implement: %s", strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		t.Errorf("interpreter builtins the checker does not know: %s", strings.Join(unknown, ", "))
	}
	if len(info.FuncSigs) < 100 || len(builtins) < 100 {
		t.Fatalf("the tables have %d and %d entries; the probe no longer reaches them", len(info.FuncSigs), len(builtins))
	}
}
