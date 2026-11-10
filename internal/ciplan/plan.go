package ciplan

import (
	"path"
	"slices"
	"sort"
	"strings"
)

func (g *Graph) Full(reason string) Plan {
	p := Plan{Version: 1, Full: true, Reason: reason, Components: []string{}, Lanes: map[string]bool{}, UnitPackages: []string{}, UnitGroups: []string{"heavy", "rest"}, UnitHosts: "x86_64 aarch64", Tests: map[string][]string{}, DarwinTests: map[string][]string{}, MacosGroups: []string{"profile", "coreutils", "native", "fixtures", "examples"}, MacosFull: true, Lint: Lint{true, true, true, true, true}}
	for lane := range g.Config.Targets {
		p.Lanes[lane] = lane != "test-affected"
	}
	return p
}

func (g *Graph) FilterTable() map[string]Filter {
	table := map[string]Filter{}
	for lane, target := range g.Config.Targets {
		inputs := map[string]bool{}
		for _, input := range append(append(append([]string{}, g.Config.Global...), target.Inputs...), target.Programs...) {
			inputs[input] = true
		}
		seen := map[string]bool{}
		var component func(string)
		component = func(name string) {
			if seen[name] {
				return
			}
			seen[name] = true
			c := g.Config.Components[name]
			for _, input := range c.Inputs {
				inputs[input] = true
			}
			for _, dep := range c.Depends {
				component(dep)
			}
		}
		if target.Compiler {
			component("compiler")
		}
		for _, name := range target.Components {
			component(name)
		}
		for _, name := range target.Go {
			packages := g.goRoots(name)
			if name == "@units" {
				packages = nil
				for name := range g.packages {
					if unitPackage(name) {
						packages = append(packages, name)
					}
				}
			}
			for _, pkg := range packages {
				for file := range g.packageFiles(pkg, true, map[string]bool{}) {
					inputs[path.Dir(file)+"/**"] = true
				}
			}
		}
		var paths []string
		for input := range inputs {
			for file := range g.files {
				if Match(input, file) {
					paths = append(paths, input)
					break
				}
			}
		}
		sort.Strings(paths)
		table[lane] = Filter{Paths: paths}
	}
	return table
}

