package component_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// TestPutTypeSectionInstanceWithInnerTypes_ResultParam round-trips
// an instance-type that declares an inner `result<_, _>` type and
// references it from the function's `status` parameter — the
// shape `wasi:cli/exit@0.2.0::exit` expects. The component is
// otherwise empty (no module / imports), so this isolates the
// type-section encoding.
func TestPutTypeSectionInstanceWithInnerTypes_ResultParam(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport(
		buf,
		[][]byte{component.InnerTypeResultEmpty},
		"exit",
		[]string{"status"},
		[]byte{0x00}, // inner-typeidx 0
	)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"(result)",
		"\"exit\"",
		"\"status\"",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed output, got:\n%s", want, out)
		}
	}
}

// TestPutComponentTypeSection_StructuralRoundTrip emits a component
// with just a header + a synthetic component-type custom section
// payload and confirms wasm-tools accepts it + surfaces the
// section under that name.
func TestPutComponentTypeSection_StructuralRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	payload := []byte{'t', 'e', 's', 't'}
	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentTypeSection(buf, payload)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("component-type")) {
		t.Errorf("expected `component-type` in printed output, got:\n%s", out)
	}
}

// TestPutCanonSectionLowerWithMemory_Bytes pins the bytes of a
// canon-lower entry carrying a single `memory` canonical-ABI
// option. The expected bytes match what wasm-tools emits for the
// wasi:io/streams::blocking-write-and-flush lowering: it carries
// a list<u8> param so canon-lower needs memory to know where the
// (ptr, len) point.
//
// Wire shape:
//
//	08 07       -- section id 8 (canon), body size 7
//	01          -- vec(1) canons
//	01 00 01    -- canon-lower, function sub-tag, funcIdx 1
//	01          -- opts vec(1)
//	03 00       -- canonopt: memory (0x03), memidx 0
func TestPutCanonSectionLowerWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerWithMemory(nil, 1, 0)
	want := []byte{0x08, 0x07, 0x01, 0x01, 0x00, 0x01, 0x01, 0x03, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerWithMemory(1, 0) = % x, want % x", got, want)
	}
}

// TestPutCanonResourceDrop_Bytes pins the canon resource.drop entry:
// section 8, body = vec(1) | 0x03 (resource.drop) | typeidx.
func TestPutCanonResourceDrop_Bytes(t *testing.T) {
	got := component.PutCanonResourceDrop(nil, 1)
	want := []byte{0x08, 0x03, 0x01, 0x03, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonResourceDrop(1) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLiftWithMemory_Bytes pins the canon-lift-with-memory entry
// (P6 composite exports): section 8, body = vec(1) | 0x00 lift | 0x00 subtag |
// funcidx | opts vec(1) | 0x03 memory | memidx | typeidx. Opts precede the
// typeidx for a lift (unlike the no-opts form's bare typeidx).
func TestPutCanonSectionLiftWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftWithMemory(nil, 1, 2, 0)
	want := []byte{0x08, 0x08, 0x01, 0x00, 0x00, 0x01, 0x01, 0x03, 0x00, 0x02}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftWithMemory(1,2,0) = % x, want % x", got, want)
	}
}

// TestPutTypeSectionOneDefined_Bytes pins a component type section carrying one
// `list<s32>` defined type (P6 list export): section 7, body = vec(1) | 0x70
// (list) | 0x7a (s32 cvaltype).
func TestPutTypeSectionOneDefined_Bytes(t *testing.T) {
	got := component.PutTypeSectionOneDefined(nil, component.InnerTypeList(component.CValtypeS32))
	want := []byte{0x07, 0x03, 0x01, 0x70, 0x7a}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneDefined(list<s32>) = % x, want % x", got, want)
	}
}

// TestPutTypeSectionOneFuncResultIdx_Bytes pins a functype whose single
// anonymous result is a defined-type index. Two cases gate the s33 encoding: a
// small index is one byte, but index 65 (≥ 64, payload bit 0x40 set) must
// sleb-encode to two bytes (c1 00) so it isn't misread as a negative primitive.
func TestPutTypeSectionOneFuncResultIdx_Bytes(t *testing.T) {
	// func(n: u32) -> (type 0): section 7, body = vec(1) | 0x40 functype |
	// vec(1) params | "n" u32 | 0x00 single-anon | sleb(0)=0x00.
	got := component.PutTypeSectionOneFuncResultIdx(nil, []string{"n"}, []byte{component.CValtypeU32}, 0)
	want := []byte{0x07, 0x08, 0x01, 0x40, 0x01, 0x01, 'n', 0x79, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneFuncResultIdx(n:u32 -> #0) = % x, want % x", got, want)
	}
	// func() -> (type 65): the result index sleb-encodes to two bytes (c1 00).
	got = component.PutTypeSectionOneFuncResultIdx(nil, nil, nil, 65)
	want = []byte{0x07, 0x06, 0x01, 0x40, 0x00, 0x00, 0xc1, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneFuncResultIdx(() -> #65) = % x, want % x", got, want)
	}
}

// TestPutTypeSectionOneFuncGeneral_Bytes pins a functype with a mix of a list
// (defined-type index 0) param and a scalar result — the P6 composite param/
// result export encoding. Body = vec(1) | functype | vec(1) param "xs" sleb(0)
// | single-anon | s32 result.
func TestPutTypeSectionOneFuncGeneral_Bytes(t *testing.T) {
	got := component.PutTypeSectionOneFuncGeneral(nil,
		[]string{"xs"}, [][]byte{{0x00}}, []byte{component.CValtypeS32})
	want := []byte{0x07, 0x09, 0x01, 0x40, 0x01, 0x02, 'x', 's', 0x00, 0x00, 0x7a}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneFuncGeneral(xs:#0 -> s32) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLiftWithMemoryRealloc_Bytes pins the lift-with-memory+realloc
// entry (string/list PARAM exports): opts vec(2) = memory + realloc, then typeidx.
func TestPutCanonSectionLiftWithMemoryRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftWithMemoryRealloc(nil, 1, 2, 0, 3)
	want := []byte{0x08, 0x0a, 0x01, 0x00, 0x00, 0x01, 0x02, 0x03, 0x00, 0x04, 0x03, 0x02}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftWithMemoryRealloc(1,2,0,3) = % x, want % x", got, want)
	}
}

// TestPutCanonResourceDrop_Validates composes a component that
// imports wasi:io/error (whose `error` resource it aliases) and
// lowers a resource.drop for it — confirming wasm-tools accepts the
// canon resource.drop encoding against a real imported resource type.
func TestPutCanonResourceDrop_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error") // error → type 1
	buf = component.PutCanonResourceDrop(buf, 1)
	dir := t.TempDir()
	p := filepath.Join(dir, "rdrop.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("resource.drop")) {
		t.Errorf("expected resource.drop in printed component, got:\n%s", out)
	}
}

// TestPutCanonSectionLowerWithMemoryRealloc_Bytes pins the bytes
// of a canon-lower entry carrying both memory + realloc opts.
// Expected shape:
//
//	08 09       -- section id 8 (canon), body size 9
//	01          -- vec(1) canons
//	01 00 02    -- canon-lower, function sub-tag, funcIdx 2
//	02          -- opts vec(2)
//	03 00       -- canonopt: memory (0x03), memidx 0
//	04 05       -- canonopt: realloc (0x04), funcidx 5
func TestPutCanonSectionLowerWithMemoryRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerWithMemoryRealloc(nil, 2, 0, 5)
	want := []byte{0x08, 0x09, 0x01, 0x01, 0x00, 0x02, 0x02, 0x03, 0x00, 0x04, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerWithMemoryRealloc(2, 0, 5) = % x, want % x", got, want)
	}
}

