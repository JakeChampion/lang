package ciplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func fixture(t *testing.T) (*Graph, string) {
	t.Helper()
	root := t.TempDir()
	write(t, root, "go.mod", "module github.com/jakechampion/lang\n\ngo 1.27\n")
	config := Config{
		Version: 1, Global: []string{"Makefile", ".github/ci/**"},
		HostIndependentUnits: []string{"internal/testing/sourcelint"},
		Components: map[string]Component{
			"compiler":      {Inputs: []string{"compiler/**", "internal/stdlib/**", "bootstrap/**"}},
			"coreutils":     {Inputs: []string{"coreutils/**"}},
			"images":        {Inputs: []string{"fern-image/**"}},
			"documentation": {Inputs: []string{"docs/**", "*.md"}},
		},
		Targets: map[string]Target{
			"test-units":        {Go: []string{"@units"}},
			"test-coreutils":    {Components: []string{"coreutils"}, Compiler: true},
			"test-e2e-selfhost": {Go: []string{"internal/testing/e2ecompiler"}, Compiler: true},
			"test-e2e-other":    {Go: []string{"internal/testing/e2e"}, Compiler: true},
			"fern-image":        {Components: []string{"images"}, Compiler: true},
			"showcase":          {Programs: []string{"examples/showcase/*.fern.md"}, Compiler: true},
			"macos":             {Go: []string{"internal/testing/e2e", "internal/testing/e2ecompiler"}, Components: []string{"coreutils"}, Compiler: true},
			"test-affected":     {},
		},
		OpaqueTests: map[string]map[string][]string{
			"internal/testing/e2e": {"TestGrammar": {"**/*.fern"}},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, ".github/ci/targets.json", string(data))
	for file, source := range map[string]string{
		"internal/testing/e2e/grammar_test.go": `package e2e; import "testing"; func TestGrammar(t *testing.T) {}`,
		"internal/testing/e2ecompiler/apps_test.go": `package e2ecompiler
import ("testing"; tools "github.com/jakechampion/lang/internal/testing/support")
func TestCat(t *testing.T) { tools.RunCat() }
func TestImage(t *testing.T) { tools.RunImage() }
func TestReader(t *testing.T) { r := tools.NewReader(); r.ReadCat() }
func TestOther(t *testing.T) {}`,
		"internal/testing/support/paths.go": `package tools
const source = "../../../coreutils/cat.fern"
func RunCat() { consume(source) }
func RunImage() { consume("../../../fern-image/packages/png/png.fern") }
type Reader struct {}
func NewReader() Reader { return Reader{} }
func (r Reader) ReadCat() { consume("../../../coreutils/cat.fern") }
func consume(s string) {}`,
		"internal/testing/sourcelint/rules_test.go": `package sourcelint; import "testing"; func TestRules(t *testing.T) {}`,
		"internal/value/value.go":                   `package numbers; func Value() int { return 42 }`,
		"internal/value/value_test.go":              `package numbers; import "testing"; func TestValue(t *testing.T) {}`,
		"internal/app/app.go":                       `package app; import "github.com/jakechampion/lang/internal/value"; func Value() int { return numbers.Value() }`,
		"internal/app/app_test.go":                  `package app; import "testing"; func TestApp(t *testing.T) {}`,
		"internal/unrelated/unrelated_test.go":      `package unrelated; import "testing"; func TestOther(t *testing.T) {}`,
		"coreutils/cat.fern":                        "import \"./lib/io\";\n",
		"coreutils/lib/io.fern":                     "pub function io(): i32 { return 0; }\n",
		"fern-image/packages/png/png.fern":          "import \"../pixels/pixels\";\n",
		"fern-image/packages/pixels/pixels.fern":    "pub function pixel(): i32 { return 0; }\n",
		"fern-image/packages/qoi/qoi.fern":          "pub function qoi(): i32 { return 0; }\n",
		"examples/showcase/imaging.fern.md":         "```fern\nimport \"../../fern-image/packages/png/png\";\n```\n",
	} {
		write(t, root, file, source)
	}
	g, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return g, root
}

func write(t *testing.T, root, name, source string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFernConsumerClosure(t *testing.T) {
	g, _ := fixture(t)
	p := g.Select([]string{"coreutils/lib/io.fern"})
	if p.Full || !p.Lanes["test-coreutils"] || p.Lanes["test-e2e-selfhost"] || p.Lanes["fern-image"] {
		t.Fatalf("unexpected component lanes: %+v", p)
	}
	if !slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestCat") || slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestImage") {
		t.Fatalf("helper and constant references do not preserve the consumer closure: %+v", p.Tests)
	}
	if !slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestReader") {
		t.Fatal("imported receiver methods lost their source inputs")
	}
	if !slices.Contains(p.Tests["internal/testing/e2e"], "TestGrammar") {
		t.Fatal("opaque input declaration did not select the grammar check")
	}
}

