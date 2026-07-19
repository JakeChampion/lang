package modload

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Attenuation (docs/PACKAGE-CAPABILITIES-BRIEF.md phase 3): a governed
// dependency may grant its own dependencies at most the capabilities it
// holds itself. Checked in capGrants at load time — the one place every
// dependency form's manifests are read transitively — so path, url,
// workspace, versioned and vendored deps are all covered by one check.

// (a) An attenuated chain is clean: root grants a [fs, net], a grants
// b the subset [fs].
func TestLoadAttenuationChainOK(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"fs\", \"net\"] }\n",
		"app/main.fern": `import "a";
function main(): i32 { return a.one(); }`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"fs\"] }\n",
		"a/a.fern": `import "b";
pub function one(): i32 { return b.one(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern":    "pub function one(): i32 { return 1; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatalf("attenuated chain must load cleanly, got: %v", err)
	}
	if got := prog.CapGrants[filepath.Join(root, "b")]; !reflect.DeepEqual(got, []string{"fs"}) {
		t.Errorf("b's grant: got %v, want [fs]", got)
	}
}

// (b) Amplification is a load error naming the granting manifest: a
// holds only [fs] but grants b 'net'.
func TestLoadAttenuationAmplificationErrors(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"fs\"] }\n",
		"app/main.fern": `import "a";
function main(): i32 { return a.one(); }`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"net\"] }\n",
		"a/a.fern": `import "b";
pub function one(): i32 { return b.one(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern":    "pub function one(): i32 { return 1; }",
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil {
		t.Fatal("expected an amplification error, got none")
	}
	want := `dependency "b" of "a" is granted 'net' but "a" itself holds only [fs] (attenuation: a dependency may grant at most what it holds)`
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error:\ngot  %q\nwant substring %q", got, want)
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "a", "fern.toml")) {
		t.Errorf("error should name the granting manifest: %v", err)
	}
}