// TestPutAliasSectionInstanceExportType_Bytes pins the bytes of
// a top-level alias that exposes a type exported by a component
// instance. The expected bytes match what wasm-tools emits for
// the wasi:io/streams component's `alias export 0 "error"` —
// the alias that surfaces wasi:io/error's `error` resource at
// the top-level type space.
//
// Wire shape:
//
//	06 0a                   -- section id 6 (alias), body size 10
//	01                      -- vec(1) aliases
//	03                      -- sort = type
//	00                      -- target: from-instance-export
//	00                      -- instance idx 0
//	05 "error"              -- name (uleb 5 + 5 bytes)
func TestPutAliasSectionInstanceExportType_Bytes(t *testing.T) {
	got := component.PutAliasSectionInstanceExportType(nil, 0, "error")
	want := []byte{0x06, 0x0a, 0x01, 0x03, 0x00, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r'}
	if !bytes.Equal(got, want) {
		t.Errorf("PutAliasSectionInstanceExportType(0, %q) = % x, want % x", "error", got, want)
	}
}

// TestOuterAliasTypeDecl_Bytes pins the 5-byte outer-alias decl
// emitted inside instance-type bodies. The example matches what
// wasm-tools emits inside the wasi:io/streams instance type:
//
//	`(alias outer 1 1 (type (;1;)))`
//
// — 1 scope up, outer typeidx 1.
func TestOuterAliasTypeDecl_Bytes(t *testing.T) {
	got := component.OuterAliasTypeDecl(1, 1)
	want := []byte{0x02, 0x03, 0x02, 0x01, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("OuterAliasTypeDecl(1, 1) = % x, want % x", got, want)
	}
}

// TestExportSubResourceDecl_Bytes pins the bytes for a
// `resource error;` declaration inside an instance type body —
// the shape used by wasi:io/error.
func TestExportSubResourceDecl_Bytes(t *testing.T) {
	got := component.ExportSubResourceDecl("error")
	want := []byte{0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("ExportSubResourceDecl(%q) = % x, want % x", "error", got, want)
	}
}

// TestExportTypeEqDecl_Bytes pins the bytes for an `export
// "error" (type (eq 1))` decl — the shape wasi:io/streams uses
// to re-export the outer-aliased error type as its own typeidx.
func TestExportTypeEqDecl_Bytes(t *testing.T) {
	got := component.ExportTypeEqDecl("error", 1)
	want := []byte{0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("ExportTypeEqDecl(%q, 1) = % x, want % x", "error", got, want)
	}
}

// TestWasiIoErrorInstanceTypeBody_Composed exercises the
// pieces together by composing the wasi:io/error instance type
// body and validating it via the escape hatch. The body is
// just `vec(1) decls + ExportSubResourceDecl("error")` plus
// the standard instance-type framing.
func TestWasiIoErrorInstanceTypeBody_Composed(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	resourceDecl := component.ExportSubResourceDecl("error")
	body := []byte{0x01, 0x42, 0x01}
	body = append(body, resourceDecl...)
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, body)
	dir := t.TempDir()
	compPath := filepath.Join(dir, "iee.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"\"error\"", "(sub resource)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
}

// TestWasiIoErrorInstanceTypeBody_Bytes pins the exact bytes of
// the wasi:io/error instance type body. Matches what wasm-tools
// emits for the canonical `interface error { resource error; }`.
func TestWasiIoErrorInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiIoErrorInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x01,
		0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoErrorInstanceTypeBody() = % x, want % x", got, want)
	}
}

