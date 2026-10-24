package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

const ownEnumQualifierLib = `pub enum Kind { Text, Number(i32) }

pub function pick(): i32 {
    let t: Kind = Kind.Text;
    let n: Kind = Kind.Number(41);
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

func TestImportedEnumQualifierKeepsVisibility(t *testing.T) {
	for _, source := range []string{
		`function main(): i32 { let x = m.HiddenUnit.SecretUnit; return 0; }`,
		`function main(): i32 { let x = m.Hidden.Secret(1); return 0; }`,
		`function f(x: m.Visible): i32 { match (x) { m.Hidden.Secret(n) => { return n; }, _ => { return 0; } } } function main(): i32 { return 0; }`,
	} {
		dir := writeFiles(t, map[string]string{
			"main.fern": `import "./enumlib" as m;` + source,
			"enumlib.fern": `enum Hidden { Secret(i32) }
enum HiddenUnit { SecretUnit }
pub enum Visible { Public }`,
		})
		_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
		if err == nil || !strings.Contains(err.Error(), "is not exported") {
			t.Fatalf("private enum qualifier: got %v", err)
		}
	}
}

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

func TestOwnEnumQualifierStillCaughtWhenWrong(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"enumlib.fern": `pub enum Kind { Text, Number }
pub enum Other { Text, Blah }

pub function pick(): i32 {
    let k: Kind = Kind.Text;
    match (k) {
        Other.Text => { return 1; },
        Kind.Number => { return 2; }
    }
    return 0;
}

pub function nope(): i32 {
    let k: Kind = Kind.Missing;
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
