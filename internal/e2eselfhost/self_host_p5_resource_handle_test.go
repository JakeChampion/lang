package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExternResourceHandle is the self-host P5 baseline gate
// (docs/WIT-BRING-YOUR-OWN.md): the self-hosted wasm backend drives a WIT
// resource across the `@import` boundary with no resource-specific codegen —
// a handle is an opaque i32, so `subscribe-duration -> own<pollable>` and
// `[method]pollable.ready -> bool` are ordinary scalar imports the self-host
// emitter already lowers. Mirror of the Go TestExternResourceHandle: a 0ns
// monotonic timer's pollable is immediately ready, run against real WASI.
func TestSelfHostExternResourceHandle(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Full standard WIT for poll + monotonic-clock (the vendored deps are
	// stripped to fern's subset).
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

	// Self-host backend: emit the core from the resource-driving program.
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_runio_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "poll-ok"
	prog := `@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
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
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	if !bytes.Contains(watBytes, []byte("subscribe-duration")) || !bytes.Contains(watBytes, []byte("[method]pollable.ready")) {
		t.Errorf("emitted core is missing the resource extern imports")
	}
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "resource.component.wasm")
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

// TestSelfHostExternResourceHandleTypes is the self-host P5 *language-layer*
// gate (docs/WIT-BRING-YOUR-OWN.md): the same 0ns-timer program as
// TestSelfHostExternResourceHandle, written with the P5 handle vocabulary — a
// `resource Pollable;` declaration and `own Pollable` / `borrow Pollable`
// types. The self-host parser erases the handle types to i32 (own/borrow → i32
// in parse_type_name) and consumes the resource declaration, so the emitted
// core is the same scalar-handle shape the raw-i32 baseline produces, and the
// composed component runs identically under real WASI.
func TestSelfHostExternResourceHandleTypes(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_runio_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "poll-ok"
	prog := `@import("wasi:io/poll@0.2.0", "pollable")
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
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	if !bytes.Contains(watBytes, []byte("subscribe-duration")) || !bytes.Contains(watBytes, []byte("[method]pollable.ready")) {
		t.Errorf("emitted core is missing the resource extern imports")
	}
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "resource_typed.component.wasm")
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

// TestSelfHostExternResourceHandleDrop is the self-host parity gate for P5
// slice 2 (docs/WIT-BRING-YOUR-OWN.md): the self-host backend emits a
// `[resource-drop]pollable` core import, and the (Go) world-driven composer
// surfaces the resource + wires the canon resource.drop. Mirror of the Go
// TestExternResourceHandleDrop — the pollable is dropped, run under real WASI.
func TestSelfHostExternResourceHandleDrop(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_runio_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "poll-ok"
	prog := `@import("wasi:io/poll@0.2.0", "pollable")
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
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	if !bytes.Contains(watBytes, []byte("[resource-drop]pollable")) {
		t.Errorf("emitted core is missing the resource-drop import")
	}
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (resource-drop): %v", err)
	}
	mine := filepath.Join(dir, "resource_drop.component.wasm")
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

// TestSelfHostExternResourceHandleAutoDrop is the self-host parity gate for P5
// slice 3 (docs/WIT-BRING-YOUR-OWN.md): AUTOMATIC drop. The program declares NO
// drop function — the self-host compiler (parser.fern lower_defers_prepass_module)
// inserts `defer <drop>(p);` for the owned `own Pollable` local, lower_defers
// expands it on the return path, and the synthesized `[resource-drop]pollable`
// import is emitted in the core. The Go world-driven composer (slice 2) then
// wires the canon resource.drop, releasing the pollable under real WASI.
func TestSelfHostExternResourceHandleAutoDrop(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_runio_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "poll-ok"
	// No drop function — the self-host must auto-insert it.
	prog := `@import("wasi:io/poll@0.2.0", "pollable")
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
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	if !bytes.Contains(watBytes, []byte("[resource-drop]pollable")) {
		t.Errorf("self-host emitted core is missing the auto-inserted resource-drop import")
	}
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (self-host auto-drop): %v", err)
	}
	mine2 := filepath.Join(dir, "resource_autodrop.component.wasm")
	if err := os.WriteFile(mine2, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine2).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine2).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