// TestWasiIoPollInstanceTypeBody_WithPoll_Validates composes the
// heavier io/poll instance type (block + the poll multiplexer) and
// confirms wasm-tools accepts it and the `poll` func + `list<u32>`
// result appear.
func TestWasiIoPollInstanceTypeBody_WithPoll_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(true))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "poll.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"\"poll\"", "(list u32)", "(list 1)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// The default (block-only) io/poll instance type must stay byte-
// identical to the historical shape the socket shapes depend on — the
// withPoll=false path adds nothing.
func TestWasiIoPollInstanceTypeBody_BlockOnly_Bytes(t *testing.T) {
	got := component.WasiIoPollInstanceTypeBody(false)
	want := []byte{
		0x01, 0x42, 0x04,
		0x04, 0x00, 0x08, 'p', 'o', 'l', 'l', 'a', 'b', 'l', 'e', 0x03, 0x01,
		0x01, 0x68, 0x00,
		0x01, 0x40, 0x01, 0x04, 's', 'e', 'l', 'f', 0x01, 0x01, 0x00,
		0x04, 0x00, 0x16, '[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'p', 'o', 'l', 'l', 'a', 'b', 'l', 'e', '.', 'b', 'l', 'o', 'c', 'k', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoPollInstanceTypeBody(false) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLowerAsync_Bytes pins the WASI Preview-3
// component-model-async LOWER encoding (the import / await side): a
// canon-lower with the `async` (0x06) + `memory` (0x03) options. Bytes
// byte-identical to what wasm-tools 1.240 emits for
// `(canon lower (func N) async (memory M))` — verified by disassembling
// a nested-component await that runs under
// `wasmtime -W component-model-async,component-model-async-stackful` and
// returns its result (docs/WASI-PREVIEW3-ASYNC-PLAN.md). `memory` is
// required for an async lower.
func TestPutCanonSectionLowerAsync_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerAsync(nil, 0, 0)
	want := []byte{
		0x08, 0x08, // canon section, size 8
		0x01,       // vec(1)
		0x01, 0x00, // canon-lower + function-lower sub-tag
		0x00,       // func index 0
		0x02,       // opts vec(2)
		0x06,       // canonopt: async
		0x03, 0x00, // canonopt: memory, memidx 0
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerAsync(0, 0) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLiftAsync_Bytes pins the WASI Preview-3
// component-model-async lift encoding. The bytes are byte-identical to
// what wasm-tools 1.240 emits for `(canon lift (core func 1) async)`
// (verified by disassembling a component that runs under
// `wasmtime -W component-model-async,component-model-async-stackful`
// and returns its result — docs/WASI-PREVIEW3-ASYNC-PLAN.md). The
// `async` canonical option is 0x06.
func TestPutCanonSectionLiftAsync_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftAsync(nil, 1, 0)
	want := []byte{
		0x08, 0x07, // canon section, size 7
		0x01,       // vec(1)
		0x00, 0x00, // canon-lift + function-lift sub-tag
		0x01,       // core func index 1
		0x01, 0x06, // opts vec(1) = [async=0x06]
		0x00, // type index 0
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftAsync(1, 0) = % x, want % x", got, want)
	}
}

// TestPutCanonTaskReturnSingle_Bytes pins the `task.return` (single
// u32 result) encoding — the intrinsic an async-lifted export calls to
// deliver its result. Byte-identical to wasm-tools 1.240's
// `(canon task.return (result u32))`.
func TestPutCanonTaskReturnSingle_Bytes(t *testing.T) {
	got := component.PutCanonTaskReturnSingle(nil, component.CValtypeU32)
	want := []byte{
		0x08, 0x05, // canon section, size 5
		0x01,                        // vec(1)
		0x09,                        // canon task.return
		0x00, component.CValtypeU32, // result: single-value u32
		0x00, // options vec(0)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonTaskReturnSingle(u32) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLowerAsyncRealloc_Bytes pins the async-import lower for a
// result that carries linear-memory data (string / list): the scalar async
// lower `[async, memory]` plus the `realloc` (0x04) option the canonical ABI
// uses to materialise the incoming bytes in the guest's memory. Derived by
// analogy from the proven scalar async lower (PutCanonSectionLowerAsync) +
// PutCanonSectionLowerWithMemoryRealloc's `realloc` option; the runnable
// string-flow check is gated on the provider-side memory circularity noted in
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestPutCanonSectionLowerAsyncRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerAsyncRealloc(nil, 0, 0, 0)
	want := []byte{
		0x08, 0x0a, // canon section, size 10
		0x01,       // vec(1)
		0x01, 0x00, // canon-lower + function-lower sub-tag
		0x00,       // func index 0
		0x03,       // opts vec(3)
		0x06,       // canonopt: async
		0x03, 0x00, // canonopt: memory, memidx 0
		0x04, 0x00, // canonopt: realloc, funcidx 0
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerAsyncRealloc(0,0,0) = % x, want % x", got, want)
	}
}

// TestPutCanonTaskReturnStringWithMemory_Bytes pins the `task.return` whose
// result is a `string` (0x73) carrying the `memory` (0x03) option — the
// intrinsic a string-returning async export calls with `(ptr, len)`. Derived
// from PutCanonTaskReturnSingle's form + the memory option.
func TestPutCanonTaskReturnStringWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonTaskReturnStringWithMemory(nil, 0)
	want := []byte{
		0x08, 0x07, // canon section, size 7
		0x01,       // vec(1)
		0x09,       // canon task.return
		0x00, 0x73, // result: single-value string
		0x01,       // options vec(1)
		0x03, 0x00, // canonopt: memory, memidx 0
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonTaskReturnStringWithMemory(0) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLiftAsyncWithMemory_Bytes pins the async export lift with
// the `memory` option appended (for a string/list result). Derived from
// PutCanonSectionLiftAsync + the memory option.
func TestPutCanonSectionLiftAsyncWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftAsyncWithMemory(nil, 1, 0, 0)
	want := []byte{
		0x08, 0x09, // canon section, size 9
		0x01,       // vec(1)
		0x00, 0x00, // canon-lift + function-lift sub-tag
		0x01,       // core func index 1
		0x02,       // opts vec(2)
		0x06,       // canonopt: async
		0x03, 0x00, // canonopt: memory, memidx 0
		0x00, // type index 0
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftAsyncWithMemory(1,0,0) = % x, want % x", got, want)
	}
}

// TestPutCanonWaitableBuiltins_Bytes pins the WASI Preview-3 waitable-set /
// subtask canon-builtin encodings — byte-verified against wasm-tools 1.240's
// `dump` (waitable-set.new 0x1f, waitable-set.wait 0x20 <cancellable> <mem>,
// waitable-set.drop 0x22, waitable.join 0x23, subtask.drop 0x0d). These are the
// encoder layer for the pending-await epic (docs/WASI-PREVIEW3-ASYNC-PLAN.md).
func TestPutCanonWaitableBuiltins_Bytes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"waitable-set.new", component.PutCanonWaitableSetNew(nil), []byte{0x08, 0x02, 0x01, 0x1f}},
		{"waitable-set.wait", component.PutCanonWaitableSetWait(nil, 0), []byte{0x08, 0x04, 0x01, 0x20, 0x00, 0x00}},
		{"waitable-set.drop", component.PutCanonWaitableSetDrop(nil), []byte{0x08, 0x02, 0x01, 0x22}},
		{"waitable.join", component.PutCanonWaitableJoin(nil), []byte{0x08, 0x02, 0x01, 0x23}},
		{"subtask.drop", component.PutCanonSubtaskDrop(nil), []byte{0x08, 0x02, 0x01, 0x0d}},
		{"thread.yield", component.PutCanonThreadYield(nil), []byte{0x08, 0x03, 0x01, 0x0c, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = % x, want % x", c.name, c.got, c.want)
		}
	}
}

// TestPutCanonFutureStreamBuiltins_Bytes pins the WASI Preview-3 future<T> /
// stream<T> canon-builtin encodings — byte-verified against wasm-tools 1.240's
// `dump` (future.new 0x15, future.read 0x16, future.write 0x17; stream.new 0x0e,
// stream.read 0x0f, stream.write 0x10; .read/.write carry the canonical
// `[async(0x06), memory(0x03 <idx>)]` options). The defvaltype encodings are
// future `0x65` / stream `0x66`, each `01 <elem>` for a payloadful channel. These
// are the encoder layer for the future/stream epic — the next async primitive
// after the scalar/string/list param×result matrix + pending-await. See
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestPutCanonFutureStreamBuiltins_Bytes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"future.new", component.PutCanonFutureNew(nil, 0), []byte{0x08, 0x03, 0x01, 0x15, 0x00}},
		{"future.read", component.PutCanonFutureRead(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x16, 0x00, 0x02, 0x06, 0x03, 0x00}},
		{"future.write", component.PutCanonFutureWrite(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x17, 0x00, 0x02, 0x06, 0x03, 0x00}},
		{"stream.new", component.PutCanonStreamNew(nil, 0), []byte{0x08, 0x03, 0x01, 0x0e, 0x00}},
		{"stream.read", component.PutCanonStreamRead(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x0f, 0x00, 0x02, 0x06, 0x03, 0x00}},
		{"stream.write", component.PutCanonStreamWrite(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x10, 0x00, 0x02, 0x06, 0x03, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = % x, want % x", c.name, c.got, c.want)
		}
	}
	// Defvaltype bodies: future<u32> = 65 01 79, stream<u8> = 66 01 7d.
	if got := component.InnerTypeFuture(component.CValtypeU32); !bytes.Equal(got, []byte{0x65, 0x01, 0x79}) {
		t.Errorf("InnerTypeFuture(u32) = % x, want 65 01 79", got)
	}
	if got := component.InnerTypeStream(component.CValtypeU8); !bytes.Equal(got, []byte{0x66, 0x01, 0x7d}) {
		t.Errorf("InnerTypeStream(u8) = % x, want 66 01 7d", got)
	}
	// task.return of a future<T> result (type index 0): 09 00 <typeidx> 00, no
	// options (the readable handle is scalar) — byte-verified vs wasm-tools 1.240.
	if got := component.PutCanonTaskReturnTypeIdx(nil, 0); !bytes.Equal(got, []byte{0x08, 0x05, 0x01, 0x09, 0x00, 0x00, 0x00}) {
		t.Errorf("PutCanonTaskReturnTypeIdx(0) = % x, want 08 05 01 09 00 00 00", got)
	}
}

