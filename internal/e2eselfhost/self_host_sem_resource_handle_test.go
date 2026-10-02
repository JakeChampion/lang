package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dropCallRE matches a call site of a resource-drop function in emitted WAT:
// the synthesized `__resource_drop_R` wrapper or a declared `[resource-drop]`
// import named `drop_*`. The wrappers' own calls to the import end in
// `__import` and are not counted.
var dropCallRE = regexp.MustCompile(`(?m)call \$(__resource_drop_\w+|drop_\w+)$`)

// TestSelfHostSemanticResourceHandles drives WIT resource handles (`own R` /
// `borrow R`) through the CLI's typed lowering to wasm, which must produce
// every declaration. Each program's
// drops are pinned by counting resource-drop call sites. Running them needs the poll world composed
// around a component core (the P5 tests in self_host_p5_resource_handle_test.go),
// which the CLI's preview1 core is not.
func TestSelfHostSemanticResourceHandles(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	emit := func(t *testing.T, src string) (string, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "prog.wat")
		cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", path, stdlibRoot, "-o", out)
		cmd.Env = append(os.Environ(), "FERN_SEM_IR_REPORT=1")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("compile: %v\n%s", err, stderr.String())
		}
		wat, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(wat), stderr.String()
	}

	for _, row := range []struct {
		name     string
		declared int
		drops    int
		src      string
	}{
		// An owned handle an import returns, lent to two borrow parameters and dropped
		// automatically on return.
		{name: "borrow-parameters-and-an-owned-import-result-auto-drop", declared: 5, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function main(): i32 {
    let p: own Pollable = subscribe(0 as u64);
    block(p);
    if (ready(p)) { write("poll-ok"); } else { write("poll-bad"); }
    return 0;
}
`},
		// A bare resource name is an owned handle, dropped like `own R`.
		{name: "a-bare-named-owned-local-auto-drops", declared: 5, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function main(): i32 {
    let p: Pollable = subscribe(0 as u64);
    block(p);
    if (ready(p)) { write("poll-ok"); } else { write("poll-bad"); }
    return 0;
}
`},
		// The program drops the handle itself: moved into the drop, so nothing else
		// drops it.
		{name: "an-explicit-drop-consumes-the-handle", declared: 5, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
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
    let p: own Pollable = subscribe(0 as u64);
    block(p);
    if (ready(p)) { write("poll-ok"); } else { write("poll-bad"); }
    drop_pollable(p);
    return 0;
}
`},
		{name: "an-auto-drop-in-a-nested-block", declared: 6, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
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
        let p: own Pollable = subscribe(0 as u64);
        block(p);
        if (ready(p)) { write("poll-ok"); } else { write("poll-bad"); }
    }
    return 0;
}
`},
		// `q = p` moves the handle: only the explicit drop of `q` runs.
		{name: "a-moved-handle-is-not-auto-dropped", declared: 5, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
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
    let p: own Pollable = subscribe(0 as u64);
    let q: own Pollable = p;
    block(q);
    if (ready(q)) { write("poll-ok"); } else { write("poll-bad"); }
    drop_pollable(q);
    return 0;
}
`},
		// Fern functions with an owned result, a borrow parameter and an owned
		// parameter that drops it.
		{name: "fern-functions-take-and-return-handles", declared: 8, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

@import("wasi:io/poll@0.2.0", "[resource-drop]pollable")
function drop_pollable(h: own Pollable): void;

function open(): own Pollable { return subscribe(0 as u64); }

function settle(h: borrow Pollable): boolean {
    block(h);
    return ready(h);
}

function finish(h: own Pollable): void { drop_pollable(h); }

function main(): i32 {
    let p: own Pollable = open();
    if (settle(p)) { write("poll-ok"); } else { write("poll-bad"); }
    finish(p);
    return 0;
}
`},
		// The drop pass drops owned locals, not owned parameters.
		{name: "an-owned-parameter-is-not-auto-dropped", declared: 5, drops: 0, src: `
@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function wait(h: own Pollable): boolean {
    block(h);
    return ready(h);
}

function main(): i32 {
    let p: own Pollable = subscribe(0 as u64);
    if (wait(p)) { write("poll-ok"); } else { write("poll-bad"); }
    return 0;
}
`},
		// An `@export` taking a borrow, holding an owned local its `return;` drops.
		{name: "an-export-lends-a-borrow-and-drops-a-local", declared: 3, drops: 1, src: `
@import("local:test/res@0.1.0", "thing")
resource Thing;

@import("local:test/res@0.1.0", "[constructor]thing")
function new_thing(): own Thing;

@export("local:test/handler@0.1.0", "handle")
function on_request(t: borrow Thing): void {
	let local: own Thing = new_thing();
	return;
}
`},
		// A generic instantiated at an owned handle and at a boolean: two
		// instances, which share no key.
		{name: "a-generic-instantiated-at-a-handle", declared: 6, drops: 1, src: `
@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: borrow Pollable);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;
function pass[T](x: T): T { return x; }

function main(): i32 {
    let p: own Pollable = pass(subscribe(0 as u64));
    block(p);
    let ok: boolean = pass(ready(p));
    if (ok) { write("poll-ok"); } else { write("poll-bad"); }
    return 0;
}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, report := emit(t, row.src)
			if n := semProducedCount(t, report); n != row.declared {
				t.Fatalf("produced %d declarations, want %d:\n%s", n, row.declared, report)
			}
			if drops := len(dropCallRE.FindAllString(got, -1)); drops != row.drops {
				t.Fatalf("resource-drop call sites: %d, want %d", drops, row.drops)
			}
		})
	}
}
