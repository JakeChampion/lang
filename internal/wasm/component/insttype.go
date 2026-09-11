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

// fsNeeds says what a filesystem instance type's prelude must declare.
// The stream directions are aliased in from io/streams; a body only
// declares the ones it uses, because a read-only wrap that mentioned
// output-stream would make the composed component import a stream
// direction the core module never touches. unit is the same idea for
// `result<_, error-code>`: only the path-mutating methods return it.
type fsNeeds struct {
	in, out  uint32 // outer typeidx; 0 means "not needed"
	needIn   bool
	needOut  bool
	needUnit bool
	// descriptor-type is named by BOTH stat-at's descriptor-stat and
	// read-directory's directory-entry, and a type can only be declared
	// once, so it lives in the prelude rather than in either block.
	needDescType bool
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
	rUnit     uint32 // result<_, error-code>
	descType  uint32 // descriptor-type enum (stat-at + read-directory)
	// wall-clock's `datetime`, declared on first use rather than in the
	// prelude: both `descriptor-stat` and `new-timestamp` reference it,
	// and an instance type may export the name only once. See
	// fsDatetimeOnce.
	datetime     uint32
	haveDatetime bool
}

// fsDatetimeOnce declares `datetime` — the {u64 seconds, u32 nanoseconds}
// record wasi:filesystem/types re-exports from wasi:clocks/wall-clock — the
// first time a method needs it, and hands back the same typeidx after that.
//
// It is declared inline rather than outer-aliased from the clock interface
// (which is how the WIT's `use` spells it). Record types match structurally,
// so an inline declaration of the same shape is accepted — and it keeps a
// program that only reads timestamps from importing a clock it never asks the
// time of.
func fsDatetimeOnce(b *instTypeBuilder, v *fsVocab) uint32 {
	if !v.haveDatetime {
		v.datetime = b.defExport(InnerTypeRecord([]RecordField{
			{Name: "seconds", Valtype: CValtypeU64},
			{Name: "nanoseconds", Valtype: CValtypeU32},
		}), "datetime")
		v.haveDatetime = true
	}
	return v.datetime
}