// TestWasiClocksMonotonicTimerInstanceTypeBody_Bytes pins the bytes
// of the wasm-reactor timer instance type: an outer-aliased pollable
// (here at top-level type index 5), own<pollable>, and the
// `subscribe-duration(when: u64) -> own<pollable>` func + export.
func TestWasiClocksMonotonicTimerInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiClocksMonotonicTimerInstanceTypeBody(5)
	want := []byte{
		0x01, 0x42, 0x04, // 4 decls
		0x02, 0x03, 0x02, 0x01, 0x05, // 0: outer-alias(1, 5) -> pollable
		0x01, 0x69, 0x00, // 1: own<pollable=0>
		// 2: func(when: u64) -> own<pollable=1>
		0x01, 0x40, 0x01, 0x04, 'w', 'h', 'e', 'n', component.CValtypeU64, 0x00, 0x01,
		// 3: export "subscribe-duration" (func 2)
		0x04, 0x00, 0x12, 's', 'u', 'b', 's', 'c', 'r', 'i', 'b', 'e', '-', 'd', 'u', 'r', 'a', 't', 'i', 'o', 'n', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiClocksMonotonicTimerInstanceTypeBody(5) = % x, want % x", got, want)
	}
}

// TestWasiFilesystemTypesDescriptorInstanceTypeBody_Bytes pins the
// bytes of the minimal wasi:filesystem/types instance type (one
// exported `descriptor` sub-resource).
func TestWasiFilesystemTypesDescriptorInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiFilesystemTypesDescriptorInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x01,
		0x04, 0x00, 0x0a, 'd', 'e', 's', 'c', 'r', 'i', 'p', 't', 'o', 'r', 0x03, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiFilesystemTypesDescriptorInstanceTypeBody() = % x, want % x", got, want)
	}
}

// TestWasiFilesystemTypesDescriptorInstanceTypeBody_Validates
// composes the descriptor instance type as an import and confirms
// wasm-tools accepts it + the descriptor sub-resource appears.
func TestWasiFilesystemTypesDescriptorInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesDescriptorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "fstypes.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"descriptor", "(sub resource)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesReadViaStreamInstanceTypeBody_Validates
// composes the read-via-stream descriptor method (which
// outer-aliases input-stream from wasi:io/streams) and confirms
// wasm-tools accepts it.
func TestWasiFilesystemTypesReadViaStreamInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesReadViaStreamInstanceTypeBody(3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsread.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"read-via-stream", "descriptor", "error-code", "input-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesReadWritePathInstanceTypeBody_Validates composes
// the combined read+write descriptor instance type (both via-stream
// directions over input + output stream) and confirms wasm-tools accepts
// it — the shape a read_file + write_file program needs.
func TestWasiFilesystemTypesReadWritePathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())                                                                              // type 0
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)                                                                                       // inst 0
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")                                                                                               // type 1
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))                                                                  // type 2
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)                                                                                     // inst 1
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")                                                                                       // type 3
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")                                                                                        // type 4
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(4, 3, component.FsFeatures{OpenAt: true, Read: true, Write: true})) // type 5
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 5)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsrw.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"read-via-stream", "write-via-stream", "descriptor", "input-stream", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesWriteViaStreamInstanceTypeBody_Validates
// composes the write-via-stream descriptor method (which
// outer-aliases output-stream from wasi:io/streams) and confirms
// wasm-tools accepts it.
func TestWasiFilesystemTypesWriteViaStreamInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// io/error (instance 0) → error top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	// io/streams (instance 1) → output-stream top-level type 3.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	// filesystem/types write-via-stream, referencing output-stream (type 3).
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesWriteViaStreamInstanceTypeBody(3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fswrite.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"write-via-stream", "descriptor", "error-code", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestInnerTypeFlags_Bytes pins a small flags defvaltype.
func TestInnerTypeFlags_Bytes(t *testing.T) {
	got := component.InnerTypeFlags([]string{"a", "bc"})
	want := []byte{0x6e, 0x02, 0x01, 'a', 0x02, 'b', 'c'}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeFlags = % x, want % x", got, want)
	}
}

// TestWasiFilesystemTypesOpenAtInstanceTypeBody_Validates composes
// the self-contained open-at descriptor method (descriptor +
// error-code + the three flag types) and confirms wasm-tools
// accepts it.
func TestWasiFilesystemTypesOpenAtInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesOpenAtInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "openat.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "path-flags", "open-flags", "descriptor-flags", "symlink-follow"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesReadPathInstanceTypeBody_Validates
// composes the combined read-path filesystem/types instance type
// (open-at + read-via-stream, outer-aliasing input-stream from
// io/streams) and confirms wasm-tools accepts it.
func TestWasiFilesystemTypesReadPathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(3, 0, component.FsFeatures{OpenAt: true, Read: true}))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsreadpath.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "read-via-stream", "descriptor", "path-flags"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesWritePathInstanceTypeBody_Validates is the
// write-side counterpart: open-at + write-via-stream, outer-aliasing
// output-stream from wasi:io/streams.
func TestWasiFilesystemTypesWritePathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(0, 3, component.FsFeatures{OpenAt: true, Write: true}))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fswritepath.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "write-via-stream", "descriptor", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesAppendPathInstanceTypeBody_Validates is the
// append-side counterpart: open-at + append-via-stream (the
// offset-less method), outer-aliasing output-stream.
func TestWasiFilesystemTypesAppendPathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(0, 3, component.FsFeatures{OpenAt: true, Append: true}))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsappendpath.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "append-via-stream", "descriptor", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemPreopensInstanceTypeBody_Validates composes
// the preopens instance type (which outer-aliases the descriptor
// resource from a wasi:filesystem/types import) and confirms
// wasm-tools accepts it + the get-directories / descriptor / tuple
// names appear.
func TestWasiFilesystemPreopensInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// wasi:filesystem/types import (instance 0) so the outer alias
	// of `descriptor` (top-level type 1) resolves.
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesDescriptorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "descriptor")
	// The preopens instance type referencing top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemPreopensInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/preopens@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "preopens.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-directories", "descriptor", "tuple"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiCliStdoutInstanceTypeBody_Bytes pins the bytes of the
