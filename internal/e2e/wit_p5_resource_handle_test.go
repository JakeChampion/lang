package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestExternResourceHandle is the P5 baseline gate (docs/WIT-BRING-YOUR-OWN.md):
// a Fern program drives a WIT *resource* across the `@import` boundary with no
// new compiler work — a resource handle is an opaque i32 at the canonical ABI,
// so `subscribe-duration(d) -> own<pollable>` is `(i64)->i32` and
// `[method]pollable.ready(borrow<pollable>) -> bool` is `(i32)->i32`, both
// scalar shapes P4b already lowers and the composer already wires.
//
// It runs against REAL WASI — wasmtime is the host, so there's no
// cross-component resource-handle bridging (which deprecated `wasm-tools
// compose` doesn't do) and no composite result. A 0ns monotonic timer is
// immediately ready, so `ready()` on its pollable returns true. The world is a
// user-supplied one (P3, DecodeWorldBytes); since the vendored deps under
// cmd/fern/wit are stripped to fern's own subset (poll has only `block`,
// monotonic-clock only the type aliases), the test supplies the FULL standard
// interfaces — what any WASI 0.2 host already implements.
//
// Handle ownership/borrow type-safety and automatic drop are the *language*
// layer of P5; this establishes the runtime baseline. (The composer doesn't
// wire [resource-drop] yet, so the pollable is intentionally leaked.)
func TestExternResourceHandle(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// WIT dir: copy the vendored deps, then overwrite io/poll + monotonic-clock
	// with their FULL standard interfaces (the vendored ones are stripped).
	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	// A 0-nanosecond timer becomes ready essentially immediately: subscribe →
	// own<pollable> (a handle), block(handle) to wait for the deadline, then
	// ready(handle) → true. (wasmtime v46 no longer reports a 0ns-duration
	// pollable as ready on the first ready() check without a preceding
	// block()/poll, so block first for a version-robust check — this still
	// exercises the resource-handle bridging, which is the point.)
	const want = "poll-ok"
	src := `@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): i32;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: i32);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: i32): boolean;

function main(): i32 {
	var p: i32 = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("subscribe-duration")) || !bytes.Contains(core, []byte("[method]pollable.ready")) {
		t.Fatalf("core is missing the resource extern imports")
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "resource.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}

// TestExternResourceHandleTypes is the P5 *language-layer* gate
// (docs/WIT-BRING-YOUR-OWN.md): the same 0ns-timer-pollable program as
// TestExternResourceHandle, but written with the P5 handle vocabulary — a
// `resource Pollable;` declaration and `own Pollable` / `borrow Pollable`
// types instead of raw `i32`. It proves the new types are real (the checker
// enforces own-vs-borrow + "no plain i32 where a handle is required") yet
// erase to the i32 handle scalar before codegen, so the component still
// validates and runs identically under real WASI. Handles are still leaked
// (automatic drop is a later P5 slice).
func TestExternResourceHandleTypes(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Same FULL standard WIT setup as TestExternResourceHandle (vendored deps
	// are stripped to fern's subset, so supply the complete interfaces).
	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	// The P5 vocabulary: a `resource` declaration, `own Pollable` result, and
	// `borrow Pollable` parameter. `p` is an owned handle lent (own → borrow)
	// to ready().
	const want = "poll-ok"
	src := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("subscribe-duration")) || !bytes.Contains(core, []byte("[method]pollable.ready")) {
		t.Fatalf("core is missing the resource extern imports")
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "resource_typed.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}

// TestExternResourceHandleDrop is the P5 slice-2 gate (docs/WIT-BRING-YOUR-OWN.md):
// the world-driven composer wires a WIT `[resource-drop]` import. The program
// drops its pollable via an `@import("…","[resource-drop]pollable")` extern;
// the composer surfaces `pollable` as a component-level type (an alias from the
// poll instance) and emits a canon `resource.drop` referencing it — the
// ComposeFromWorldAuto path gated by hasResourceDropPrefix. The resource is
// released rather than leaked, validated + run under real WASI.
//
// The drop extern is the test vehicle for the composer change; automatic
// compiler-inserted drop is slice 3.
func TestExternResourceHandleDrop(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	const want = "poll-ok"
	src := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

@import("wasi:io/poll@0.2.0", "[resource-drop]pollable")
function drop_pollable(h: own Pollable): void;

function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	drop_pollable(p);
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("[resource-drop]pollable")) {
		t.Fatalf("core is missing the resource-drop import")
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (resource-drop): %v", err)
	}
	mine := filepath.Join(dir, "resource_drop.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}

// TestExternResourceHandleAutoDrop is the P5 slice-3 headline gate
// (docs/WIT-BRING-YOUR-OWN.md): AUTOMATIC drop. The program declares NO drop
// function — it just lets an owned `own Pollable` go out of scope. The compiler
// inserts `defer <drop>(p);`, synthesizes the `[resource-drop]pollable` import,
// and the world-driven composer (slice 2) wires the canon resource.drop, so the
// pollable is released. The borrowed handle passed to ready() is not dropped.
func TestExternResourceHandleAutoDrop(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	const want = "poll-ok"
	// No drop function — the handle is just declared and goes out of scope.
	src := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	// The compiler must have synthesized the resource-drop import even though
	// the program never names it.
	if !bytes.Contains(core, []byte("[resource-drop]pollable")) {
		t.Fatalf("core is missing the auto-inserted resource-drop import")
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (auto-drop): %v", err)
	}
	mine := filepath.Join(dir, "resource_autodrop.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
