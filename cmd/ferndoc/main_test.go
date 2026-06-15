package main

import (
	"strings"
	"testing"
)

// findModule returns the collected module with the given prefix/name,
// failing the test if it isn't present.
func findModule(t *testing.T, mods []module, prefix, name string) module {
	t.Helper()
	for _, m := range mods {
		if m.prefix == prefix && m.name == name {
			return m
		}
	}
	t.Fatalf("module %s/%s not found in collected set", prefix, name)
	return module{}
}

// ferndoc documents both the `std` and `core` import namespaces. core
// is where the trait foundation (core/cmp) and the Map/int helpers live,
// so dropping it would leave real importable modules undocumented.
func TestCollectModulesIncludesStdAndCore(t *testing.T) {
	mods, err := collectModules()
	if err != nil {
		t.Fatalf("collectModules: %v", err)
	}
	// A representative std module and all three core modules must be
	// present.
	findModule(t, mods, "std", "string")
	findModule(t, mods, "core", "cmp")
	findModule(t, mods, "core", "map")
	findModule(t, mods, "core", "int")
}

// A nested module (`std/wasm/convert.fern`) keeps its subdir in the
// module name + page title and gets a path-flattened, collision-free page
// filename — so it doesn't clash with a same-basename top-level module
// (`std/convert.fern`). Regression for the ferndoc basename collision.
func TestCollectModulesNestedNoCollision(t *testing.T) {
	mods, err := collectModules()
	if err != nil {
		// A collision (e.g. std/convert vs std/wasm/convert) returns an
		// error here — the bug this guards against.
		t.Fatalf("collectModules: %v", err)
	}
	top := findModule(t, mods, "std", "convert")
	nested := findModule(t, mods, "std", "wasm/convert")
	if top.fileName == nested.fileName {
		t.Errorf("page filenames collide: both %q", top.fileName)
	}
	if top.fileName != "convert" {
		t.Errorf("top-level fileName = %q, want unchanged \"convert\"", top.fileName)
	}
	if nested.fileName != "wasm_convert" {
		t.Errorf("nested fileName = %q, want \"wasm_convert\"", nested.fileName)
	}
	page, err := renderModule(nested)
	if err != nil {
		t.Fatalf("renderModule(std/wasm/convert): %v", err)
	}
	if !strings.Contains(page, "# `std/wasm/convert`") {
		t.Errorf("nested page missing the path-qualified `std/wasm/convert` heading\n%s", page)
	}
}

// The page title / heading must carry the right namespace so a reader
// (and the sidebar) can tell `core/cmp` from a hypothetical `std/cmp`.
func TestRenderModulePrefixesNamespace(t *testing.T) {
	mods, err := collectModules()
	if err != nil {
		t.Fatalf("collectModules: %v", err)
	}

	core := findModule(t, mods, "core", "cmp")
	page, err := renderModule(core)
	if err != nil {
		t.Fatalf("renderModule(core/cmp): %v", err)
	}
	for _, want := range []string{"title: core/cmp", "# `core/cmp`"} {
		if !strings.Contains(page, want) {
			t.Errorf("core/cmp page missing %q\n--- page ---\n%s", want, page)
		}
	}
	if strings.Contains(page, "title: std/core/cmp") {
		t.Errorf("core/cmp page has a doubled std/ prefix in its title")
	}

	std := findModule(t, mods, "std", "string")
	stdPage, err := renderModule(std)
	if err != nil {
		t.Fatalf("renderModule(std/string): %v", err)
	}
	if !strings.Contains(stdPage, "title: std/string") {
		t.Errorf("std/string page missing 'title: std/string'")
	}
}

// core/cmp is entirely traits + impls (no free functions), so without
// trait rendering its page would be empty. Assert the trait signatures
// and the grouped Implementations table both make it out.
func TestRenderModuleEmitsTraitsAndImpls(t *testing.T) {
	mods, err := collectModules()
	if err != nil {
		t.Fatalf("collectModules: %v", err)
	}
	page, err := renderModule(findModule(t, mods, "core", "cmp"))
	if err != nil {
		t.Fatalf("renderModule(core/cmp): %v", err)
	}

	wants := []string{
		"## `trait Display`",
		"function to_string(self: Self): string;", // method signature with Self
		"## `trait Default`",
		"function default(): Self;", // associated function (no self)
		"## Implementations",
		"| Trait | Implementing types |",
		"`Display`",
		"`i32`",
	}
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("core/cmp page missing %q", w)
		}
	}
}