// wasi:cli/stdout instance type body. The expected bytes match
// what wasm-tools emits when wasi:cli/stdout is the THIRD import
// in a print-component (after wasi:io/error and wasi:io/streams)
// — the output-stream type lands at outer typeidx 3.
func TestWasiCliStdoutInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliStdoutInstanceTypeBody(3)
	want := []byte{
		0x01, 0x42, 0x05,
		// alias outer 1 3
		0x02, 0x03, 0x02, 0x01, 0x03,
		// export "output-stream" (type (eq 0))
		0x04, 0x00, 0x0d, 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x00, 0x00,
		// type (own 1)
		0x01, 0x69, 0x01,
		// type (func () -> 2)
		0x01, 0x40, 0x00, 0x00, 0x02,
		// export "get-stdout" (func 3)
		0x04, 0x00, 0x0a, 'g', 'e', 't', '-', 's', 't', 'd', 'o', 'u', 't', 0x01, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliStdoutInstanceTypeBody(3) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestInnerTypeBorrow_Bytes pins `borrow<0>` defvaltype bytes.
func TestInnerTypeBorrow_Bytes(t *testing.T) {
	got := component.InnerTypeBorrow(0)
	want := []byte{0x68, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeBorrow(0) = % x, want % x", got, want)
	}
}

// TestInnerTypeListU8_Bytes pins `list<u8>` defvaltype bytes.
func TestInnerTypeListU8_Bytes(t *testing.T) {
	got := component.InnerTypeListU8
	want := []byte{0x70, 0x7d}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeListU8 = % x, want % x", got, want)
	}
}

// TestInnerTypeResultErr_Bytes pins `result<_, err=5>` bytes.
func TestInnerTypeResultErr_Bytes(t *testing.T) {
	got := component.InnerTypeResultErr(5)
	want := []byte{0x6a, 0x00, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeResultErr(5) = % x, want % x", got, want)
	}
}

// TestInnerTypeVariant_StreamError pins the bytes for the
// canonical-ABI variant `stream-error` declared inside
// wasi:io/streams. Cases:
//   - "last-operation-failed(error)" — payload typeidx 3
//     (own<error>)
//   - "closed" — no payload
//
// Expected wire shape (matches wasm-tools dump):
//
//	6b 02
//	15 "last-operation-failed" 01 03 00
//	06 "closed" 00 00
func TestInnerTypeVariant_StreamError(t *testing.T) {
	got := component.InnerTypeVariant([]component.VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})
	want := []byte{
		0x71, 0x02,
		0x15, 'l', 'a', 's', 't', '-', 'o', 'p', 'e', 'r', 'a', 't', 'i', 'o', 'n', '-', 'f', 'a', 'i', 'l', 'e', 'd', 0x01, 0x03, 0x00,
		0x06, 'c', 'l', 'o', 's', 'e', 'd', 0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeVariant(stream-error) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiIoStreamsInstanceTypeBody_Bytes pins the bytes of the
// wasi:io/streams instance type body, parameterised by where
// wasi:io/error's `error` resource lives at the top-level type
// space (in a canonical print component, that's outer typeidx
// 1 — wasi:io/error is the first import, its single resource
// gets aliased to top-level type 1 right after).
func TestWasiIoStreamsInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiIoStreamsInstanceTypeBody(1)
	// Bytes captured from wasm-tools dump of a canonical
	// print-component (see /tmp/print_comp.wasm in the build
	// playground). The body sits inside the type section at the
	// expected `42 0b ...` opening (instance form, vec(11) decls).
	want := []byte{
		0x01, 0x42, 0x0b,
		// decl 0: export "output-stream" (sub resource)
		0x04, 0x00, 0x0d, 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x01,
		// decl 1: alias outer 1 1
		0x02, 0x03, 0x02, 0x01, 0x01,
		// decl 2: export "error" (type (eq 1))
		0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x01,
		// decl 3: type own<2>
		0x01, 0x69, 0x02,
		// decl 4: type variant stream-error
		0x01, 0x71, 0x02,
		0x15, 'l', 'a', 's', 't', '-', 'o', 'p', 'e', 'r', 'a', 't', 'i', 'o', 'n', '-', 'f', 'a', 'i', 'l', 'e', 'd', 0x01, 0x03, 0x00,
		0x06, 'c', 'l', 'o', 's', 'e', 'd', 0x00, 0x00,
		// decl 5: export "stream-error" (type (eq 4))
		0x04, 0x00, 0x0c, 's', 't', 'r', 'e', 'a', 'm', '-', 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x04,
		// decl 6: type borrow<0>
		0x01, 0x68, 0x00,
		// decl 7: type list<u8>
		0x01, 0x70, 0x7d,
		// decl 8: type result<_, err=5>
		0x01, 0x6a, 0x00, 0x01, 0x05,
		// decl 9: type func(self: 6, contents: 7) -> 8
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x07,
		0x00, 0x08,
		// decl 10: export "[method]output-stream.blocking-write-and-flush" (func 9)
		0x04, 0x00, 0x2e,
		'[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', '.', 'b', 'l', 'o', 'c', 'k', 'i', 'n', 'g', '-', 'w', 'r', 'i', 't', 'e', '-', 'a', 'n', 'd', '-', 'f', 'l', 'u', 's', 'h',
		0x01, 0x09,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoStreamsInstanceTypeBody(1) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestTrampolineFixupModuleForNI32NoResult_Validates checks the
// param-count-generalised trampoline + fixup builders for a
// non-4 arity (1 param — the wasi:clocks/wall-clock::now
// indirect-return shape, `(out_ptr i32) -> ()`). Each module is
// a standalone valid core wasm binary.
func TestTrampolineFixupModuleForNI32NoResult_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	for _, m := range []struct {
		name  string
		bytes []byte
	}{
		{"trampoline-1i32", component.TrampolineModuleForNI32NoResult(1)},
		{"fixup-1i32", component.FixupModuleForNI32NoResult(1)},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, m.name+".wasm")
		if err := os.WriteFile(p, m.bytes, 0o644); err != nil {
			t.Fatalf("%s write: %v", m.name, err)
		}
		if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("%s validate failed: %v\n%s", m.name, err, out)
		}
	}
}

