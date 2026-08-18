package modload_test

// Self-qualified own-enum references inside a non-entry module (#6951):
// `MyEnum.Variant` where MyEnum is declared by the module doing the
// referring. It is the same construct that works in a single file and in
// the entry module, and it broke as soon as the code moved into an
// import, because the rewriter mangles the enum decl to `<mod>__MyEnum`
// but left the qualifier spelled as the user wrote it.
//
// In expression position the qualifier is a plain Ident target, so the
// checker looked up `MyEnum` in c.info.Enums, missed, and fell through
// to E001 "undefined identifier". In pattern position the qualifier
// reached checkVariantQualifier unmangled, missed the enum branch the
// same way, and was diagnosed as a MODULE qualifier: E029 "names module
// MyEnum, but enum mod__MyEnum lives in module …".

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
)

const ownEnumQualifierLib = `pub enum Kind { Text, Number(i32) }

pub function pick(): i32 {
    var t: Kind = Kind.Text;
    var n: Kind = Kind.Number(41);
    match (n) {
        Kind.Text => { return 1; },
        Kind.Number(v) => { return v + 1; }
    }
    return 0;
}
`

const ownEnumQualifierMain = `import "./enumlib";
function main(): i32 { return enumlib.pick(); }
`

// TestOwnEnumQualifierInImportedModule pins that both positions check
// clean and that the match arm actually selects — a qualifier that
// merely stopped erroring but no longer matched its variant would be a
// worse bug than the one it replaced.
func TestOwnEnumQualifierInImportedModule(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"enumlib.fern": ownEnumQualifierLib,
		"main.fern":    ownEnumQualifierMain,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("checker rejected a module self-qualifying its own enum: %v", err)
	}

	i := interp.New()
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("interp: %v", err)
	}
	n, ok := v.(interp.Number)
	if !ok {
		t.Fatalf("main returned %T, want a number", v)
	}
	if int(n) != 42 {
		t.Errorf("main() = %d, want 42 (the Kind.Number(41) arm)", int(n))
	}
}

// TestOwnEnumQualifierStillCaughtWhenWrong pins that mangling the
// qualifier did not turn the checks it feeds into no-ops: a qualifier
// naming a DIFFERENT own enum, and a variant the qualified enum does
// not declare, must both still be rejected.
func TestOwnEnumQualifierStillCaughtWhenWrong(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"enumlib.fern": `pub enum Kind { Text, Number }
pub enum Other { Text, Blah }

pub function pick(): i32 {
    var k: Kind = Kind.Text;
    match (k) {
        Other.Text => { return 1; },
        Kind.Number => { return 2; }
    }
    return 0;
}

pub function nope(): i32 {
    var k: Kind = Kind.Missing;
    return 0;
}
`,
		"main.fern": `import "./enumlib";
function main(): i32 { return enumlib.pick() + enumlib.nope(); }
`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = checker.Check(prog)
	if err == nil {
		t.Fatal("expected E029 for the mismatched qualifier and E036 for the unknown variant")
	}
	msg := err.Error()
	for _, want := range []string{
		"does not match scrutinee enum",
		`has no variant "Missing"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostics missing %q:\n%s", want, msg)
		}
	}
}