func TestLiterateConsumerAndIndependentCodec(t *testing.T) {
	g, _ := fixture(t)
	for _, file := range []string{"fern-image/packages/png/png.fern", "fern-image/packages/pixels/pixels.fern"} {
		p := g.Select([]string{file})
		if !p.Lanes["showcase"] || !p.Lanes["fern-image"] || !slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestImage") {
			t.Fatalf("literate consumer lost an imported input: %s: %+v", file, p)
		}
	}
	p := g.Select([]string{"fern-image/packages/qoi/qoi.fern"})
	if !p.Lanes["fern-image"] || p.Lanes["showcase"] || p.Lanes["test-e2e-selfhost"] {
		t.Fatalf("independent codec reaches unrelated consumers: %+v", p.Lanes)
	}
}

func TestGoReverseImportsAndTestOnlyInputs(t *testing.T) {
	g, _ := fixture(t)
	p := g.Select([]string{"internal/value/value.go"})
	if p.UnitRepositoryOnly {
		t.Fatal("production package selection dropped its toolchains")
	}
	for _, pkg := range []string{"./internal/value", "./internal/app"} {
		if !slices.Contains(p.UnitPackages, pkg) {
			t.Errorf("Go source change does not select %s", pkg)
		}
	}
	if slices.Contains(p.UnitPackages, "./internal/unrelated") {
		t.Fatal("Go source change selects an independent package")
	}
	p = g.Select([]string{"internal/value/value_test.go"})
	if !slices.Contains(p.UnitPackages, "./internal/value") || slices.Contains(p.UnitPackages, "./internal/app") {
		t.Fatalf("test files propagate through production imports: %v", p.UnitPackages)
	}
}

func TestDeletedGoFileAndPackageResources(t *testing.T) {
	g, root := fixture(t)
	write(t, root, "internal/value/old.go", "package numbers")
	write(t, root, "internal/value/testdata/case.json", "{}")
	for _, file := range []string{"internal/value/old.go", "internal/value/testdata/case.json"} {
		p := g.Select([]string{file})
		if !slices.Contains(p.UnitPackages, "./internal/app") {
			t.Fatalf("removed files or resource inputs lost reverse dependencies: %s: %v", file, p.UnitPackages)
		}
	}
}

func TestSharedInputsAndUnknownPathsSelectBroadValidation(t *testing.T) {
	g, _ := fixture(t)
	for _, file := range []string{"Makefile", "new-component/main.fern", "../outside", "/absolute", "internal/newpkg/main.go"} {
		if !g.Select([]string{file}).Full {
			t.Errorf("%s must select broad validation", file)
		}
	}
	for _, file := range []string{"compiler/checker.fern", "internal/stdlib/std/audio/new.fern", "bootstrap/stage0.lock"} {
		p := g.Select([]string{file})
		if !p.Lanes["test-e2e-selfhost"] || !p.Lanes["fern-image"] || !p.Lint.Compiler {
			t.Errorf("%s lost a compiler consumer: %+v", file, p)
		}
	}
}

func TestDocumentationKeepsRepositoryChecksWithoutCompiler(t *testing.T) {
	g, _ := fixture(t)
	p := g.Select([]string{"docs/new.md"})
	if p.Full || p.Lint.Compiler || p.Lint.Fern || p.Lint.Go || p.Lanes["test-e2e-selfhost"] {
		t.Fatalf("documentation selects compiler work: %+v", p)
	}
	if !slices.Equal(p.UnitPackages, []string{"./internal/testing/sourcelint"}) || p.UnitHosts != "x86_64" || !p.UnitRepositoryOnly {
		t.Fatalf("repository checks do not retain the cheap host selection: %+v", p)
	}
}

func TestWorkflowAndCompositeActionInputs(t *testing.T) {
	g, root := fixture(t)
	write(t, root, ".github/workflows/fern-image.yml", "steps:\n  - uses: ./.github/actions/image-check\n  - run: scripts/image-qualify\n")
	write(t, root, ".github/actions/image-check/action.yml", "steps:\n  - run: scripts/image-reference\n")
	write(t, root, "scripts/image-reference", "#!/bin/sh\ntrue\n")
	write(t, root, "scripts/image-qualify", "#!/bin/sh\ntrue\n")
	for _, file := range []string{".github/actions/image-check/action.yml", "scripts/image-reference", "scripts/image-qualify"} {
		g.files[file] = true
	}
	for _, file := range []string{"scripts/image-reference", ".github/actions/image-check/action.yml"} {
		p := g.Select([]string{file})
		if p.Full || !p.Lanes["fern-image"] || p.Lanes["test-coreutils"] {
			t.Fatalf("recipe dependency did not select its consumer: %s: %+v", file, p.Lanes)
		}
	}
}