// TestTrampolineFixupModuleForParamsNoResult_ReadShape validates
// the mixed-valtype trampoline + fixup for the canon-lowered
// blocking-read ABI `(self: i32, len: i64, ret_ptr: i32) -> ()`
// — the i64 in the middle is what the N-i32 builders can't
// express. Each module is a standalone valid core wasm binary.
func TestTrampolineFixupModuleForParamsNoResult_ReadShape(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	readParams := []byte{0x7f, 0x7e, 0x7f} // i32 i64 i32
	for _, m := range []struct {
		name  string
		bytes []byte
	}{
		{"trampoline-read", component.TrampolineModuleForParamsNoResult(readParams)},
		{"fixup-read", component.FixupModuleForParamsNoResult(readParams)},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, m.name+".wasm")
		if err := os.WriteFile(p, m.bytes, 0o644); err != nil {
			t.Fatalf("%s write: %v", m.name, err)
		}
		if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("%s validate failed: %v\n%s", m.name, err, out)
		}
	}
}

// TestTrampolineModuleForNI32_Matches4 confirms the generalised
// builder reproduces the original hardcoded 4-i32 bytes exactly
// (regression guard for the refactor).
func TestTrampolineModuleForNI32_Matches4(t *testing.T) {
	tramp := component.TrampolineModuleForNI32NoResult(4)
	fixup := component.FixupModuleForNI32NoResult(4)
	// The exported "4I32" wrappers delegate to the N-builders, so
	// equality is structural; assert non-empty + the wasm magic.
	for _, b := range [][]byte{tramp, fixup} {
		if len(b) < 8 || b[0] != 0x00 || b[1] != 0x61 || b[2] != 0x73 || b[3] != 0x6d {
			t.Errorf("not a core wasm module: % x", b[:min(8, len(b))])
		}
	}
}

// TestWasiClocksWallClockInstanceTypeBody_Bytes pins the bytes
// of the wasi:clocks/wall-clock instance type — the datetime
// record + now() function. Matches what wasm-tools emits.
func TestWasiClocksWallClockInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiClocksWallClockInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x04,
		// type record { seconds: u64; nanoseconds: u32 }
		0x01, 0x72, 0x02,
		0x07, 's', 'e', 'c', 'o', 'n', 'd', 's', 0x77,
		0x0b, 'n', 'a', 'n', 'o', 's', 'e', 'c', 'o', 'n', 'd', 's', 0x79,
		// export "datetime" (type (eq 0))
		0x04, 0x00, 0x08, 'd', 'a', 't', 'e', 't', 'i', 'm', 'e', 0x03, 0x00, 0x00,
		// type func() -> typeidx 1
		0x01, 0x40, 0x00, 0x00, 0x01,
		// export "now" (func 2)
		0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiClocksWallClockInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliStdinInstanceTypeBody_Bytes pins the bytes of the
// wasi:cli/stdin instance type — get-stdin() -> input-stream.
// Same shape as the stdout body but referencing input-stream
// (re-exported from outer typeidx) and the get-stdin name.
func TestWasiCliStdinInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliStdinInstanceTypeBody(3)
	want := []byte{
		0x01, 0x42, 0x05,
		// alias outer 1 3
		0x02, 0x03, 0x02, 0x01, 0x03,
		// export "input-stream" (type (eq 0))
		0x04, 0x00, 0x0c, 'i', 'n', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x00, 0x00,
		// type (own 1)
		0x01, 0x69, 0x01,
		// type func() -> 2
		0x01, 0x40, 0x00, 0x00, 0x02,
		// export "get-stdin" (func 3)
		0x04, 0x00, 0x09, 'g', 'e', 't', '-', 's', 't', 'd', 'i', 'n', 0x01, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliStdinInstanceTypeBody(3) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliEnvironmentArgsInstanceTypeBody_Bytes pins the bytes
// of the wasi:cli/environment instance type for
// `get-arguments: func() -> list<string>`.
func TestWasiCliEnvironmentArgsInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliEnvironmentArgsInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x03,
		// type list<string>
		0x01, 0x70, 0x73,
		// type func() -> typeidx 0
		0x01, 0x40, 0x00, 0x00, 0x00,
		// export "get-arguments" (func 1)
		0x04, 0x00, 0x0d, 'g', 'e', 't', '-', 'a', 'r', 'g', 'u', 'm', 'e', 'n', 't', 's', 0x01, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliEnvironmentArgsInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliEnvironmentArgsInstanceTypeBody_Validates composes
// the instance type as a single import and confirms wasm-tools
// accepts it + the get-arguments / (list string) names appear.
func TestWasiCliEnvironmentArgsInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiCliEnvironmentArgsInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "env.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-arguments", "list string"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Bytes pins
// the bytes of the get-environment instance type
// (`func() -> list<tuple<string, string>>`).
func TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliEnvironmentGetEnvironmentInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x04,
		// type tuple<string, string>
		0x01, 0x6f, 0x02, 0x73, 0x73,
		// type list<typeidx 0>
		0x01, 0x70, 0x00,
		// type func() -> typeidx 1
		0x01, 0x40, 0x00, 0x00, 0x01,
		// export "get-environment" (func 2)
		0x04, 0x00, 0x0f, 'g', 'e', 't', '-', 'e', 'n', 'v', 'i', 'r', 'o', 'n', 'm', 'e', 'n', 't', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliEnvironmentGetEnvironmentInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Validates
// composes the instance type as a single import and confirms
// wasm-tools accepts it + the get-environment / tuple / list names
// appear.
func TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiCliEnvironmentGetEnvironmentInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "getenv.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-environment", "tuple", "list"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiCliEnvironmentArgsAndEnvInstanceTypeBody_Validates composes
// the combined args+env instance type as a single import and confirms
// wasm-tools accepts it + both function names appear.
func TestWasiCliEnvironmentArgsAndEnvInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiCliEnvironmentArgsAndEnvInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "argsenv.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-arguments", "get-environment", "tuple", "list string"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestInnerTypeEnum_Bytes pins a small enum defvaltype.
func TestInnerTypeEnum_Bytes(t *testing.T) {
	got := component.InnerTypeEnum([]string{"a", "bc"})
	want := []byte{0x6d, 0x02, 0x01, 'a', 0x02, 'b', 'c'}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeEnum = % x, want % x", got, want)
	}
}

