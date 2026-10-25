package modload

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

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
	want := manPath + `:5:1: error[E088]: dependency "b" of "a" is granted 'env' but "a" itself holds only [] (attenuation: a dependency may grant at most what it holds)` + "\n" +
		manPath + `:5:1: error[E088]: dependency "b" of "a" is granted 'net' but "a" itself holds only [] (attenuation: a dependency may grant at most what it holds)` + "\n" +
		manPath + `:6:1: error[E088]: dependency "c" of "a" is granted 'fs' but "a" itself holds only [] (attenuation: a dependency may grant at most what it holds)`
	if got := err.Error(); got != want {
		t.Errorf("violations:\ngot  %q\nwant %q", got, want)
	}
	errors, ok := err.(diag.Errors)
	if !ok || len(errors) != 3 {
		t.Fatalf("want three structured errors, got %T: %v", err, err)
	}
	for i, one := range errors {
		located, ok := one.(*manifest.Error)
		if !ok || located.Code() != "E088" || located.Path != manPath || located.Line != []int{5, 5, 6}[i] {
			t.Errorf("diagnostic %d: %#v", i, one)
		}
	}
}
