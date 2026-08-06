package component

import "github.com/jakechampion/lang/internal/wasm/leb128"

// instTypeBuilder assembles a component INSTANCE TYPE body — the
// `0x01 0x42 <count> <decls…>` shape every `Wasi…InstanceTypeBody`
// returns — while tracking the two things that are easy to get wrong by
// hand: how many decls have been emitted, and what typeidx each one
// landed at.
//
// The index bookkeeping is the whole point. A component instance type
// numbers its inner types in emission order, but NOT every decl consumes
// an index: a type decl, a sub-resource export and a type export each
// take one, while a FUNC export does not — it only names a functype that
// was defined by the decl before it. Hand-written bodies therefore carry
// comments like "func read-via-stream (func 17 — func-exports don't
// consume a typeidx, so the read functype is typeidx 17, not 18)", and
// every such number has to be re-derived by hand whenever a method is
// added or removed.
//
// That is what made the filesystem instance types combinatorial. The
// component model requires an imported instance type to declare exactly
// the methods the core module imports, so each SUBSET of the methods a
// program might use needs its own body — and each was hand-numbered.
// Adding the directory + metadata methods (#6208 part 2) to that scheme
// would have meant a hand-written body per subset of a much larger set.
// With the builder, a body is a list of what it needs and the indices
// fall out.
//
// Two of the bodies (read-write and read-write-append) already had these
// helpers inline as local closures, duplicated between them; this is
// those closures hoisted, with the remaining four bodies moved onto them.
type instTypeBuilder struct {
	decls []byte
	idx   uint32 // next typeidx to hand out
	count uint32 // decls emitted
}

// emit appends one decl and counts it. It does NOT advance the typeidx —
// callers that define a type use the helpers below.
func (b *instTypeBuilder) emit(d []byte) {
	b.decls = append(b.decls, d...)
	b.count++
}

// takeIdx consumes and returns the next typeidx.
func (b *instTypeBuilder) takeIdx() uint32 {
	i := b.idx
	b.idx++
	return i
}

// def emits an inner type definition (`0x01 <deftype>`) and returns its
// typeidx. Used for the anonymous types a signature needs —
// `own<T>` / `borrow<T>` / `result<ok, err>` — which are referenced by
// index and never exported by name.
func (b *instTypeBuilder) def(deftype []byte) uint32 {
	b.emit(append([]byte{0x01}, deftype...))
	return b.takeIdx()
}

// sub emits an exported sub-resource declaration (`descriptor`,
// `directory-entry-stream`, …) and returns its typeidx.
func (b *instTypeBuilder) sub(name string) uint32 {
	b.emit(ExportSubResourceDecl(name))
	return b.takeIdx()
}

// aliasExport pulls a type in from an enclosing scope (an `input-stream`
// from wasi:io/streams, say) and re-exports it under `name`, returning
// the typeidx of the EXPORT — the one a signature may reference, since
// an import's public signature can only name exported types.
func (b *instTypeBuilder) aliasExport(outerTypeidx uint32, name string) uint32 {
	b.emit(OuterAliasTypeDecl(1, outerTypeidx))
	inner := b.takeIdx()
	b.emit(ExportTypeEqDecl(name, inner))
	return b.takeIdx()
}

// defExport defines an inner type and exports it under `name`, returning
// the EXPORT's typeidx for the same reason as aliasExport.
func (b *instTypeBuilder) defExport(deftype []byte, name string) uint32 {
	b.emit(append([]byte{0x01}, deftype...))
	inner := b.takeIdx()
	b.emit(ExportTypeEqDecl(name, inner))
	return b.takeIdx()
}

// funcExport emits a functype followed by its export. The functype
// consumes a typeidx; the export does not, which is the asymmetry the
// hand-written bodies had to track in comments.
func (b *instTypeBuilder) funcExport(functype []byte, name string) {
	b.emit(functype)
	fn := b.takeIdx()
	export := append([]byte{0x04, 0x00, byte(len(name))}, name...)
	b.emit(append(export, 0x01, byte(fn)))
}

// body closes the builder into the instance-type body.
func (b *instTypeBuilder) body() []byte {
	out := []byte{0x01, 0x42}
	out = leb128.UlebU64(out, uint64(b.count))
	return append(out, b.decls...)
}

