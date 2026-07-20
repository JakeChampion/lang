package caps_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/caps"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/parser"
)

// checkerBuiltins enumerates the checker's builtin registry: the
// FuncSigs of a minimal program are exactly the pre-declared builtins
// plus the program's own decls.
func checkerBuiltins(t *testing.T) map[string]bool {
	t.Helper()
	prog, err := parser.Parse("function main(): void {}")
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for name := range info.FuncSigs {
		if name == "main" {
			continue
		}
		names[name] = true
	}
	return names
}

// The completeness contract, checker side: every builtin the checker
// pre-declares is either capability-tagged, explicitly ungated, or a
// `__`-prefixed compiler-internal helper. A new I/O builtin
// registered in the checker without a caps classification fails here.
func TestInventoryCoversCheckerRegistry(t *testing.T) {
	for name := range checkerBuiltins(t) {
		if strings.HasPrefix(name, "__") {
			continue
		}
		_, tagged := caps.BuiltinCaps[name]
		if !tagged && !caps.Ungated[name] {
			t.Errorf("checker builtin %q is unclassified: add it to caps.BuiltinCaps or caps.Ungated", name)
		}
		if tagged && caps.Ungated[name] {
			t.Errorf("builtin %q is both capability-tagged and ungated", name)
		}
	}
}

// Same contract, interpreter side. Names containing `__` are skipped:
// the interp registry also carries `__method_*` shims (authority
// lives on the constructor that produced the receiver, not the
// method) and modload-mangled stdlib aliases like int__int_to_string.
func TestInventoryCoversInterpRegistry(t *testing.T) {
	for name := range interp.New().Builtins {
		if strings.Contains(name, "__") {
			continue
		}
		_, tagged := caps.BuiltinCaps[name]
		if !tagged && !caps.Ungated[name] {
			t.Errorf("interp builtin %q is unclassified: add it to caps.BuiltinCaps or caps.Ungated", name)
		}
		if tagged && caps.Ungated[name] {
			t.Errorf("builtin %q is both capability-tagged and ungated", name)
		}
	}
}

// The reverse direction: every classified name must exist in at least
// one registry (catches a builtin rename stranding a stale table
// entry), and every caps.BuiltinCaps value must be in the v1 vocabulary.
func TestInventoryNamesAreReal(t *testing.T) {
	registry := checkerBuiltins(t)
	for name := range interp.New().Builtins {
		registry[name] = true
	}
	vocab := map[string]bool{}
	for _, c := range caps.Capabilities {
		vocab[c] = true
	}
	for name, c := range caps.BuiltinCaps {
		if !registry[name] {
			t.Errorf("caps.BuiltinCaps entry %q is not a registered builtin", name)
		}
		if !vocab[c] {
			t.Errorf("caps.BuiltinCaps[%q] = %q is not in the v1 vocabulary %v", name, c, caps.Capabilities)
		}
	}
	for name := range caps.Ungated {
		if !registry[name] {
			t.Errorf("caps.Ungated entry %q is not a registered builtin", name)
		}
	}
}
