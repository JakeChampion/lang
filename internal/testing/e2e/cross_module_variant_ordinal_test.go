package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestCrossModuleVariantOrdinalEmitIsDeterministic(t *testing.T) {
	assertEmitDeterministic(t, filepath.Join(writeVariantOrdinalProject(t), "main.fern"))
}

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