// ---- wasi:filesystem/types@0.2.0 -------------------------------------
//
// The shared vocabulary every filesystem instance type declares, in the
// order the hand-written bodies declared it. Order is load-bearing: the
// emitted bytes are pinned byte-for-byte by
// TestWasiFilesystemTypesInstanceTypeBodies_Golden, because these types
// are what a composed component's imports are matched against.

// fsStreams says which io/streams handles a filesystem instance type
// needs to alias in. A body only declares the directions it uses — a
// read-only wrap must not mention output-stream, or the composed
// component imports a stream direction the core module never touches.
type fsStreams struct {
	in, out uint32 // outer typeidx; 0 means "not needed"
	needIn  bool
	needOut bool
}

// fsVocab is the set of typeidxs a filesystem method signature can name.
type fsVocab struct {
	desc      uint32 // descriptor resource
	inS, outS uint32 // exported input-stream / output-stream
	errC      uint32 // exported error-code
	pathFlags uint32
	openFlags uint32
	descFlags uint32
	bDesc     uint32 // borrow<descriptor>
	rDesc     uint32 // result<own<descriptor>, error-code>
	rIn, rOut uint32 // result<own<stream>, error-code>
}

// fsPrelude emits the descriptor resource, the requested stream aliases,
// error-code, the three flag types, and the own/borrow/result types the
// method signatures reference — in the exact order the hand-written
// bodies used.
func fsPrelude(b *instTypeBuilder, s fsStreams) fsVocab {
	var v fsVocab
	v.desc = b.sub("descriptor")
	if s.needIn {
		v.inS = b.aliasExport(s.in, "input-stream")
	}
	if s.needOut {
		v.outS = b.aliasExport(s.out, "output-stream")
	}
	v.errC = b.defExport(InnerTypeEnum(WasiFilesystemErrorCodeNames), "error-code")
	v.pathFlags = b.defExport(InnerTypeFlags([]string{"symlink-follow"}), "path-flags")
	v.openFlags = b.defExport(InnerTypeFlags([]string{"create", "directory", "exclusive", "truncate"}), "open-flags")
	v.descFlags = b.defExport(InnerTypeFlags([]string{
		"read", "write", "file-integrity-sync", "data-integrity-sync",
		"requested-write-sync", "mutate-directory",
	}), "descriptor-flags")

	ownDesc := b.def([]byte{0x69, byte(v.desc)})
	var ownIn, ownOut uint32
	if s.needIn {
		ownIn = b.def([]byte{0x69, byte(v.inS)})
	}
	if s.needOut {
		ownOut = b.def([]byte{0x69, byte(v.outS)})
	}
	v.bDesc = b.def([]byte{0x68, byte(v.desc)})
	v.rDesc = b.def(InnerTypeResultOkErr(ownDesc, v.errC))
	if s.needIn {
		v.rIn = b.def(InnerTypeResultOkErr(ownIn, v.errC))
	}
	if s.needOut {
		v.rOut = b.def(InnerTypeResultOkErr(ownOut, v.errC))
	}
	return v
}

// fsOpenAt emits the open-at method — every path-based body has it.
func fsOpenAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("open-at",
		[]string{"self", "path-flags", "path", "open-flags", "flags"},
		[]byte{byte(v.bDesc), byte(v.pathFlags), CValtypeString, byte(v.openFlags), byte(v.descFlags)},
		byte(v.rDesc)),
		"[method]descriptor.open-at")
}

// fsViaStream emits one of the three `*-via-stream` methods. `append` has
// no offset parameter; the other two take a u64.
func fsViaStream(b *instTypeBuilder, v fsVocab, method string, result uint32) {
	if method == "append-via-stream" {
		b.funcExport(tcpMethodFuncDecl(method,
			[]string{"self"}, []byte{byte(v.bDesc)}, byte(result)),
			"[method]descriptor."+method)
		return
	}
	b.funcExport(tcpMethodFuncDecl(method,
		[]string{"self", "offset"}, []byte{byte(v.bDesc), CValtypeU64}, byte(result)),
		"[method]descriptor."+method)
}