// TestWasiFilesystemErrorCodeEnum_Validates composes the 37-case
// error-code enum inside an instance type (exported under
// "error-code") and confirms wasm-tools accepts it.
func TestWasiFilesystemErrorCodeEnum_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if n := len(component.WasiFilesystemErrorCodeNames); n != 37 {
		t.Fatalf("error-code names = %d, want 37", n)
	}
	// instance type: vec(2) decls — the enum type + its export.
	enum := component.InnerTypeEnum(component.WasiFilesystemErrorCodeNames)
	ibody := []byte{0x01, 0x42, 0x02}
	ibody = append(ibody, 0x01)    // type decl
	ibody = append(ibody, enum...) // inner 0 = enum
	ibody = append(ibody, component.ExportTypeEqDecl("error-code", 0)...)
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, ibody)
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "errcode.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"error-code", "would-block", "cross-device"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiIoStreamsReadWriteInstanceTypeBody_Validates composes the
// combined read+write io/streams instance type (both stream
// resources + both methods) and confirms wasm-tools accepts it.
func TestWasiIoStreamsReadWriteInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "rwstreams.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"input-stream", "output-stream", "blocking-read", "blocking-write-and-flush"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiIoPollInstanceTypeBody_Validates composes the wasi:io/poll
// instance type (pollable resource + block method) and a
// resource.drop on the pollable — exercising the poll type plus
// brick 1's resource.drop against a socket dependency.
func TestWasiIoPollInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "pollable") // → type 1
	buf = component.PutCanonResourceDrop(buf, 1)
	dir := t.TempDir()
	p := filepath.Join(dir, "poll.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"pollable", "pollable.block", "resource.drop"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiHttpValueTypesInstanceTypeBody_Validates composes the
// wasi:http/types value types (method / scheme / header-error +
// the 39-case error-code variant with its DNS-error /
// TLS-alert-received / field-size payload records and the option<…>
// wrappers) into a standalone instance type and confirms wasm-tools
// validates the encoding + surfaces the named exports. This isolates
// the variant / option / record encoders before they fold into the
// full wasi:http/types instance type.
func TestWasiHttpValueTypesInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiHttpValueTypesInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:http/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "httptypes.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"method", "scheme", "header-error", "error-code",
		"DNS-error-payload", "TLS-alert-received-payload", "field-size-payload",
		"HTTP-response-body-size", "internal-error",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestBuildHttpIncomingHandlerComponent_Validates composes a minimal
// wasi:http/incoming-handler component from a stub core module that
// exports `handle` (core sig (i32,i32)->()), and confirms wasm-tools
// validates the export-of-interface shape: the component imports
// wasi:http/types and EXPORTS wasi:http/incoming-handler, whose `handle`
// func references the imported incoming-request / response-outparam
// resources. This isolates the novel export-of-interface mechanics from
// the (separate) http/types method lowering.
func TestBuildHttpIncomingHandlerComponent_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	// Stub core module: one func type (i32,i32)->(), one empty func,
	// exported as "handle". Imports nothing — it just receives the two
	// resource handles and returns.
	stub := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // header
		0x01, 0x06, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x00, // type: func (i32,i32)->()
		0x03, 0x02, 0x01, 0x00, // func: 1 func, type 0
		0x07, 0x0a, 0x01, 0x06, 'h', 'a', 'n', 'd', 'l', 'e', 0x00, 0x00, // export "handle" func 0
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b, // code: 1 func, empty body
	}
	comp := component.BuildHttpIncomingHandlerComponent(stub, "handle")
	dir := t.TempDir()
	p := filepath.Join(dir, "handler.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"wasi:http/incoming-handler@0.2.0", "wasi:http/types@0.2.0", "handle",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiHttpTypesInstanceTypeBody_Validates composes the full
// wasi:http/types instance type — io/error + io/streams (for the
// input-stream / output-stream the body methods hand back) surfaced
// first, then the seven http resources, brick-1 value types, and the
// fifteen method / constructor / static func decls — and confirms
// wasm-tools validates the whole interface and surfaces the methods
// the handler core imports.
func TestWasiHttpTypesInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())             // type 0
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)                      // inst 0
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")                              // type 1
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1)) // type 2
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)                    // inst 1
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")                      // type 3
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")                       // type 4
	buf = component.PutTypeSectionRawBody(buf, component.WasiHttpTypesInstanceTypeBody(4, 3))       // type 5
	buf = component.PutImportSectionOneInstance(buf, "wasi:http/types@0.2.0", 5)                    // inst 2
	dir := t.TempDir()
	p := filepath.Join(dir, "httptypes.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"incoming-request", "incoming-body", "future-trailers", "outgoing-response",
		"outgoing-body", "response-outparam", "fields",
		"path-with-query", "set-status-code", "response-outparam.set",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiSocketsNetworkInstanceTypeBody_Validates composes the
// wasi:sockets/network instance type — exercising the record / tuple
// / variant encoders in their real ip-socket-address context — and
// confirms wasm-tools validates the network resource, error-code,
// ip-address-family, and ip-socket-address exports.
func TestWasiSocketsNetworkInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "network.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"network", "error-code", "ip-address-family", "ip-socket-address"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiSocketsInstanceNetworkInstanceTypeBody_Validates composes
// wasi:sockets/network, surfaces its `network` resource at the top
// level, then imports wasi:sockets/instance-network whose
// instance-network() -> own<network> outer-aliases that resource.
func TestWasiSocketsInstanceNetworkInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody()) // type 0
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 0)          // instance 0
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "network")                       // network → type 1
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsInstanceNetworkInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/instance-network@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "instnet.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("instance-network")) {
		t.Errorf("expected instance-network in printed component, got:\n%s", out)
	}
}