// (c) An ungoverned middle imposes no ceiling: a has no capabilities
// key in the root's manifest, so its grant of b stands (the phase-2
// warn-and-allow story for a itself is unchanged and lives at the
// enforcement layer, not here).
func TestLoadAttenuationUngovernedMiddle(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\" }\n",
		"app/main.fern": `import "a";
function main(): i32 { return a.one(); }`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"net\"] }\n",
		"a/a.fern": `import "b";
pub function one(): i32 { return b.one(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern":    "pub function one(): i32 { return 1; }",
	})
	prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err != nil {
		t.Fatalf("ungoverned grantor must impose no ceiling, got: %v", err)
	}
	if got := prog.CapGrants[filepath.Join(root, "b")]; !reflect.DeepEqual(got, []string{"net"}) {
		t.Errorf("b's grant: got %v, want [net]", got)
	}
}

// (d) The root's own grants are unrestricted — nothing declares the
// root, so it is ungoverned and holds everything.
func TestLoadAttenuationRootUnrestricted(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"env\", \"fs\", \"net\", \"random\", \"subprocess\", \"time\"] }\n",
		"app/main.fern": `import "a";
function main(): i32 { return a.one(); }`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n",
		"a/a.fern":    "pub function one(): i32 { return 1; }",
	})
	if _, _, err := Load(filepath.Join(root, "app", "main.fern")); err != nil {
		t.Fatalf("root grants are unrestricted, got: %v", err)
	}
}

// (e) Diamond: b reachable via a1 and a2 gets the UNION of grants, but
// each granting edge is checked against ITS grantor's holdings
// independently.
func TestLoadAttenuationDiamond(t *testing.T) {
	base := map[string]string{
		"app/main.fern": `import "a1";
import "a2";
function main(): i32 { return a1.one() + a2.two(); }`,
		"a1/fern.toml": "[package]\nname = \"a1\"\nlib = \"a1.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"fs\"] }\n",
		"a1/a1.fern": `import "b";
pub function one(): i32 { return b.one(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern":    "pub function one(): i32 { return 1; }",
	}
	appToml := "[package]\nname = \"app\"\n[dependencies]\na1 = { path = \"../a1\", capabilities = [\"fs\"] }\na2 = { path = \"../a2\", capabilities = [\"net\"] }\n"

	t.Run("union of passing edges", func(t *testing.T) {
		files := map[string]string{}
		for k, v := range base {
			files[k] = v
		}
		files["app/fern.toml"] = appToml
		files["a2/fern.toml"] = "[package]\nname = \"a2\"\nlib = \"a2.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"net\"] }\n"
		files["a2/a2.fern"] = `import "b";
pub function two(): i32 { return b.one() + 1; }`
		root := writeTree(t, files)
		prog, _, err := Load(filepath.Join(root, "app", "main.fern"))
		if err != nil {
			t.Fatalf("both edges are within their grantors' holdings, got: %v", err)
		}
		if got := prog.CapGrants[filepath.Join(root, "b")]; !reflect.DeepEqual(got, []string{"fs", "net"}) {
			t.Errorf("b's diamond union: got %v, want [fs net]", got)
		}
	})

	t.Run("sibling grant does not excuse an amplifying edge", func(t *testing.T) {
		// a1 legitimately grants b 'fs'; a2 (holding only [net]) granting
		// b 'fs' is still an error — edges are checked independently, not
		// against the union of all grantors.
		files := map[string]string{}
		for k, v := range base {
			files[k] = v
		}
		files["app/fern.toml"] = appToml
		files["a2/fern.toml"] = "[package]\nname = \"a2\"\nlib = \"a2.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"fs\"] }\n"
		files["a2/a2.fern"] = `import "b";
pub function two(): i32 { return b.one() + 1; }`
		root := writeTree(t, files)
		_, _, err := Load(filepath.Join(root, "app", "main.fern"))
		if err == nil {
			t.Fatal("expected a2's amplifying edge to error, got none")
		}
		want := `dependency "b" of "a2" is granted 'fs' but "a2" itself holds only [net] (attenuation: a dependency may grant at most what it holds)`
		if got := err.Error(); !strings.Contains(got, want) {
			t.Errorf("error:\ngot  %q\nwant substring %q", got, want)
		}
	})
}

// Multiple violations report all at once, sorted by granting manifest
// dir, then dependency name, then capability.
func TestLoadAttenuationMultipleViolationsDeterministic(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [] }\n",
		"app/main.fern": `import "a";
function main(): i32 { return a.one(); }`,
		"a/fern.toml": "[package]\nname = \"a\"\nlib = \"a.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"net\", \"env\"] }\nc = { path = \"../c\", capabilities = [\"fs\"] }\n",
		"a/a.fern": `import "b";
import "c";
pub function one(): i32 { return b.one() + c.one(); }`,
		"b/fern.toml": "[package]\nname = \"b\"\nlib = \"b.fern\"\n",
		"b/b.fern":    "pub function one(): i32 { return 1; }",
		"c/fern.toml": "[package]\nname = \"c\"\nlib = \"c.fern\"\n",
		"c/c.fern":    "pub function one(): i32 { return 2; }",
	})
	_, _, err := Load(filepath.Join(root, "app", "main.fern"))
	if err == nil {
		t.Fatal("expected amplification errors, got none")
	}
	manPath := filepath.Join(root, "a", "fern.toml")
	want := manPath + `: dependency "b" of "a" is granted 'env' but "a" itself holds only [] (attenuation: a dependency may grant at most what it holds)` + "\n" +
		manPath + `: dependency "b" of "a" is granted 'net' but "a" itself holds only [] (attenuation: a dependency may grant at most what it holds)` + "\n" +
		manPath + `: dependency "c" of "a" is granted 'fs' but "a" itself holds only [] (attenuation: a dependency may grant at most what it holds)`
	if got := err.Error(); got != want {
		t.Errorf("violations:\ngot  %q\nwant %q", got, want)
	}
}