func (g *Graph) Select(changes []string) Plan {
	p := g.Full("")
	p.Full = false
	p.Changed = append([]string{}, changes...)
	p.MacosGroups = []string{}
	p.MacosFull = false
	p.Lint = Lint{}
	for lane := range p.Lanes {
		p.Lanes[lane] = false
	}
	if len(changes) == 0 {
		p.Reason = "no changed paths"
		return p
	}
	if MatchesAny(g.Config.Global, changes) {
		return g.Full("shared build or CI input")
	}
	affected := map[string]bool{}
	for _, file := range changes {
		if file == "" || strings.Contains(file, "\\") || strings.HasPrefix(file, "/") || path.Clean(file) != file || strings.HasPrefix(file, "../") {
			return g.Full("invalid changed path")
		}
		known := false
		for name, component := range g.Config.Components {
			if MatchesAny(component.Inputs, []string{file}) {
				affected[name] = true
				known = true
			}
		}
		if goSource(file) {
			_, known = g.packages[path.Dir(file)]
			if !known {
				return g.Full("new or removed Go package")
			}
			p.Lint.Go = true
		}
		if strings.HasPrefix(file, "internal/") || strings.HasPrefix(file, "cmd/") {
			for dir := path.Dir(file); dir != "."; dir = path.Dir(dir) {
				if _, exists := g.packages[dir]; exists {
					known = true
					break
				}
			}
		}
		if strings.HasSuffix(file, ".fern") || strings.HasSuffix(file, ".fern.md") {
			p.Lint.Fern = true
		}
		if strings.HasPrefix(file, "scripts/") || strings.HasPrefix(file, ".github/") || strings.HasPrefix(file, "internal/") || strings.HasPrefix(file, "cmd/") || strings.HasPrefix(file, "tools/") {
			// Unresolved support inputs select every target instead of no target.
			if !known && !goSource(file) && !g.usedByWorkflow(file) {
				return g.Full("unclassified support input: " + file)
			}
			known = true
		}
		if !known {
			return g.Full("unclassified input: " + file)
		}
	}
	for changed := true; changed; {
		changed = false
		for name, component := range g.Config.Components {
			for _, dep := range component.Depends {
				if affected[dep] && !affected[name] {
					affected[name] = true
					changed = true
				}
			}
		}
	}
	compiler := affected["compiler"]
	p.NeedsGNU = affected["coreutils"] || MatchesAny([]string{"internal/testing/e2e/**", "internal/testing/e2ecompiler/**"}, changes)
	p.Lint.Compiler = compiler
	p.Lint.Generators = compiler || affected["generators"]
	p.Lint.Cache = compiler || MatchesAny([]string{"internal/testing/e2eharness/**", "scripts/check-fern-test-cache"}, changes)
	for name := range affected {
		p.Components = append(p.Components, name)
	}
	sort.Strings(p.Components)
	sourceChanges := g.sourceChanges(changes)
	hits := g.affectedDeclarations(sourceChanges)
	for name, pkg := range g.packages {
		if !unitPackage(name) || !slices.ContainsFunc(pkg.files, func(file string) bool { return strings.HasSuffix(file, "_test.go") }) {
			continue
		}
		entryHit := len(g.affectedTests(name, sourceChanges, hits)) > 0
		for decl := range pkg.decls {
			if (strings.HasPrefix(decl, "Fuzz") || strings.HasPrefix(decl, "Example")) && hits[name+"."+decl] {
				entryHit = true
			}
		}
		if compiler || name == "internal/testing/sourcelint" || g.inputHit(g.packageFiles(name, true, map[string]bool{}), changes) || entryHit {
			p.UnitPackages = append(p.UnitPackages, "./"+name)
		}
	}
	sort.Strings(p.UnitPackages)
	independent := len(p.UnitPackages) > 0
	for _, pkg := range p.UnitPackages {
		independent = independent && slices.Contains(g.Config.HostIndependentUnits, strings.TrimPrefix(pkg, "./"))
	}
	p.UnitRepositoryOnly = independent
	if independent {
		p.UnitHosts = "x86_64"
	}
	p.UnitGroups = []string{}
	for _, pkg := range p.UnitPackages {
		group := "rest"
		if pkg == "./internal/syntax/printer" {
			group = "heavy"
		}
		if !slices.Contains(p.UnitGroups, group) {
			p.UnitGroups = append(p.UnitGroups, group)
		}
	}
	sort.Strings(p.UnitGroups)
	for lane, target := range g.Config.Targets {
		if lane == "test-affected" {
			continue
		}
		selected := compiler && target.Compiler
		macosFull := compiler || affected["fixtures"]
		for _, component := range target.Components {
			selected = selected || affected[component]
		}
		workflow := MatchesAny(target.Inputs, changes) || g.workflowHit(lane, changes)
		selected = selected || workflow
		macosFull = macosFull || workflow
		for _, pattern := range target.Go {
			roots := g.goRoots(pattern)
			if pattern == "@units" {
				roots = []string{"@units"}
			}
			for _, pkg := range roots {
				if pkg == "@units" {
					selected = selected || len(p.UnitPackages) > 0
				} else {
					inputs := g.packageFiles(pkg, true, map[string]bool{})
					goHit := g.inputHit(inputs, sourceChanges)
					if (pkg == "internal/testing/e2e" || pkg == "internal/testing/e2ecompiler") && goHit {
						onlyTests := true
						for _, file := range changes {
							if g.inputHit(inputs, []string{file}) && (path.Dir(file) != pkg || !g.packages[pkg].testOnly[file]) {
								onlyTests = false
							}
						}
						goHit = !onlyTests
					}
					selected = selected || goHit
					macosFull = macosFull || goHit
				}
			}
		}
		inputs := map[string]bool{}
		for file := range g.files {
			if MatchesAny(target.Programs, []string{file}) {
				inputs[file] = true
			}
		}
		selected = selected || g.inputHit(inputs, sourceChanges)
		p.Lanes[lane] = selected
		if lane == "macos" && selected {
			p.MacosFull = macosFull
			if macosFull {
				p.MacosGroups = []string{"profile", "coreutils", "native", "fixtures", "examples"}
			} else {
				if affected["coreutils"] {
					p.MacosGroups = append(p.MacosGroups, "coreutils")
				}
				if g.inputHit(inputs, sourceChanges) {
					p.MacosGroups = append(p.MacosGroups, "examples")
				}
			}
		}
	}
	if !compiler {
		for _, pkg := range []string{"internal/testing/e2e", "internal/testing/e2ecompiler"} {
			if tests := g.affectedTests(pkg, sourceChanges, hits); len(tests) > 0 {
				p.Tests[pkg] = tests
			}
		}
		// Dedicated workflows retain tests with special environments or budgets.
		for pkg, tests := range p.Tests {
			var ordinary []string
			for _, test := range tests {
				if owner := g.special[test]; owner != "" {
					p.Lanes[owner] = true
					continue
				}
				linux, darwinOnly := g.testPlatforms(pkg, test)
				if strings.Contains(test, "Darwin") || darwinOnly {
					p.Lanes["macos"] = true
					p.DarwinTests[pkg] = append(p.DarwinTests[pkg], test)
					group := "native"
					if test == "TestSelfHostDarwinProfile" {
						group = "profile"
					}
					if !slices.Contains(p.MacosGroups, group) {
						p.MacosGroups = append(p.MacosGroups, group)
					}
					if !linux {
						continue
					}
				}
				if g.dedicated(test) {
					if pkg == "internal/testing/e2ecompiler" || strings.HasPrefix(test, "TestSelfHost") || strings.HasPrefix(test, "TestFernFixtures") {
						p.Lanes["test-e2e-selfhost"] = true
					} else {
						for _, lane := range []string{"test-e2e-x86_64", "test-e2e-arm64", "test-e2e-wasm", "test-e2e-other", "test-e2e-differential"} {
							p.Lanes[lane] = true
						}
					}
					continue
				}
				ordinary = append(ordinary, test)
			}
			if len(ordinary) == 0 {
				delete(p.Tests, pkg)
			} else {
				p.Tests[pkg] = ordinary
			}
		}
		if len(p.Tests) > 0 {
			p.Lanes["test-affected"] = true
		}
	}
	for pkg, tests := range p.Tests {
		var remaining []string
		for _, test := range tests {
			covered := pkg == "internal/testing/e2ecompiler" && p.Lanes["test-e2e-selfhost"]
			if pkg == "internal/testing/e2e" {
				switch {
				case strings.HasPrefix(test, "TestSelfHost"), strings.HasPrefix(test, "TestFernFixtures"), test == "TestGrammarDerivesSelfHostParsedSources":
					covered = p.Lanes["test-e2e-selfhost"]
				case strings.HasPrefix(test, "TestArm64"):
					covered = p.Lanes["test-e2e-arm64"]
				case strings.HasPrefix(test, "TestX86_64"):
					covered = p.Lanes["test-e2e-x86_64"]
				case strings.HasPrefix(test, "TestWASM"), strings.HasPrefix(test, "TestWasm"):
					covered = p.Lanes["test-e2e-wasm"]
				case strings.HasPrefix(test, "TestDifferential"):
					covered = p.Lanes["test-e2e-differential"]
				default:
					covered = p.Lanes["test-e2e-other"]
				}
			}
			if !covered {
				remaining = append(remaining, test)
			}
		}
		if len(remaining) == 0 {
			delete(p.Tests, pkg)
		} else {
			p.Tests[pkg] = remaining
		}
	}
	p.Lanes["test-affected"] = len(p.Tests) > 0
	sort.Strings(p.MacosGroups)
	p.Reason = "component inputs, Go imports, Fern imports, and test consumers"
	return p
}

func (g *Graph) dedicated(test string) bool {
	for _, prefix := range []string{"TestDifferential", "TestFernFixtures", "TestSeccomp", "TestSelfHostStdTest", "TestSelfHostCryptoSuites", "TestSelfHostCheckAgreesWithGoOnStdlib", "TestSelfHostKernel", "TestSelfHostWarmStockDriver"} {
		if strings.HasPrefix(test, prefix) {
			return true
		}
	}
	return g.special[test] != ""
}