// fsPrelude emits the descriptor resource, the requested stream aliases,
// error-code, the three flag types, and the own/borrow/result types the
// method signatures reference — in the exact order the hand-written
// bodies used.
//
// Everything optional is emitted LAST within its group, so a body that
// asks for less produces a byte-identical prefix of one that asks for
// more. That is what lets the method set grow without renumbering the
// bodies that were pinned before it grew.
func fsPrelude(b *instTypeBuilder, s fsNeeds) fsVocab {
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
	if s.needUnit {
		v.rUnit = b.def(InnerTypeResultErr(v.errC))
	}
	if s.needDescType {
		// Case order fixes the discriminants, and they are NOT the
		// preview-1 filetype numbers: a directory is 3 in both, but a
		// regular file is 6 here and 4 there.
		v.descType = b.defExport(InnerTypeEnum([]string{
			"unknown", "block-device", "character-device", "directory",
			"fifo", "symbolic-link", "regular-file", "socket",
		}), "descriptor-type")
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

// fsStat emits the `stat-at` and / or `stat` methods — `atPath` and
// `self` select which — and the four types their shared result needs.
// stat-at is the only filesystem method whose signature is not
// expressible in the vocabulary fsPrelude already has, which is why it
// carries its own type block rather than taking typeidxs from fsVocab;
// `stat` differs from it only in taking no path.
//
// The record has to be declared IN FULL even though Fern's `stat`
// surfaces only two of its six fields: an imported instance type is
// matched against what the host exports, so a `descriptor-stat` missing
// its three timestamps is a different type and the component fails to
// instantiate. The reader in wasi_fs_dir.go skips the fields it does
// not want by offset.
func fsStat(b *instTypeBuilder, v *fsVocab, atPath, self bool) {
	optDatetime := b.def(InnerTypeOption(byte(fsDatetimeOnce(b, v))))
	stat := b.defExport(InnerTypeRecord([]RecordField{
		{Name: "type", Valtype: byte(v.descType)},
		{Name: "link-count", Valtype: CValtypeU64},
		{Name: "size", Valtype: CValtypeU64},
		{Name: "data-access-timestamp", Valtype: byte(optDatetime)},
		{Name: "data-modification-timestamp", Valtype: byte(optDatetime)},
		{Name: "status-change-timestamp", Valtype: byte(optDatetime)},
	}), "descriptor-stat")
	rStat := b.def(InnerTypeResultOkErr(stat, v.errC))
	if atPath {
		b.funcExport(tcpMethodFuncDecl("stat-at",
			[]string{"self", "path-flags", "path"},
			[]byte{byte(v.bDesc), byte(v.pathFlags), CValtypeString},
			byte(rStat)),
			"[method]descriptor.stat-at")
	}
	if self {
		b.funcExport(tcpMethodFuncDecl("stat",
			[]string{"self"}, []byte{byte(v.bDesc)}, byte(rStat)),
			"[method]descriptor.stat")
	}
}

// fsReadDir emits the directory-listing surface: `read-directory` on
// the descriptor, the `directory-entry-stream` resource it returns, and
// that stream's `read-directory-entry` pull method.
//
// This is the only filesystem shape with a SECOND resource. A listing
// is not a value the host can hand over whole — it is a cursor the
// guest pumps — so `read-directory` returns an owned handle and the
// caller loops on `read-directory-entry` until it yields `none`, then
// drops the handle. The drop is a canonical `resource.drop`, declared
// by the composer alongside the methods rather than here.
//
// Note what the stream does NOT yield: wasi-filesystem specifies that
// `.` and `..` are absent, unlike preview-1's fd_readdir, which yields
// both and makes the preview-1 body filter them out.
func fsReadDir(b *instTypeBuilder, v fsVocab) {
	stream := b.sub("directory-entry-stream")
	entry := b.defExport(InnerTypeRecord([]RecordField{
		{Name: "type", Valtype: byte(v.descType)},
		{Name: "name", Valtype: CValtypeString},
	}), "directory-entry")
	ownStream := b.def([]byte{0x69, byte(stream)})
	rStream := b.def(InnerTypeResultOkErr(ownStream, v.errC))
	optEntry := b.def(InnerTypeOption(byte(entry)))
	rEntry := b.def(InnerTypeResultOkErr(optEntry, v.errC))
	b.funcExport(tcpMethodFuncDecl("read-directory",
		[]string{"self"}, []byte{byte(v.bDesc)}, byte(rStream)),
		"[method]descriptor.read-directory")
	bStream := b.def([]byte{0x68, byte(stream)})
	b.funcExport(tcpMethodFuncDecl("read-directory-entry",
		[]string{"self"}, []byte{byte(bStream)}, byte(rEntry)),
		"[method]directory-entry-stream.read-directory-entry")
}

// fsLinkAt declares `link-at: func(self: borrow<descriptor>, old-path-flags:
// path-flags, old-path: string, new-descriptor: borrow<descriptor>, new-path:
// string) -> result<_, error-code>`. It is the one path method with a SECOND
// descriptor: a hard link can cross directories, so the new name is resolved
// against its own.
func fsLinkAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("link-at",
		[]string{"self", "old-path-flags", "old-path", "new-descriptor", "new-path"},
		[]byte{byte(v.bDesc), byte(v.pathFlags), CValtypeString, byte(v.bDesc), CValtypeString},
		byte(v.rUnit)),
		"[method]descriptor.link-at")
}

// fsSymlinkAt declares `symlink-at: func(self: borrow<descriptor>, old-path:
// string, new-path: string) -> result<_, error-code>`. `old-path` is the
// target STORED in the link, never resolved, which is why it takes no
// descriptor and no path-flags.
func fsSymlinkAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("symlink-at",
		[]string{"self", "old-path", "new-path"},
		[]byte{byte(v.bDesc), CValtypeString, CValtypeString},
		byte(v.rUnit)),
		"[method]descriptor.symlink-at")
}

