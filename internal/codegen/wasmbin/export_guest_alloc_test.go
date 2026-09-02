package wasmbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An `@export` function whose parameters cross the canonical ABI as `(ptr, len)`
// — a string or a numeric list — can only be called if the host can put those
// bytes somewhere inside the guest's linear memory. That means the module has to
// export an allocator.
//
// A plain `-emit core-module` build exported none: `cabi_realloc` was pinned and
// surfaced only for preview-2 component wrapping (ForceMemorySection) or a
// composite-result `@import` extern. So the browser playground's own shape —
// core module, no component, JS calling `fern#compile(src)` — left the host
// picking an address by hand. Small strings happened to land above the bump
// cursor and below the single initial page; 20 KB ran off the end of memory,
// with nothing in the module saying where the guest heap had reached.
//
// The allocator forwards to __fern_alloc, which grows memory, so exporting it
// replaces that guess with the canonical contract: call cabi_realloc(0, 0,
// align, n), write n bytes at the returned pointer, pass (ptr, n).

func exportsCabiRealloc(t *testing.T, src string) bool {
	t.Helper()
	bin, err := buildFromSource(t, src)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return exportExists(t, bin, "cabi_realloc")
}

func TestCoreModuleExportsAllocatorForStringParam(t *testing.T) {
	if !exportsCabiRealloc(t, `
@export("fern", "shout")
function shout(s: string): string { return s + "!"; }
function main(): i32 { return 0; }
`) {
		t.Error("a string-param @export must export cabi_realloc: the host has nowhere to write the argument bytes without it")
	}
}

func TestCoreModuleExportsAllocatorForListParam(t *testing.T) {
	if !exportsCabiRealloc(t, `
@export("fern", "total")
function total(xs: u8[]): i32 { return xs.len() as i32; }
function main(): i32 { return 0; }
`) {
		t.Error("a list-param @export must export cabi_realloc")
	}
}

// Controls. A string RESULT allocates its return area inside the guest (the
// wrapper calls __fern_alloc itself), so the host only reads — no allocator
// needed, and exporting one would be dead weight in every scalar module.
func TestCoreModuleOmitsAllocatorWithoutGuestWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"scalar-params-string-result", `
@export("fern", "digits")
function digits(n: i32): string { return "n"; }
function main(): i32 { return 0; }
`},
		{"scalar-params-scalar-result", `
@export("fern", "add")
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return 0; }
`},
		{"no-exports-at-all", `
function main(): i32 { return 0; }
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if exportsCabiRealloc(t, tc.src) {
				t.Error("nothing here needs the host to allocate in guest memory; cabi_realloc should not be exported")
			}
		})
	}
}

// The exported allocator has to actually serve the request, not just exist: a
// size past the module's single initial page must grow memory rather than trap,
// which is exactly the case the hand-picked scratch address could not survive.
func TestExportedAllocatorGrowsMemory(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	bin, err := buildFromSource(t, `
@export("fern", "shout")
function shout(s: string): string { return s + "!"; }
function main(): i32 { return 0; }
`)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	p := filepath.Join(t.TempDir(), "exporter.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	// 200000 bytes is three pages past the initial one.
	out, err := exec.Command(wasmtime, "run", "--invoke", "cabi_realloc", p, "0", "0", "1", "200000").CombinedOutput()
	if err != nil {
		t.Fatalf("cabi_realloc(0, 0, 1, 200000) failed: %v\n%s", err, out)
	}
	var ptr string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "warning:") {
			ptr = line
		}
	}
	if ptr == "" || ptr == "0" {
		t.Fatalf("allocator returned no usable pointer; wasmtime said:\n%s", out)
	}
}

// cabi_realloc is not self-contained: its body aligns the bump cursor and then
// forwards to __fern_alloc, which buildCabiReallocBody looks up BY NAME. An
// absent callee reads back as funcidx 0, so the emitted call lands on whatever
// function happens to sit there — main, in a small program — and the module
// fails to validate on the argument count.
//
// Nothing pinned that dependency. It stayed hidden because every caller so far
// pinned cabi_realloc for a program that also allocates: the preview-2 wrapping
// path (ForceMemorySection) is reached by wasi:http and wasi:cli programs, and
// the string-param exports above concatenate. A program that takes a string and
// only measures it allocates nothing at all.
func TestCabiReallocPinsItsAllocator(t *testing.T) {
	var needs runtimeNeeds
	needs.add("cabi_realloc")
	if !needs.set["__fern_alloc"] {
		t.Error("pinning cabi_realloc must pin __fern_alloc: its body calls it, and an unresolved callee silently becomes funcidx 0")
	}
}

// The end-to-end half of the above, on the two programs that reach it without
// allocating: a non-allocating string-param export, and the preview-1 command
// shape the browser playground builds (SynthStart + ForceMemorySection), which
// emitted an invalid module for `main` returning a constant.
func TestNonAllocatingModulesWithCabiReallocValidate(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	validate := func(t *testing.T, bin []byte) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "m.wasm")
		if err := os.WriteFile(p, bin, 0o644); err != nil {
			t.Fatalf("write module: %v", err)
		}
		if out, err := exec.Command(wasmtools, "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("module does not validate: %v\n%s", err, out)
		}
	}

	t.Run("a-string-param-export-that-never-allocates", func(t *testing.T) {
		bin, err := buildFromSource(t, `
@export("fern", "len_of")
function len_of(s: string): i32 { return s.len() as i32; }
function main(): i32 { return 0; }
`)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		validate(t, bin)
	})

	t.Run("the-preview-1-command-shape", func(t *testing.T) {
		prog, info := loadAndCheckModule(t, `function main(): i32 { return 42; }`)
		bin, err := BuildWithOptions(prog, info, BuildOptions{
			SynthStart:         true,
			ForceMemorySection: true,
		})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		validate(t, bin)
	})
}
