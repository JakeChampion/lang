package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

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

	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	run("cp", "-r", "../../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:cli/stderr@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
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
	src := `@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): i32;

@import("wasi:io/poll@0.2.0", "[method]pollable.block")
function block(h: i32);

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: i32): boolean;

function main(): i32 {
	let p: i32 = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	core := e2eharness.SelfHostComponentCore(t, mainPath)
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

	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	run("cp", "-r", "../../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:cli/stderr@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
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

function main(): i32 {
	let p: own Pollable = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	core := e2eharness.SelfHostComponentCore(t, mainPath)
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
	run("cp", "-r", "../../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:cli/stderr@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
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
	let p: own Pollable = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	drop_pollable(p);
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	core := e2eharness.SelfHostComponentCore(t, mainPath)
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
	run("cp", "-r", "../../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	if err := os.WriteFile(filepath.Join(witDir, "deps", "io", "poll.wit"),
		[]byte("package wasi:io@0.2.0;\ninterface poll {\n  resource pollable {\n    ready: func() -> bool;\n    block: func();\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write poll.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "deps", "clocks", "monotonic-clock.wit"),
		[]byte("package wasi:clocks@0.2.0;\ninterface monotonic-clock {\n  use wasi:io/poll@0.2.0.{pollable};\n  type instant = u64;\n  type duration = u64;\n  now: func() -> instant;\n  resolution: func() -> duration;\n  subscribe-instant: func(when: instant) -> pollable;\n  subscribe-duration: func(when: duration) -> pollable;\n}\n"), 0o644); err != nil {
		t.Fatalf("write monotonic-clock.wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import wasi:cli/stderr@0.2.0;\n    import wasi:clocks/monotonic-clock@0.2.0;\n    import wasi:io/poll@0.2.0;\n}\n"), 0o644); err != nil {
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

function main(): i32 {
	let p: own Pollable = subscribe(0 as u64);
	block(p);
	if (ready(p)) { write("` + want + `"); } else { write("poll-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	core := e2eharness.SelfHostComponentCore(t, mainPath)
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