// fsReadlinkAt declares `readlink-at: func(self: borrow<descriptor>, path:
// string) -> result<string, error-code>` — the only path method whose ok arm
// carries a value, so it defines its own result type rather than sharing the
// mutators' `result<_, error-code>`.
func fsReadlinkAt(b *instTypeBuilder, v fsVocab) {
	rStr := b.def(InnerTypeResultOkErr(CValtypeString, v.errC))
	b.funcExport(tcpMethodFuncDecl("readlink-at",
		[]string{"self", "path"},
		[]byte{byte(v.bDesc), CValtypeString},
		byte(rStr)),
		"[method]descriptor.readlink-at")
}

// fsRenameAt declares `rename-at: func(self: borrow<descriptor>, old-path:
// string, new-descriptor: borrow<descriptor>, new-path: string) ->
// result<_, error-code>`. Like link-at it names a second descriptor, and
// unlike link-at it takes no path-flags: a rename moves the entry itself, so
// there is no final symlink to follow or not.
func fsRenameAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("rename-at",
		[]string{"self", "old-path", "new-descriptor", "new-path"},
		[]byte{byte(v.bDesc), CValtypeString, byte(v.bDesc), CValtypeString},
		byte(v.rUnit)),
		"[method]descriptor.rename-at")
}

// fsSetTimesAt declares `set-times-at: func(self: borrow<descriptor>,
// path-flags: path-flags, path: string, data-access-timestamp: new-timestamp,
// data-modification-timestamp: new-timestamp) -> result<_, error-code>`.
//
// It is the only method here whose arguments carry a VARIANT.
// `new-timestamp`'s three arms are how preview 2 spells "leave this one
// alone", "use the host's clock" and a value; it is declared here rather
// than in the prelude because nothing else references it, so a body that
// does not ask for this method keeps the type numbering it had.
func fsSetTimesAt(b *instTypeBuilder, v *fsVocab) {
	datetime := fsDatetimeOnce(b, v)
	newTimestamp := b.defExport(InnerTypeVariant([]VariantCase{
		{Name: "no-change"},
		{Name: "now"},
		{Name: "timestamp", HasPayload: true, PayloadValtype: byte(datetime)},
	}), "new-timestamp")
	b.funcExport(tcpMethodFuncDecl("set-times-at",
		[]string{"self", "path-flags", "path", "data-access-timestamp", "data-modification-timestamp"},
		[]byte{byte(v.bDesc), byte(v.pathFlags), CValtypeString, byte(newTimestamp), byte(newTimestamp)},
		byte(v.rUnit)),
		"[method]descriptor.set-times-at")
}

// fsSetSize declares `set-size: func(self: borrow<descriptor>, size:
// filesize) -> result<_, error-code>`. `filesize` is a plain u64, so unlike
// every other method here it names no path: preview 2 has no path-based
// set-size, which is why `truncate` opens a descriptor of its own.
func fsSetSize(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("set-size",
		[]string{"self", "size"},
		[]byte{byte(v.bDesc), CValtypeU64},
		byte(v.rUnit)),
		"[method]descriptor.set-size")
}

// fsPathMutator emits one of the path-mutating methods —
// `unlink-file-at`, `create-directory-at`, `remove-directory-at`. They
// share a signature exactly: (borrow<descriptor>, string) ->
// result<_, error-code>. Note the absent path-flags parameter, which
// open-at and stat-at both take; these three do not follow symlinks and
// the WIT gives them no flags argument.
func fsPathMutator(b *instTypeBuilder, v fsVocab, method string) {
	b.funcExport(tcpMethodFuncDecl(method,
		[]string{"self", "path"}, []byte{byte(v.bDesc), CValtypeString}, byte(v.rUnit)),
		"[method]descriptor."+method)
}