func TestOpaqueInputsMustNameALiveTest(t *testing.T) {
	_, root := fixture(t)
	data, err := os.ReadFile(filepath.Join(root, ".github/ci/targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config.OpaqueTests["internal/testing/e2e"]["TestRemoved"] = []string{"examples/**"}
	data, _ = json.Marshal(config)
	write(t, root, ".github/ci/targets.json", string(data))
	if _, err := Load(root); err == nil {
		t.Fatal("a dead opaque input declaration must fail")
	}
}

func TestPathPatterns(t *testing.T) {
	for _, tc := range []struct {
		pattern, file string
		want          bool
	}{
		{"*.md", "README.md", true},
		{"*.md", "examples/main.fern.md", false},
		{"**/*.fern", "main.fern", true},
		{"**/*.fern", "examples/nested/main.fern", true},
		{"coreutils/*.fern", "coreutils/lib/io.fern", false},
		{"coreutils/**", "coreutils/lib/io.fern", true},
	} {
		if got := Match(tc.pattern, tc.file); got != tc.want {
			t.Errorf("Match(%q, %q) = %v", tc.pattern, tc.file, got)
		}
	}
}

func TestPureCompilerTestChangesUseConsumerLane(t *testing.T) {
	g, _ := fixture(t)
	p := g.Select([]string{"internal/testing/e2ecompiler/apps_test.go"})
	if p.Full || p.Lanes["test-e2e-selfhost"] || !p.Lanes["test-affected"] || !p.Lint.Go {
		t.Fatalf("test declarations select unrelated compiler jobs: %+v", p)
	}
	if !slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestOther") {
		t.Fatal("the new test selection omits a changed test")
	}
}

func TestSharedCompilerTestHelperKeepsItsWholeLane(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "internal/testing/e2ecompiler/helper_test.go", "package e2ecompiler\nfunc helper() {}\n")
	g, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Select([]string{"internal/testing/e2ecompiler/helper_test.go"}).Lanes["test-e2e-selfhost"] {
		t.Fatal("a shared test helper must retain full package coverage")
	}
}

func TestPlatformVariantsRetainBothDefinitions(t *testing.T) {
	_, root := fixture(t)
	for _, platform := range []string{"linux", "darwin"} {
		write(t, root, "internal/testing/e2ecompiler/variant_"+platform+"_test.go", "package e2ecompiler\nimport \"testing\"\nfunc TestVariant(t *testing.T) {}\n")
	}
	g, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"linux", "darwin"} {
		p := g.Select([]string{"internal/testing/e2ecompiler/variant_" + platform + "_test.go"})
		if !slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestVariant") || !slices.Contains(p.DarwinTests["internal/testing/e2ecompiler"], "TestVariant") {
			t.Fatalf("a platform definition lost a consumer: %s: %+v", platform, p)
		}
		if p.MacosFull || slices.Contains(p.MacosGroups, "profile") || !slices.Contains(p.MacosGroups, "native") {
			t.Fatalf("native test edits select unrelated Darwin jobs: %+v", p)
		}
	}
}

func TestGoTestdataIsAResource(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "internal/value/testdata/broken.go", "this is deliberately invalid Go")
	g, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := g.Select([]string{"internal/value/testdata/broken.go"})
	if p.Full || !slices.Contains(p.UnitPackages, "./internal/app") {
		t.Fatalf("Go fixtures must follow resource dependencies: %+v", p)
	}
}

func TestComponentEdgesAndInvalidRegistries(t *testing.T) {
	g, root := fixture(t)
	g.Config.Components["viewer"] = Component{Depends: []string{"images"}}
	g.Config.Targets["viewer"] = Target{Components: []string{"viewer"}}
	if !g.Select([]string{"fern-image/packages/qoi/qoi.fern"}).Lanes["viewer"] {
		t.Fatal("a component dependency did not reach its consumer")
	}
	g.Config.Components["images"] = Component{Depends: []string{"viewer"}}
	data, _ := json.Marshal(g.Config)
	write(t, root, ".github/ci/targets.json", string(data))
	if _, err := Load(root); err == nil {
		t.Fatal("component cycles must fail")
	}
	write(t, root, ".github/ci/targets.json", `{"version":1,"targets":{"test":{}},"typo":true}`)
	if _, err := ReadConfig(root); err == nil {
		t.Fatal("unknown registry fields must fail")
	}
}

func TestCorpusImportsAndRemovedLiterateModules(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "internal/testing/e2ecompiler/corpus_test.go", `package e2ecompiler
import "testing"
func TestCorpus(t *testing.T) { consume("examples/showcase/") }`)
	write(t, root, "examples/showcase/removed.fern", "import \"../../fern-image/packages/gone/module\";\n")
	g, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"fern-image/packages/pixels/pixels.fern", "fern-image/packages/gone/module.fern.md"} {
		p := g.Select([]string{file})
		if !slices.Contains(p.Tests["internal/testing/e2ecompiler"], "TestCorpus") {
			t.Fatalf("corpus lost imported input %s: %+v", file, p)
		}
	}
}

func TestRepoPathArgumentsRemainOnePath(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "internal/testing/e2ecompiler/paths_test.go", `package e2ecompiler
import "testing"
func TestPaths(t *testing.T) { RepoPath("internal", "value", "testdata", "case.json") }`)
	g, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if g.inputHit(g.decls["internal/testing/e2ecompiler.TestPaths"].inputs, []string{"internal/app/app_test.go"}) {
		t.Fatal("path segments expanded into unrelated roots")
	}
	if !g.inputHit(g.decls["internal/testing/e2ecompiler.TestPaths"].inputs, []string{"internal/value/testdata/case.json"}) {
		t.Fatal("joined resource path was lost")
	}
}
