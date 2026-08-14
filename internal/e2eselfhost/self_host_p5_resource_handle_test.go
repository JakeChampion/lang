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

// pollableFixture is the scaffolding every P5 resource-handle gate in this file
// shares: the poll + monotonic-clock WIT world the emitted core is composed
// against, and the self-hosted wasm emitter that produces it.
type pollableFixture struct {
	dir       string
	wasmtime  string
	wasmtools string
	gcc       string
	runner    []string
	driverBin string
	world     *componenttype.World
}

// newPollableFixture skips unless the whole toolchain (wasmtime, wasm-tools,
// an x86-64 runner for the self-host driver) is present.
func newPollableFixture(t *testing.T) *pollableFixture {
	t.Helper()
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
	f := &pollableFixture{dir: dir, wasmtime: wasmtime, wasmtools: wasmtools, gcc: gcc, runner: runner}

	// Full standard WIT for poll + monotonic-clock (the vendored deps are
	// stripped to fern's subset).
	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	f.run(t, "cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
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
	f.run(t, wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	f.run(t, wasmtools, "component", "embed", witDir, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}
	f.world = w

	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	f.driverBin = buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")
	return f
}

func (f *pollableFixture) run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// emit runs prog through the self-hosted wasm emitter and returns the core WAT.
func (f *pollableFixture) emit(t *testing.T, prog string) []byte {
	t.Helper()
	wat := runCapture(t, f.gcc, f.runner, f.driverBin, []byte(prog))
	if len(wat) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	return wat
}

// composeRun composes the emitted core against the world, validates the
// component, runs it under real WASI and returns its stdout.
func (f *pollableFixture) composeRun(t *testing.T, name string, wat []byte) []byte {
	t.Helper()
	watPath := filepath.Join(f.dir, name+".wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(f.dir, name+".wasm")
	f.run(t, f.wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, f.world)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (%s): %v", name, err)
	}
	compPath := filepath.Join(f.dir, name+".component.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(f.wasmtools, "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(f.wasmtime, "run", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	return out
}

// pollWant is the marker a ready pollable prints in every program below. Each
// one blocks on the pollable first: under wasmtime 46 a 0ns subscribe-duration
// timer that has not been blocked on reports NOT ready, so `ready` alone prints
// poll-bad.
const pollWant = "poll-ok"

func wantStdout(t *testing.T, out []byte) {
	t.Helper()
	if !bytes.Contains(out, []byte(pollWant)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, pollWant)
	}
}

// TestSelfHostExternResourceHandle is the self-host P5 baseline gate
// (docs/WIT-BRING-YOUR-OWN.md): the self-hosted wasm backend drives a WIT
// resource across the `@import` boundary with no resource-specific codegen —
// a handle is an opaque i32, so `subscribe-duration -> own<pollable>` and
// `[method]pollable.ready -> bool` are ordinary scalar imports the self-host
// emitter already lowers. Mirror of the Go TestExternResourceHandle: a 0ns
// monotonic timer's pollable is immediately ready, run against real WASI.
func TestSelfHostExternResourceHandle(t *testing.T) {
	f := newPollableFixture(t)
	prog := `@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): i32;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: i32);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: i32): boolean;

function main(): i32 {
    var p: i32 = subscribe(0 as u64);
    block(p);
    if (ready(p)) { write("` + pollWant + `"); } else { write("poll-bad"); }
    return 0;
}`
	watBytes := f.emit(t, prog)
	if !bytes.Contains(watBytes, []byte("subscribe-duration")) || !bytes.Contains(watBytes, []byte("[method]pollable.ready")) {
		t.Errorf("emitted core is missing the resource extern imports")
	}
	wantStdout(t, f.composeRun(t, "resource", watBytes))
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
	f := newPollableFixture(t)
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
    if (ready(p)) { write("` + pollWant + `"); } else { write("poll-bad"); }
    return 0;
}`
	watBytes := f.emit(t, prog)
	if !bytes.Contains(watBytes, []byte("subscribe-duration")) || !bytes.Contains(watBytes, []byte("[method]pollable.ready")) {
		t.Errorf("emitted core is missing the resource extern imports")
	}
	wantStdout(t, f.composeRun(t, "resource_typed", watBytes))
}

// TestSelfHostExternResourceHandleDrop is the self-host parity gate for P5
// slice 2 (docs/WIT-BRING-YOUR-OWN.md): the self-host backend emits a
// `[resource-drop]pollable` core import, and the (Go) world-driven composer
// surfaces the resource + wires the canon resource.drop. Mirror of the Go
// TestExternResourceHandleDrop — the pollable is dropped, run under real WASI.
func TestSelfHostExternResourceHandleDrop(t *testing.T) {
	f := newPollableFixture(t)
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
    if (ready(p)) { write("` + pollWant + `"); } else { write("poll-bad"); }
    drop_pollable(p);
    return 0;
}`
	watBytes := f.emit(t, prog)
	if !bytes.Contains(watBytes, []byte("[resource-drop]pollable")) {
		t.Errorf("emitted core is missing the resource-drop import")
	}
	wantStdout(t, f.composeRun(t, "resource_drop", watBytes))
}

// TestSelfHostExternResourceHandleAutoDrop is the self-host parity gate for P5
// slice 3 (docs/WIT-BRING-YOUR-OWN.md): AUTOMATIC drop. The program declares NO
// drop function — the self-host compiler (parser.fern lower_defers_prepass_module)
// inserts `defer <drop>(p);` for the owned `own Pollable` local, lower_defers
// expands it on the return path, and the synthesized `[resource-drop]pollable`
// import is emitted in the core. The Go world-driven composer (slice 2) then
// wires the canon resource.drop, releasing the pollable under real WASI.
func TestSelfHostExternResourceHandleAutoDrop(t *testing.T) {
	f := newPollableFixture(t)
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
    if (ready(p)) { write("` + pollWant + `"); } else { write("poll-bad"); }
    return 0;
}`
	watBytes := f.emit(t, prog)
	if !bytes.Contains(watBytes, []byte("[resource-drop]pollable")) {
		t.Errorf("self-host emitted core is missing the auto-inserted resource-drop import")
	}
	wantStdout(t, f.composeRun(t, "resource_autodrop", watBytes))
}

// TestSelfHostExternResourceHandleAutoDropNestedBlock pins the auto-drop for a
// handle declared inside a nested block. The insertion walks every block, not
// just the function's top level (#5337) — before that it matched top-level
// declarations only and this program leaked the pollable with no drop import
// emitted at all.
func TestSelfHostExternResourceHandleAutoDropNestedBlock(t *testing.T) {
	f := newPollableFixture(t)
	prog := `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function gate(): boolean { return true; }

function main(): i32 {
    if (gate()) {
        var p: own Pollable = subscribe(0 as u64);
        block(p);
        if (ready(p)) { write("` + pollWant + `"); } else { write("poll-bad"); }
    }
    return 0;
}`
	watBytes := f.emit(t, prog)
	if !bytes.Contains(watBytes, []byte("[resource-drop]pollable")) {
		t.Errorf("self-host emitted core is missing the auto-inserted resource-drop import for a nested-block handle")
	}
	wantStdout(t, f.composeRun(t, "resource_autodrop_nested", watBytes))
}

// TestSelfHostExternResourceHandleMovedNoAutoDrop pins the move side of the
// same analysis: a handle aliased into another local and then handed to an
// `own` parameter is MOVED, so the consumer drops it and the compiler must not
// (#5337). The alias used to read as "not moved", which put a synthesized drop
// on a handle the program already dropped — a double drop, which the composed
// component turns into a wasmtime trap on the second release.
func TestSelfHostExternResourceHandleMovedNoAutoDrop(t *testing.T) {
	f := newPollableFixture(t)
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
    var q: own Pollable = p;
    block(q);
    if (ready(q)) { write("` + pollWant + `"); } else { write("poll-bad"); }
    drop_pollable(q);
    return 0;
}`
	watBytes := f.emit(t, prog)
	if bytes.Contains(watBytes, []byte("__resource_drop_Pollable")) {
		t.Errorf("self-host synthesized a drop for a moved handle the program already drops (double drop)")
	}
	wantStdout(t, f.composeRun(t, "resource_moved", watBytes))
}
