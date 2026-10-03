package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// Two MODULES declaring one variant name at DIFFERENT ordinals: `Wrap` is
// index 2 in a's Kind and index 0 in b's Kind. Neither module imports the
// other, so under module-scoped resolution (#6951) each module's bare
// `Wrap(v)` is unambiguous where it stands and both reach the IR with no
// qualifier.
//
// A constructor resolved by a program-wide scan instead of within its own
// module builds the other enum's tag (#6944: native's scan ran over a Go map
// and answered 79 or 69 between compiles of identical source).
//
// The accessors match with BARE arms deliberately: arms already resolve
// against the scrutinee (#6950), so the constructor is the half under test.
const variantOrdinalA = `enum Kind { Pad0, Pad1, Wrap(i32) }
pub function mk(v: i32): Kind { return Wrap(v); }
pub function get(k: Kind): i32 { match (k) { Wrap(v) => { return v; }, _ => { return -1; } } }
`

const variantOrdinalB = `enum Kind { Wrap(i32), Pad1, Pad2 }
pub function mk(v: i32): Kind { return Wrap(v); }
pub function get(k: Kind): i32 { match (k) { Wrap(v) => { return v; }, _ => { return -1; } } }
`

const variantOrdinalMain = `import "./a";
import "./b";
function main(): i32 { return a.get(a.mk(7)) * 10 + b.get(b.mk(9)); }
`

const variantOrdinalWant = 79

func writeVariantOrdinalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"a.fern":    variantOrdinalA,
		"b.fern":    variantOrdinalB,
		"main.fern": variantOrdinalMain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// Each module's constructor resolves within its own module on every compile.
func TestCrossModuleVariantOrdinalEmitIsDeterministic(t *testing.T) {
	assertEmitDeterministic(t, filepath.Join(writeVariantOrdinalProject(t), "main.fern"))
}

// The answer on both compiled targets. a's Wrap holding 7 and b's holding 9
// makes a constructor that built the other module's tag visible as a wrong
// digit or a -1.
func TestCrossModuleVariantOrdinalX86_64(t *testing.T) {
	dir := writeVariantOrdinalProject(t)
	if _, got := runFixtureX86_64(t, filepath.Join(dir, "main.fern"), ""); got != variantOrdinalWant {
		t.Errorf("exit code: got %d, want %d", got, variantOrdinalWant)
	}
}

func TestCrossModuleVariantOrdinalArm64(t *testing.T) {
	dir := writeVariantOrdinalProject(t)
	if _, got := runFixtureArm64(t, filepath.Join(dir, "main.fern"), ""); got != variantOrdinalWant {
		t.Errorf("exit code: got %d, want %d", got, variantOrdinalWant)
	}
}
