package component

import "github.com/jakechampion/lang/internal/wasm/leb128"

type instTypeBuilder struct {
	decls []byte
	idx   uint32
	count uint32
}

func (b *instTypeBuilder) emit(d []byte) {
	b.decls = append(b.decls, d...)
	b.count++
}

func (b *instTypeBuilder) takeIdx() uint32 {
	i := b.idx
	b.idx++
	return i
}

func (b *instTypeBuilder) def(deftype []byte) uint32 {
	b.emit(append([]byte{0x01}, deftype...))
	return b.takeIdx()
}

func (b *instTypeBuilder) sub(name string) uint32 {
	b.emit(ExportSubResourceDecl(name))
	return b.takeIdx()
}

func (b *instTypeBuilder) aliasExport(outerTypeidx uint32, name string) uint32 {
	b.emit(OuterAliasTypeDecl(1, outerTypeidx))
	inner := b.takeIdx()
	b.emit(ExportTypeEqDecl(name, inner))
	return b.takeIdx()
}

func (b *instTypeBuilder) defExport(deftype []byte, name string) uint32 {
	b.emit(append([]byte{0x01}, deftype...))
	inner := b.takeIdx()
	b.emit(ExportTypeEqDecl(name, inner))
	return b.takeIdx()
}

func (b *instTypeBuilder) funcExport(functype []byte, name string) {
	b.emit(functype)
	fn := b.takeIdx()
	export := append([]byte{0x04, 0x00, byte(len(name))}, name...)
	b.emit(append(export, 0x01, byte(fn)))
}

func (b *instTypeBuilder) body() []byte {
	out := []byte{0x01, 0x42}
	out = leb128.UlebU64(out, uint64(b.count))
	return append(out, b.decls...)
}

type fsNeeds struct {
	in, out      uint32
	needIn       bool
	needOut      bool
	needUnit     bool
	needDescType bool
}

type fsVocab struct {
	desc         uint32
	inS, outS    uint32
	errC         uint32
	pathFlags    uint32
	openFlags    uint32
	descFlags    uint32
	bDesc        uint32
	rDesc        uint32
	rIn, rOut    uint32
	rUnit        uint32
	descType     uint32
	datetime     uint32
	haveDatetime bool
}

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
		v.descType = b.defExport(InnerTypeEnum([]string{
			"unknown", "block-device", "character-device", "directory",
			"fifo", "symbolic-link", "regular-file", "socket",
		}), "descriptor-type")
	}
	return v
}

func fsOpenAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("open-at",
		[]string{"self", "path-flags", "path", "open-flags", "flags"},
		[]byte{byte(v.bDesc), byte(v.pathFlags), CValtypeString, byte(v.openFlags), byte(v.descFlags)},
		byte(v.rDesc)),
		"[method]descriptor.open-at")
}

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

func fsLinkAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("link-at",
		[]string{"self", "old-path-flags", "old-path", "new-descriptor", "new-path"},
		[]byte{byte(v.bDesc), byte(v.pathFlags), CValtypeString, byte(v.bDesc), CValtypeString},
		byte(v.rUnit)),
		"[method]descriptor.link-at")
}

func fsSymlinkAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("symlink-at",
		[]string{"self", "old-path", "new-path"},
		[]byte{byte(v.bDesc), CValtypeString, CValtypeString},
		byte(v.rUnit)),
		"[method]descriptor.symlink-at")
}

func fsReadlinkAt(b *instTypeBuilder, v fsVocab) {
	rStr := b.def(InnerTypeResultOkErr(CValtypeString, v.errC))
	b.funcExport(tcpMethodFuncDecl("readlink-at",
		[]string{"self", "path"},
		[]byte{byte(v.bDesc), CValtypeString},
		byte(rStr)),
		"[method]descriptor.readlink-at")
}

func fsRenameAt(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("rename-at",
		[]string{"self", "old-path", "new-descriptor", "new-path"},
		[]byte{byte(v.bDesc), CValtypeString, byte(v.bDesc), CValtypeString},
		byte(v.rUnit)),
		"[method]descriptor.rename-at")
}

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

func fsSetSize(b *instTypeBuilder, v fsVocab) {
	b.funcExport(tcpMethodFuncDecl("set-size",
		[]string{"self", "size"},
		[]byte{byte(v.bDesc), CValtypeU64},
		byte(v.rUnit)),
		"[method]descriptor.set-size")
}

func fsSyncSelf(b *instTypeBuilder, v fsVocab, method string) {
	b.funcExport(tcpMethodFuncDecl(method,
		[]string{"self"}, []byte{byte(v.bDesc)}, byte(v.rUnit)),
		"[method]descriptor."+method)
}

func fsAdvise(b *instTypeBuilder, v fsVocab) {
	advice := b.defExport(InnerTypeEnum([]string{
		"normal", "sequential", "random", "will-need", "dont-need", "no-reuse",
	}), "advice")
	b.funcExport(tcpMethodFuncDecl("advise",
		[]string{"self", "offset", "length", "advice"},
		[]byte{byte(v.bDesc), CValtypeU64, CValtypeU64, byte(advice)},
		byte(v.rUnit)),
		"[method]descriptor.advise")
}

func fsPathMutator(b *instTypeBuilder, v fsVocab, method string) {
	b.funcExport(tcpMethodFuncDecl(method,
		[]string{"self", "path"}, []byte{byte(v.bDesc), CValtypeString}, byte(v.rUnit)),
		"[method]descriptor."+method)
}