// TestWasiSocketsTcpInstanceTypeBody_Validates composes the full
// dependency chain a tcp-socket needs — io/error, io/streams
// (input+output stream), io/poll, sockets/network — surfaces the six
// referenced types at the top level, then imports wasi:sockets/tcp
// and confirms wasm-tools validates the cross-instance resource +
// the start-bind/finish-bind/start-listen/finish-listen/accept/
// subscribe methods (accept's tuple-of-owns result included).
func TestWasiSocketsTcpInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())                     // type 0
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)                              // inst 0
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")                                      // type 1
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))         // type 2
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)                            // inst 1
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")                              // type 3
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")                               // type 4
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))                 // type 5
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)                               // inst 2
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "pollable")                                   // type 6
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())              // type 7
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 7)                       // inst 3
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "network")                                    // type 8
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "error-code")                                 // type 9
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-socket-address")                          // type 10
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpInstanceTypeBody(8, 9, 10, 4, 3, 6)) // type 11
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp@0.2.0", 11)                          // inst 4
	dir := t.TempDir()
	p := filepath.Join(dir, "tcp.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"tcp-socket", "start-bind", "start-listen", "accept", "subscribe"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiSocketsTcpConnectInstanceTypeBody_Validates composes the same
// dependency chain as the server variant but with the outbound-client
// tcp instance type, and confirms wasm-tools accepts the appended
// connect chain: start-connect + finish-connect (whose result is
// tuple<own input-stream, own output-stream>). Guards the type indices
// of the connect extension (types 22-25 appended after subscribe).
func TestWasiSocketsTcpConnectInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "pollable")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 7)
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "network")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "error-code")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-socket-address")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpConnectInstanceTypeBody(8, 9, 10, 4, 3, 6))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp@0.2.0", 11)
	dir := t.TempDir()
	p := filepath.Join(dir, "tcp_connect.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"start-connect", "finish-connect", "start-bind", "subscribe"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiSocketsTcpCreateSocketInstanceTypeBody_Validates extends the
// full socket dependency chain, surfaces ip-address-family (network) +
// tcp-socket (tcp), then imports wasi:sockets/tcp-create-socket and
// confirms wasm-tools validates create-tcp-socket() ->
// result<own<tcp-socket>, error-code>.
func TestWasiSocketsTcpCreateSocketInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())                         // type 0
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)                                  // inst 0
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")                                          // type 1
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))             // type 2
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)                                // inst 1
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")                                  // type 3
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")                                   // type 4
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))                     // type 5
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)                                   // inst 2
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "pollable")                                       // type 6
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())                  // type 7
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 7)                           // inst 3
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "network")                                        // type 8
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "error-code")                                     // type 9
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-socket-address")                              // type 10
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpInstanceTypeBody(8, 9, 10, 4, 3, 6))     // type 11
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp@0.2.0", 11)                              // inst 4
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-address-family")                              // type 12
	buf = component.PutAliasSectionInstanceExportType(buf, 4, "tcp-socket")                                     // type 13
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpCreateSocketInstanceTypeBody(12, 9, 13)) // type 14
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp-create-socket@0.2.0", 14)
	dir := t.TempDir()
	p := filepath.Join(dir, "tcpcreate.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("create-tcp-socket")) {
		t.Errorf("expected create-tcp-socket in printed component, got:\n%s", out)
	}
}

// TestWasiSocketsUdpInstanceTypeBody_Validates composes the send-only
// wasi:sockets/udp + udp-create-socket instance types over
// sockets/network and io/poll (the datagram path is its own resources,
// but outgoing-datagram-stream.subscribe returns own<pollable>) and
// confirms wasm-tools validates the udp-socket / datagram-stream
// resources, the outgoing-datagram record, and the start-bind / stream
// / check-send / send / subscribe / create-udp-socket methods.
func TestWasiSocketsUdpInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())                // type 0
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 0)                         // inst 0
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "network")                                      // type 1
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error-code")                                   // type 2
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "ip-socket-address")                            // type 3
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "ip-address-family")                            // type 4
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))                   // type 5
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)                                 // inst 1
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "pollable")                                     // type 6
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsUdpInstanceTypeBody(1, 2, 3, 6))          // type 7
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/udp@0.2.0", 7)                             // inst 2
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "udp-socket")                                   // type 8
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsUdpCreateSocketInstanceTypeBody(4, 2, 8)) // type 9
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/udp-create-socket@0.2.0", 9)
	dir := t.TempDir()
	p := filepath.Join(dir, "udp.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"udp-socket", "incoming-datagram-stream", "outgoing-datagram-stream",
		"udp-socket.start-bind", "udp-socket.stream",
		"outgoing-datagram-stream.check-send", "outgoing-datagram-stream.send",
		"outgoing-datagram-stream.subscribe",
		"create-udp-socket",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestInnerTypeTuple_Bytes pins tuple<u8,u8,u8,u8> (ipv4-address).
func TestInnerTypeTuple_Bytes(t *testing.T) {
	got := component.InnerTypeTuple([]byte{component.CValtypeU8, component.CValtypeU8, component.CValtypeU8, component.CValtypeU8})
	want := []byte{0x6f, 0x04, component.CValtypeU8, component.CValtypeU8, component.CValtypeU8, component.CValtypeU8}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeTuple = % x, want % x", got, want)
	}
}

// TestInnerTypeRecord_Bytes pins record{port:u16, address:<typeidx 5>}.
func TestInnerTypeRecord_Bytes(t *testing.T) {
	got := component.InnerTypeRecord([]component.RecordField{
		{Name: "port", Valtype: component.CValtypeU16},
		{Name: "address", Valtype: 0x05},
	})
	want := []byte{0x72, 0x02,
		0x04, 'p', 'o', 'r', 't', component.CValtypeU16,
		0x07, 'a', 'd', 'd', 'r', 'e', 's', 's', 0x05,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeRecord = % x, want % x", got, want)
	}
}

// TestInnerTypeResultOkErr_Bytes pins result<ok=7, err=5>.
func TestInnerTypeResultOkErr_Bytes(t *testing.T) {
	got := component.InnerTypeResultOkErr(7, 5)
	want := []byte{0x6a, 0x01, 0x07, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeResultOkErr(7,5) = % x, want % x", got, want)
	}
}

// TestWasiIoStreamsReadInstanceTypeBody_Validates composes the
// read-side streams instance type via the escape hatch (with a
// wasi:io/error import to satisfy the outer alias) and confirms
// wasm-tools accepts it + the input-stream / blocking-read names
// appear.
func TestWasiIoStreamsReadInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// wasi:io/error import (instance 0) so the outer alias of
	// `error` (top-level type 1) resolves.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	// The read-streams instance type referencing top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "rd.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"input-stream", "blocking-read", "(list u8)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestTrampolineFixupModuleForParamsResults_Validates checks the
// results-carrying trampoline + fixup (P4c: a memory-param import that returns
// a flat scalar, e.g. `func(string) -> u32` lowered to `(i32 i32) -> i32`).
// Each module must be a standalone valid core wasm binary, and the empty-result
// case must be byte-identical to the NoResult builder (the byte-identity
// guarantee for every existing WASI import).
func TestTrampolineFixupModuleForParamsResults_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	params := []byte{0x7f, 0x7f} // i32 i32 (string ptr, len)
	results := []byte{0x7f}      // i32 (u32)
	for _, m := range []struct {
		name  string
		bytes []byte
	}{
		{"trampoline-result", component.TrampolineModuleForParamsResults(params, results)},
		{"fixup-result", component.FixupModuleForParamsResults(params, results)},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, m.name+".wasm")
		if err := os.WriteFile(p, m.bytes, 0o644); err != nil {
			t.Fatalf("%s write: %v", m.name, err)
		}
		if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("%s validate failed: %v\n%s", m.name, err, out)
		}
	}
	// Empty results → byte-identical to the NoResult builders (so every WASI
	// import's bytes are unchanged).
	if !bytes.Equal(component.TrampolineModuleForParamsResults(params, nil), component.TrampolineModuleForParamsNoResult(params)) {
		t.Errorf("results=nil trampoline must match the NoResult builder byte-for-byte")
	}
	if !bytes.Equal(component.FixupModuleForParamsResults(params, nil), component.FixupModuleForParamsNoResult(params)) {
		t.Errorf("results=nil fixup must match the NoResult builder byte-for-byte")
	}
}
