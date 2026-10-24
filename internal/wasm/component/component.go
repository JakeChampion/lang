package component

import (
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

const (
	SectionCustom       = 0
	SectionCoreModule   = 1
	SectionCoreInstance = 2
	SectionCoreType     = 3
	SectionComponent    = 4
	SectionInstance     = 5
	SectionAlias        = 6
	SectionType         = 7
	SectionCanon        = 8
	SectionStart        = 9
	SectionImport       = 10
	SectionExport       = 11
)

const (
	CoreSortFunc     = 0x00
	CoreSortTable    = 0x01
	CoreSortMemory   = 0x02
	CoreSortGlobal   = 0x03
	CoreSortType     = 0x10
	CoreSortModule   = 0x11
	CoreSortInstance = 0x12
)

const (
	CValtypeBool   = 0x7f
	CValtypeS8     = 0x7e
	CValtypeU8     = 0x7d
	CValtypeS16    = 0x7c
	CValtypeU16    = 0x7b
	CValtypeS32    = 0x7a
	CValtypeU32    = 0x79
	CValtypeS64    = 0x78
	CValtypeU64    = 0x77
	CValtypeF32    = 0x76
	CValtypeF64    = 0x75
	CValtypeChar   = 0x74
	CValtypeString = 0x73
)

func PutComponentHeader(buf []byte) []byte {
	return append(buf,
		0x00, 0x61, 0x73, 0x6d,
		0x0d, 0x00,
		0x01, 0x00,
	)
}

func PutCoreModuleSection(buf, core []byte) []byte {
	buf = append(buf, SectionCoreModule)
	buf = leb128.UlebU64(buf, uint64(len(core)))
	buf = append(buf, core...)
	return buf
}

func PutComponentSection(buf, comp []byte) []byte {
	buf = append(buf, SectionComponent)
	buf = leb128.UlebU64(buf, uint64(len(comp)))
	buf = append(buf, comp...)
	return buf
}

func PutInstanceSectionInstantiateComponent(buf []byte, componentIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(componentIdx))
	body = leb128.UlebU64(body, 0)
	return wrapSection(buf, SectionInstance, body)
}

func PutComponentImportSectionFuncs(buf []byte, names []string, typeidxs []uint32) []byte {
	if len(names) != len(typeidxs) {
		panic("component: names and typeidxs must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, uint64(len(names)))
	for i := range names {
		body = append(body, 0x00)
		body = putName(body, names[i])
		body = append(body, 0x01)
		body = leb128.UlebU64(body, uint64(typeidxs[i]))
	}
	return wrapSection(buf, SectionImport, body)
}

func PutInstanceSectionInstantiateComponentWithFuncArgs(buf []byte, componentIdx uint32, argNames []string, funcIdxs []uint32) []byte {
	if len(argNames) != len(funcIdxs) {
		panic("component: argNames and funcIdxs must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(componentIdx))
	body = leb128.UlebU64(body, uint64(len(argNames)))
	for i := range argNames {
		body = putName(body, argNames[i])
		body = append(body, 0x01)
		body = leb128.UlebU64(body, uint64(funcIdxs[i]))
	}
	return wrapSection(buf, SectionInstance, body)
}

func putName(buf []byte, s string) []byte {
	buf = leb128.UlebU64(buf, uint64(len(s)))
	buf = append(buf, s...)
	return buf
}

func wrapSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = leb128.UlebU64(buf, uint64(len(body)))
	buf = append(buf, body...)
	return buf
}

func FixupModuleForNI32NoResult(nparams int) []byte {
	return FixupModuleForParamsNoResult(repeatByte(0x7f, nparams))
}

func FixupModuleForParamsNoResult(paramValtypes []byte) []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = append(typeBody, 0x00)
	out = appendCoreSection(out, 0x01, typeBody)
	importBody := []byte{
		0x02,
		0x00, 0x01, '0', 0x00, 0x00,
		0x00, 0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x70, 0x01, 0x01, 0x01,
	}
	out = appendCoreSection(out, 0x02, importBody)
	elemBody := []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x00}
	out = appendCoreSection(out, 0x09, elemBody)
	return out
}

func appendCoreSection(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id)
	buf = leb128.UlebU64(buf, uint64(len(body)))
	return append(buf, body...)
}

func TrampolineModuleForNI32NoResult(nparams int) []byte {
	return TrampolineModuleForParamsNoResult(repeatByte(0x7f, nparams))
}

func TrampolineModuleForParamsNoResult(paramValtypes []byte) []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = append(typeBody, 0x00)
	out = appendCoreSection(out, 0x01, typeBody)
	out = appendCoreSection(out, 0x03, []byte{0x01, 0x00})
	out = appendCoreSection(out, 0x04, []byte{0x01, 0x70, 0x01, 0x01, 0x01})
	exportBody := []byte{
		0x02,
		0x01, '0', 0x00, 0x00,
		0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x00,
	}
	out = appendCoreSection(out, 0x07, exportBody)
	funcBody := []byte{0x00}
	for i := range paramValtypes {
		funcBody = append(funcBody, 0x20)
		funcBody = leb128.UlebU64(funcBody, uint64(i))
	}
	funcBody = append(funcBody, 0x41, 0x00, 0x11, 0x00, 0x00, 0x0b)
	codeBody := []byte{0x01}
	codeBody = leb128.UlebU64(codeBody, uint64(len(funcBody)))
	codeBody = append(codeBody, funcBody...)
	out = appendCoreSection(out, 0x0a, codeBody)
	return out
}

func TrampolineModuleForParamsResults(paramValtypes, resultValtypes []byte) []byte {
	if len(resultValtypes) == 0 {
		return TrampolineModuleForParamsNoResult(paramValtypes)
	}
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = leb128.UlebU64(typeBody, uint64(len(resultValtypes)))
	typeBody = append(typeBody, resultValtypes...)
	out = appendCoreSection(out, 0x01, typeBody)
	out = appendCoreSection(out, 0x03, []byte{0x01, 0x00})
	out = appendCoreSection(out, 0x04, []byte{0x01, 0x70, 0x01, 0x01, 0x01})
	exportBody := []byte{
		0x02,
		0x01, '0', 0x00, 0x00,
		0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x00,
	}
	out = appendCoreSection(out, 0x07, exportBody)
	funcBody := []byte{0x00}
	for i := range paramValtypes {
		funcBody = append(funcBody, 0x20)
		funcBody = leb128.UlebU64(funcBody, uint64(i))
	}
	funcBody = append(funcBody, 0x41, 0x00, 0x11, 0x00, 0x00, 0x0b)
	codeBody := []byte{0x01}
	codeBody = leb128.UlebU64(codeBody, uint64(len(funcBody)))
	codeBody = append(codeBody, funcBody...)
	out = appendCoreSection(out, 0x0a, codeBody)
	return out
}

func FixupModuleForParamsResults(paramValtypes, resultValtypes []byte) []byte {
	if len(resultValtypes) == 0 {
		return FixupModuleForParamsNoResult(paramValtypes)
	}
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeBody := []byte{0x01, 0x60}
	typeBody = leb128.UlebU64(typeBody, uint64(len(paramValtypes)))
	typeBody = append(typeBody, paramValtypes...)
	typeBody = leb128.UlebU64(typeBody, uint64(len(resultValtypes)))
	typeBody = append(typeBody, resultValtypes...)
	out = appendCoreSection(out, 0x01, typeBody)
	importBody := []byte{
		0x02,
		0x00, 0x01, '0', 0x00, 0x00,
		0x00, 0x08, '$', 'i', 'm', 'p', 'o', 'r', 't', 's', 0x01, 0x70, 0x01, 0x01, 0x01,
	}
	out = appendCoreSection(out, 0x02, importBody)
	elemBody := []byte{0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x00}
	out = appendCoreSection(out, 0x09, elemBody)
	return out
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func PutTypeSectionOneInstanceOneFuncNoResultExport(buf []byte, exportName string, paramNames []string, paramValtypes []byte) []byte {
	return PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport(buf, nil, exportName, paramNames, paramValtypes)
}

func PutTypeSectionRawBody(buf []byte, body []byte) []byte {
	return wrapSection(buf, SectionType, body)
}

func PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport(buf []byte, innerTypes [][]byte, exportName string, paramNames []string, paramValtypes []byte) []byte {
	return PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf, innerTypes, exportName, paramNames, paramValtypes, nil)
}

func PutTypeSectionInstanceWithInnerTypesAndOneFuncExport(buf []byte, innerTypes [][]byte, exportName string, paramNames []string, paramValtypes []byte, resultValtypes []byte) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	if len(resultValtypes) > 1 {
		panic("component: multi-result functions not yet supported")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x42)
	body = leb128.UlebU64(body, uint64(2+len(innerTypes)))

	for _, it := range innerTypes {
		body = append(body, 0x01)
		body = append(body, it...)
	}

	body = append(body, 0x01)
	body = append(body, 0x40)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	if len(resultValtypes) == 0 {
		body = append(body, 0x01)
		body = leb128.UlebU64(body, 0)
	} else {
		body = append(body, 0x00)
		body = append(body, resultValtypes[0])
	}

	body = append(body, 0x04)
	body = append(body, 0x00)
	body = putName(body, exportName)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, uint64(len(innerTypes)))

	return wrapSection(buf, SectionType, body)
}

var InnerTypeResultEmpty = []byte{0x6a, 0x00, 0x00}

func InnerTypeBorrow(resourceTypeidx uint32) []byte {
	out := []byte{0x68}
	out = leb128.UlebU64(out, uint64(resourceTypeidx))
	return out
}

func InnerTypeOwn(resourceTypeidx uint32) []byte {
	out := []byte{0x69}
	out = leb128.UlebU64(out, uint64(resourceTypeidx))
	return out
}

var InnerTypeListU8 = []byte{0x70, CValtypeU8}

func InnerTypeList(elemValtype byte) []byte {
	return []byte{0x70, elemValtype}
}

func InnerTypeFuture(elemValtype byte) []byte {
	return []byte{0x65, 0x01, elemValtype}
}

func InnerTypeStream(elemValtype byte) []byte {
	return []byte{0x66, 0x01, elemValtype}
}

func InnerTypeResultErr(errTypeidx uint32) []byte {
	out := []byte{0x6a, 0x00, 0x01}
	out = leb128.UlebU64(out, uint64(errTypeidx))
	return out
}

func InnerTypeEnum(names []string) []byte {
	out := []byte{0x6d}
	out = leb128.UlebU64(out, uint64(len(names)))
	for _, n := range names {
		out = putName(out, n)
	}
	return out
}

func InnerTypeFlags(names []string) []byte {
	out := []byte{0x6e}
	out = leb128.UlebU64(out, uint64(len(names)))
	for _, n := range names {
		out = putName(out, n)
	}
	return out
}

func InnerTypeOption(innerValtype byte) []byte {
	return []byte{0x6b, innerValtype}
}

var WasiFilesystemErrorCodeNames = []string{
	"access", "would-block", "already", "bad-descriptor", "busy",
	"deadlock", "quota", "exist", "file-too-large", "illegal-byte-sequence",
	"in-progress", "interrupted", "invalid", "io", "is-directory", "loop",
	"too-many-links", "message-size", "name-too-long", "no-device",
	"no-entry", "no-lock", "insufficient-memory", "insufficient-space",
	"not-directory", "not-empty", "not-recoverable", "unsupported",
	"no-tty", "no-such-device", "overflow", "not-permitted", "pipe",
	"read-only", "invalid-seek", "text-file-busy", "cross-device",
}

func InnerTypeResultOk(okTypeidx uint32) []byte {
	out := []byte{0x6a, 0x01}
	out = leb128.UlebU64(out, uint64(okTypeidx))
	return append(out, 0x00)
}

func InnerTypeResultOkErr(okTypeidx, errTypeidx uint32) []byte {
	out := []byte{0x6a, 0x01}
	out = leb128.UlebU64(out, uint64(okTypeidx))
	out = append(out, 0x01)
	out = leb128.UlebU64(out, uint64(errTypeidx))
	return out
}

type VariantCase struct {
	Name           string
	HasPayload     bool
	PayloadValtype byte
}

func InnerTypeVariant(cases []VariantCase) []byte {
	out := []byte{0x71}
	out = leb128.UlebU64(out, uint64(len(cases)))
	for _, c := range cases {
		out = leb128.UlebU64(out, uint64(len(c.Name)))
		out = append(out, c.Name...)
		if c.HasPayload {
			out = append(out, 0x01, c.PayloadValtype)
		} else {
			out = append(out, 0x00)
		}
		out = append(out, 0x00)
	}
	return out
}

func InnerTypeTuple(elemValtypes []byte) []byte {
	out := []byte{0x6f}
	out = leb128.UlebU64(out, uint64(len(elemValtypes)))
	out = append(out, elemValtypes...)
	return out
}

type RecordField struct {
	Name    string
	Valtype byte
}

func InnerTypeRecord(fields []RecordField) []byte {
	out := []byte{0x72}
	out = leb128.UlebU64(out, uint64(len(fields)))
	for _, f := range fields {
		out = leb128.UlebU64(out, uint64(len(f.Name)))
		out = append(out, f.Name...)
		out = append(out, f.Valtype)
	}
	return out
}

func OuterAliasTypeDecl(scopesUp uint32, outerTypeidx uint32) []byte {
	out := []byte{0x02, 0x03, 0x02}
	out = leb128.UlebU64(out, uint64(scopesUp))
	out = leb128.UlebU64(out, uint64(outerTypeidx))
	return out
}

func ExportSubResourceDecl(name string) []byte {
	out := []byte{0x04, 0x00}
	out = leb128.UlebU64(out, uint64(len(name)))
	out = append(out, name...)
	out = append(out, 0x03, 0x01)
	return out
}

func ExportTypeEqDecl(name string, typeidx uint32) []byte {
	out := []byte{0x04, 0x00}
	out = leb128.UlebU64(out, uint64(len(name)))
	out = append(out, name...)
	out = append(out, 0x03, 0x00)
	out = leb128.UlebU64(out, uint64(typeidx))
	return out
}

func WasiIoErrorInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x01}
	body = append(body, ExportSubResourceDecl("error")...)
	return body
}

var httpMethodCases = []VariantCase{
	{Name: "get"}, {Name: "head"}, {Name: "post"}, {Name: "put"},
	{Name: "delete"}, {Name: "connect"}, {Name: "options"},
	{Name: "trace"}, {Name: "patch"},
	{Name: "other", HasPayload: true, PayloadValtype: CValtypeString},
}

var httpSchemeCases = []VariantCase{
	{Name: "HTTP"}, {Name: "HTTPS"},
	{Name: "other", HasPayload: true, PayloadValtype: CValtypeString},
}

var httpHeaderErrorCases = []VariantCase{
	{Name: "invalid-syntax"}, {Name: "forbidden"}, {Name: "immutable"},
}

type httpValueTypeIdx struct {
	method      uint32
	scheme      uint32
	headerError uint32
	errorCode   uint32
	optString   uint32
}

func httpErrorCodeCases(dnsPayload, tlsPayload, optU64, optU32, optFieldSize, fieldSize, optString uint32) []VariantCase {
	p := func(idx uint32) (bool, byte) { return true, byte(idx) }
	mk := func(name string, has bool, vt byte) VariantCase {
		return VariantCase{Name: name, HasPayload: has, PayloadValtype: vt}
	}
	dnsH, dnsV := p(dnsPayload)
	tlsH, tlsV := p(tlsPayload)
	u64H, u64V := p(optU64)
	u32H, u32V := p(optU32)
	ofsH, ofsV := p(optFieldSize)
	fsH, fsV := p(fieldSize)
	strH, strV := p(optString)
	return []VariantCase{
		mk("DNS-timeout", false, 0),
		mk("DNS-error", dnsH, dnsV),
		mk("destination-not-found", false, 0),
		mk("destination-unavailable", false, 0),
		mk("destination-IP-prohibited", false, 0),
		mk("destination-IP-unroutable", false, 0),
		mk("connection-refused", false, 0),
		mk("connection-terminated", false, 0),
		mk("connection-timeout", false, 0),
		mk("connection-read-timeout", false, 0),
		mk("connection-write-timeout", false, 0),
		mk("connection-limit-reached", false, 0),
		mk("TLS-protocol-error", false, 0),
		mk("TLS-certificate-error", false, 0),
		mk("TLS-alert-received", tlsH, tlsV),
		mk("HTTP-request-denied", false, 0),
		mk("HTTP-request-length-required", false, 0),
		mk("HTTP-request-body-size", u64H, u64V),
		mk("HTTP-request-method-invalid", false, 0),
		mk("HTTP-request-URI-invalid", false, 0),
		mk("HTTP-request-URI-too-long", false, 0),
		mk("HTTP-request-header-section-size", u32H, u32V),
		mk("HTTP-request-header-size", ofsH, ofsV),
		mk("HTTP-request-trailer-section-size", u32H, u32V),
		mk("HTTP-request-trailer-size", fsH, fsV),
		mk("HTTP-response-incomplete", false, 0),
		mk("HTTP-response-header-section-size", u32H, u32V),
		mk("HTTP-response-header-size", fsH, fsV),
		mk("HTTP-response-body-size", u64H, u64V),
		mk("HTTP-response-trailer-section-size", u32H, u32V),
		mk("HTTP-response-trailer-size", fsH, fsV),
		mk("HTTP-response-transfer-coding", strH, strV),
		mk("HTTP-response-content-coding", strH, strV),
		mk("HTTP-response-timeout", false, 0),
		mk("HTTP-upgrade-failed", false, 0),
		mk("HTTP-protocol-error", false, 0),
		mk("loop-detected", false, 0),
		mk("configuration-error", false, 0),
		mk("internal-error", strH, strV),
	}
}

func httpValueTypeDecls(start uint32) ([]byte, httpValueTypeIdx, uint32) {
	var decls []byte
	idx := start
	def := func(b []byte) uint32 {
		decls = append(decls, 0x01)
		decls = append(decls, b...)
		i := idx
		idx++
		return i
	}
	export := func(name string, typeidx uint32) uint32 {
		decls = append(decls, ExportTypeEqDecl(name, typeidx)...)
		i := idx
		idx++
		return i
	}

	optString := def(InnerTypeOption(CValtypeString))
	optU16 := def(InnerTypeOption(CValtypeU16))
	optU8 := def(InnerTypeOption(CValtypeU8))
	optU32 := def(InnerTypeOption(CValtypeU32))
	optU64 := def(InnerTypeOption(CValtypeU64))

	dnsDef := def(InnerTypeRecord([]RecordField{
		{Name: "rcode", Valtype: byte(optString)},
		{Name: "info-code", Valtype: byte(optU16)},
	}))
	dnsPayload := export("DNS-error-payload", dnsDef)
	tlsDef := def(InnerTypeRecord([]RecordField{
		{Name: "alert-id", Valtype: byte(optU8)},
		{Name: "alert-message", Valtype: byte(optString)},
	}))
	tlsPayload := export("TLS-alert-received-payload", tlsDef)
	fsDef := def(InnerTypeRecord([]RecordField{
		{Name: "field-name", Valtype: byte(optString)},
		{Name: "field-size", Valtype: byte(optU32)},
	}))
	fieldSize := export("field-size-payload", fsDef)
	optFieldSize := def(InnerTypeOption(byte(fieldSize)))

	methodDef := def(InnerTypeVariant(httpMethodCases))
	method := export("method", methodDef)
	schemeDef := def(InnerTypeVariant(httpSchemeCases))
	scheme := export("scheme", schemeDef)
	heDef := def(InnerTypeVariant(httpHeaderErrorCases))
	headerError := export("header-error", heDef)
	ecDef := def(InnerTypeVariant(httpErrorCodeCases(
		dnsPayload, tlsPayload, optU64, optU32, optFieldSize, fieldSize, optString)))
	errorCode := export("error-code", ecDef)

	return decls, httpValueTypeIdx{
		method:      method,
		scheme:      scheme,
		headerError: headerError,
		errorCode:   errorCode,
		optString:   optString,
	}, idx
}

func WasiHttpValueTypesInstanceTypeBody() []byte {
	decls, _, next := httpValueTypeDecls(0)
	body := []byte{0x01, 0x42}
	body = leb128.UlebU64(body, uint64(next))
	return append(body, decls...)
}

func WasiHttpTypesInstanceTypeBody(inputStreamT, outputStreamT uint32) []byte {
	var decls []byte
	idx := uint32(0)
	declCount := uint32(0)

	def := func(b []byte) uint32 {
		decls = append(decls, 0x01)
		decls = append(decls, b...)
		declCount++
		i := idx
		idx++
		return i
	}
	aliasOuter := func(outerIdx uint32) uint32 {
		decls = append(decls, OuterAliasTypeDecl(1, outerIdx)...)
		declCount++
		i := idx
		idx++
		return i
	}
	subResource := func(name string) uint32 {
		decls = append(decls, ExportSubResourceDecl(name)...)
		declCount++
		i := idx
		idx++
		return i
	}
	funcDef := func(paramNames []string, paramVT []byte, hasResult bool, resultIdx byte) uint32 {
		decls = append(decls, 0x01, 0x40)
		decls = leb128.UlebU64(decls, uint64(len(paramNames)))
		for i, n := range paramNames {
			decls = leb128.UlebU64(decls, uint64(len(n)))
			decls = append(decls, n...)
			decls = append(decls, paramVT[i])
		}
		if hasResult {
			decls = append(decls, 0x00, resultIdx)
		} else {
			decls = append(decls, 0x01, 0x00)
		}
		declCount++
		i := idx
		idx++
		return i
	}
	exportFunc := func(name string, funcIdx uint32) {
		decls = append(decls, 0x04, 0x00)
		decls = leb128.UlebU64(decls, uint64(len(name)))
		decls = append(decls, name...)
		decls = append(decls, 0x01)
		decls = leb128.UlebU64(decls, uint64(funcIdx))
		declCount++
	}

	inStream := aliasOuter(inputStreamT)
	outStream := aliasOuter(outputStreamT)

	vtDecls, vt, next := httpValueTypeDecls(idx)
	decls = append(decls, vtDecls...)
	declCount += next - idx
	idx = next

	fields := subResource("fields")
	incomingRequest := subResource("incoming-request")
	incomingBody := subResource("incoming-body")
	futureTrailers := subResource("future-trailers")
	outgoingResponse := subResource("outgoing-response")
	outgoingBody := subResource("outgoing-body")
	responseOutparam := subResource("response-outparam")

	bIncReq := def(InnerTypeBorrow(incomingRequest))
	bFields := def(InnerTypeBorrow(fields))
	bIncBody := def(InnerTypeBorrow(incomingBody))
	bOutResp := def(InnerTypeBorrow(outgoingResponse))
	bOutBody := def(InnerTypeBorrow(outgoingBody))

	oFields := def([]byte{0x69, byte(fields)})
	oIncBody := def([]byte{0x69, byte(incomingBody)})
	oFutTrail := def([]byte{0x69, byte(futureTrailers)})
	oInStream := def([]byte{0x69, byte(inStream)})
	oOutStream := def([]byte{0x69, byte(outStream)})
	oOutResp := def([]byte{0x69, byte(outgoingResponse)})
	oOutBody := def([]byte{0x69, byte(outgoingBody)})
	oOutparam := def([]byte{0x69, byte(responseOutparam)})

	rIncBody := def(InnerTypeResultOk(oIncBody))
	rInStream := def(InnerTypeResultOk(oInStream))
	rOutBody := def(InnerTypeResultOk(oOutBody))
	rOutStream := def(InnerTypeResultOk(oOutStream))
	rHeaderErr := def(InnerTypeResultErr(vt.headerError))
	rEmpty := def(InnerTypeResultEmpty)
	rErrCode := def(InnerTypeResultErr(vt.errorCode))
	rOutRespErr := def(InnerTypeResultOkErr(oOutResp, vt.errorCode))

	fieldValue := def(InnerTypeListU8)
	entryTuple := def(InnerTypeTuple([]byte{CValtypeString, byte(fieldValue)}))
	entriesList := def(InnerTypeList(byte(entryTuple)))

	optTrailers := def(InnerTypeOption(byte(oFields)))

	f := funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(vt.method))
	exportFunc("[method]incoming-request.method", f)
	f = funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(vt.optString))
	exportFunc("[method]incoming-request.path-with-query", f)
	f = funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(oFields))
	exportFunc("[method]incoming-request.headers", f)
	f = funcDef([]string{"self"}, []byte{byte(bIncReq)}, true, byte(rIncBody))
	exportFunc("[method]incoming-request.consume", f)

	f = funcDef([]string{"self"}, []byte{byte(bIncBody)}, true, byte(rInStream))
	exportFunc("[method]incoming-body.stream", f)
	f = funcDef([]string{"this"}, []byte{byte(oIncBody)}, true, byte(oFutTrail))
	exportFunc("[static]incoming-body.finish", f)

	f = funcDef(nil, nil, true, byte(oFields))
	exportFunc("[constructor]fields", f)
	f = funcDef([]string{"self"}, []byte{byte(bFields)}, true, byte(entriesList))
	exportFunc("[method]fields.entries", f)
	f = funcDef([]string{"self", "name", "value"},
		[]byte{byte(bFields), CValtypeString, byte(fieldValue)}, true, byte(rHeaderErr))
	exportFunc("[method]fields.append", f)

	f = funcDef([]string{"headers"}, []byte{byte(oFields)}, true, byte(oOutResp))
	exportFunc("[constructor]outgoing-response", f)
	f = funcDef([]string{"self", "status-code"}, []byte{byte(bOutResp), CValtypeU16}, true, byte(rEmpty))
	exportFunc("[method]outgoing-response.set-status-code", f)
	f = funcDef([]string{"self"}, []byte{byte(bOutResp)}, true, byte(rOutBody))
	exportFunc("[method]outgoing-response.body", f)

	f = funcDef([]string{"self"}, []byte{byte(bOutBody)}, true, byte(rOutStream))
	exportFunc("[method]outgoing-body.write", f)
	f = funcDef([]string{"this", "trailers"}, []byte{byte(oOutBody), byte(optTrailers)}, true, byte(rErrCode))
	exportFunc("[static]outgoing-body.finish", f)

	f = funcDef([]string{"param", "response"}, []byte{byte(oOutparam), byte(rOutRespErr)}, false, 0)
	exportFunc("[static]response-outparam.set", f)

	body := []byte{0x01, 0x42}
	body = leb128.UlebU64(body, uint64(declCount))
	return append(body, decls...)
}

func WasiIoPollInstanceTypeBody(withPoll bool) []byte {
	declCount := byte(4)
	if withPoll {
		declCount = 8
	}
	body := []byte{0x01, 0x42, declCount}
	body = append(body, ExportSubResourceDecl("pollable")...)
	body = append(body, 0x01, 0x68, 0x00)
	body = append(body,
		0x01, 0x40, 0x01,
		0x04, 's', 'e', 'l', 'f', 0x01,
		0x01, 0x00,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]pollable.block")))
	body = append(body, "[method]pollable.block"...)
	body = append(body, 0x01, 0x02)
	if withPoll {
		body = append(body, 0x01, 0x70, 0x01)
		body = append(body, 0x01, 0x70, CValtypeU32)
		body = append(body, tcpMethodFuncDecl("poll", []string{"in"}, []byte{0x03}, 0x04)...)
		body = append(body, 0x04, 0x00, byte(len("poll")))
		body = append(body, "poll"...)
		body = append(body, 0x01, 0x05)
	}
	return body
}

func WasiClocksMonotonicTimerInstanceTypeBody(pollableT uint32) []byte {
	body := []byte{0x01, 0x42, 0x04}
	body = append(body, OuterAliasTypeDecl(1, pollableT)...)
	body = append(body, 0x01, 0x69, 0x00)
	body = append(body, tcpMethodFuncDecl("subscribe-duration",
		[]string{"when"}, []byte{CValtypeU64}, 0x01)...)
	const name = "subscribe-duration"
	body = append(body, 0x04, 0x00, byte(len(name)))
	body = append(body, name...)
	body = append(body, 0x01, 0x02)
	return body
}

func WasiClocksMonotonicTimerAndNowInstanceTypeBody(pollableT uint32) []byte {
	body := []byte{0x01, 0x42, 0x06}
	body = append(body, OuterAliasTypeDecl(1, pollableT)...)
	body = append(body, 0x01, 0x69, 0x00)
	body = append(body, tcpMethodFuncDecl("subscribe-duration",
		[]string{"when"}, []byte{CValtypeU64}, 0x01)...)
	const sd = "subscribe-duration"
	body = append(body, 0x04, 0x00, byte(len(sd)))
	body = append(body, sd...)
	body = append(body, 0x01, 0x02)
	body = append(body, 0x01, 0x40, 0x00, 0x00, CValtypeU64)
	body = append(body, 0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x03)
	return body
}

var WasiSocketsNetworkErrorCodeNames = []string{
	"unknown", "access-denied", "not-supported", "invalid-argument",
	"out-of-memory", "timeout", "concurrency-conflict", "not-in-progress",
	"would-block", "invalid-state", "new-socket-limit",
	"address-not-bindable", "address-in-use", "remote-unreachable",
	"connection-refused", "connection-reset", "connection-aborted",
	"datagram-too-large", "name-unresolvable", "temporary-resolver-failure",
	"permanent-resolver-failure",
}

func WasiSocketsNetworkInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x0f}
	body = append(body, ExportSubResourceDecl("network")...)
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiSocketsNetworkErrorCodeNames)...)
	body = append(body, ExportTypeEqDecl("error-code", 1)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum([]string{"ipv4", "ipv6"})...)
	body = append(body, ExportTypeEqDecl("ip-address-family", 3)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeTuple([]byte{CValtypeU8, CValtypeU8, CValtypeU8, CValtypeU8})...)
	body = append(body, ExportTypeEqDecl("ipv4-address", 5)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeRecord([]RecordField{
		{Name: "port", Valtype: CValtypeU16},
		{Name: "address", Valtype: 0x06},
	})...)
	body = append(body, ExportTypeEqDecl("ipv4-socket-address", 7)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeTuple([]byte{
		CValtypeU16, CValtypeU16, CValtypeU16, CValtypeU16,
		CValtypeU16, CValtypeU16, CValtypeU16, CValtypeU16,
	})...)
	body = append(body, ExportTypeEqDecl("ipv6-address", 9)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeRecord([]RecordField{
		{Name: "port", Valtype: CValtypeU16},
		{Name: "flow-info", Valtype: CValtypeU32},
		{Name: "address", Valtype: 0x0a},
		{Name: "scope-id", Valtype: CValtypeU32},
	})...)
	body = append(body, ExportTypeEqDecl("ipv6-socket-address", 11)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "ipv4", HasPayload: true, PayloadValtype: 0x08},
		{Name: "ipv6", HasPayload: true, PayloadValtype: 0x0c},
	})...)
	body = append(body, ExportTypeEqDecl("ip-socket-address", 13)...)
	return body
}

const wasiConfigStore = "wasi:config/store@0.2.0-rc.1"

func WasiConfigStoreInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x06}
	body = append(body, 0x01)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "upstream", HasPayload: true, PayloadValtype: CValtypeString},
		{Name: "io", HasPayload: true, PayloadValtype: CValtypeString},
	})...)
	body = append(body, ExportTypeEqDecl("error", 0)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeOption(CValtypeString)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(2, 1)...)
	body = append(body, tcpMethodFuncDecl("get", []string{"key"}, []byte{CValtypeString}, 3)...)
	body = append(body, 0x04, 0x00, byte(len("get")))
	body = append(body, "get"...)
	body = append(body, 0x01, 0x04)
	return body
}

func WasiSocketsInstanceNetworkInstanceTypeBody(outerNetworkTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x04}
	body = append(body, OuterAliasTypeDecl(1, outerNetworkTypeidx)...)
	body = append(body, 0x01, 0x69, 0x00)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x01)
	body = append(body, 0x04, 0x00, byte(len("instance-network")))
	body = append(body, "instance-network"...)
	body = append(body, 0x01, 0x02)
	return body
}

func tcpMethodFuncDecl(method string, paramNames []string, paramValtypes []byte, resultTypeidx byte) []byte {
	out := []byte{0x01, 0x40}
	out = leb128.UlebU64(out, uint64(len(paramNames)))
	for i, n := range paramNames {
		out = leb128.UlebU64(out, uint64(len(n)))
		out = append(out, n...)
		out = append(out, paramValtypes[i])
	}
	out = append(out, 0x00, resultTypeidx)
	return out
}

func WasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT uint32) []byte {
	return wasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT, false)
}

func WasiSocketsTcpConnectInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT uint32) []byte {
	return wasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT, true)
}

func wasiSocketsTcpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, inputStreamT, outputStreamT, pollableT uint32, withConnect bool) []byte {
	exportMethod := func(body []byte, name string, funcTypeidx byte) []byte {
		body = append(body, 0x04, 0x00, byte(len(name)))
		body = append(body, name...)
		return append(body, 0x01, funcTypeidx)
	}
	declCount := byte(0x29)
	if withConnect {
		declCount = 0x2f
	}
	body := []byte{0x01, 0x42, declCount}
	body = append(body, OuterAliasTypeDecl(1, networkT)...)
	body = append(body, OuterAliasTypeDecl(1, errorCodeT)...)
	body = append(body, OuterAliasTypeDecl(1, ipSockAddrT)...)
	body = append(body, OuterAliasTypeDecl(1, inputStreamT)...)
	body = append(body, OuterAliasTypeDecl(1, outputStreamT)...)
	body = append(body, OuterAliasTypeDecl(1, pollableT)...)
	body = append(body, ExportSubResourceDecl("tcp-socket")...)
	body = append(body, 0x01, 0x68, 0x06)
	body = append(body, 0x01, 0x68, 0x00)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultErr(1)...)
	body = append(body, 0x01, 0x69, 0x06)
	body = append(body, 0x01, 0x69, 0x03)
	body = append(body, 0x01, 0x69, 0x04)
	body = append(body, 0x01, 0x69, 0x05)
	body = append(body, 0x01)
	body = append(body, InnerTypeTuple([]byte{0x0a, 0x0b, 0x0c})...)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(14, 1)...)
	body = append(body, tcpMethodFuncDecl("start-bind",
		[]string{"self", "network", "local-address"}, []byte{0x07, 0x08, 0x02}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.start-bind", 0x10)
	body = append(body, tcpMethodFuncDecl("finish-bind", []string{"self"}, []byte{0x07}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.finish-bind", 0x11)
	body = append(body, tcpMethodFuncDecl("start-listen", []string{"self"}, []byte{0x07}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.start-listen", 0x12)
	body = append(body, tcpMethodFuncDecl("finish-listen", []string{"self"}, []byte{0x07}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.finish-listen", 0x13)
	body = append(body, tcpMethodFuncDecl("accept", []string{"self"}, []byte{0x07}, 0x0f)...)
	body = exportMethod(body, "[method]tcp-socket.accept", 0x14)
	body = append(body, tcpMethodFuncDecl("subscribe", []string{"self"}, []byte{0x07}, 0x0d)...)
	body = exportMethod(body, "[method]tcp-socket.subscribe", 0x15)
	if withConnect {
		body = append(body, 0x01)
		body = append(body, InnerTypeTuple([]byte{0x0b, 0x0c})...)
		body = append(body, 0x01)
		body = append(body, InnerTypeResultOkErr(22, 1)...)
		body = append(body, tcpMethodFuncDecl("start-connect",
			[]string{"self", "network", "remote-address"}, []byte{0x07, 0x08, 0x02}, 0x09)...)
		body = exportMethod(body, "[method]tcp-socket.start-connect", 0x18)
		body = append(body, tcpMethodFuncDecl("finish-connect", []string{"self"}, []byte{0x07}, 0x17)...)
		body = exportMethod(body, "[method]tcp-socket.finish-connect", 0x19)
	}
	localAddr := byte(0x16)
	if withConnect {
		localAddr = 0x1a
	}
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(2, 1)...)
	body = append(body, tcpMethodFuncDecl("local-address", []string{"self"}, []byte{0x07}, localAddr)...)
	body = exportMethod(body, "[method]tcp-socket.local-address", localAddr+1)
	body = append(body, tcpMethodFuncDecl("remote-address", []string{"self"}, []byte{0x07}, localAddr)...)
	body = exportMethod(body, "[method]tcp-socket.remote-address", localAddr+2)
	ctl := localAddr + 3
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum([]string{"receive", "send", "both"})...)
	body = append(body, ExportTypeEqDecl("shutdown-type", uint32(ctl))...)
	body = append(body, tcpMethodFuncDecl("set-listen-backlog-size", []string{"self", "value"}, []byte{0x07, CValtypeU64}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.set-listen-backlog-size", ctl+2)
	body = append(body, tcpMethodFuncDecl("set-keep-alive-enabled", []string{"self", "value"}, []byte{0x07, CValtypeBool}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.set-keep-alive-enabled", ctl+3)
	body = append(body, tcpMethodFuncDecl("shutdown", []string{"self", "shutdown-type"}, []byte{0x07, ctl + 1}, 0x09)...)
	body = exportMethod(body, "[method]tcp-socket.shutdown", ctl+4)
	return body
}

func WasiSocketsTcpCreateSocketInstanceTypeBody(ipAddrFamilyT, errorCodeT, tcpSocketT uint32) []byte {
	body := []byte{0x01, 0x42, 0x07}
	body = append(body, OuterAliasTypeDecl(1, ipAddrFamilyT)...)
	body = append(body, OuterAliasTypeDecl(1, errorCodeT)...)
	body = append(body, OuterAliasTypeDecl(1, tcpSocketT)...)
	body = append(body, 0x01, 0x69, 0x02)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(3, 1)...)
	body = append(body, tcpMethodFuncDecl("create-tcp-socket", []string{"address-family"}, []byte{0x00}, 0x04)...)
	body = append(body, 0x04, 0x00, byte(len("create-tcp-socket")))
	body = append(body, "create-tcp-socket"...)
	body = append(body, 0x01, 0x05)
	return body
}

func WasiSocketsUdpInstanceTypeBody(networkT, errorCodeT, ipSockAddrT, pollableT uint32) []byte {
	var decls []byte
	idx := uint32(0)
	declCount := uint32(0)
	alias := func(outer uint32) uint32 {
		decls = append(decls, OuterAliasTypeDecl(1, outer)...)
		declCount++
		i := idx
		idx++
		return i
	}
	sub := func(name string) uint32 {
		decls = append(decls, ExportSubResourceDecl(name)...)
		declCount++
		i := idx
		idx++
		return i
	}
	def := func(b []byte) uint32 {
		decls = append(decls, 0x01)
		decls = append(decls, b...)
		declCount++
		i := idx
		idx++
		return i
	}
	exportType := func(name string, typeidx uint32) uint32 {
		decls = append(decls, ExportTypeEqDecl(name, typeidx)...)
		declCount++
		i := idx
		idx++
		return i
	}
	method := func(name string, paramNames []string, paramVT []byte, resultIdx byte) {
		decls = append(decls, tcpMethodFuncDecl(name, paramNames, paramVT, resultIdx)...)
		declCount++
		idx++
		decls = append(decls, 0x04, 0x00, byte(len(name)))
		decls = append(decls, name...)
		decls = append(decls, 0x01, byte(idx-1))
		declCount++
	}

	netT := alias(networkT)
	errT := alias(errorCodeT)
	sockAddrT := exportType("ip-socket-address", alias(ipSockAddrT))
	pollT := alias(pollableT)

	udpSock := sub("udp-socket")
	inStream := sub("incoming-datagram-stream")
	outStream := sub("outgoing-datagram-stream")

	listU8 := def(InnerTypeListU8)
	optAddr := def(InnerTypeOption(byte(sockAddrT)))
	outDatagram := exportType("outgoing-datagram", def(InnerTypeRecord([]RecordField{
		{Name: "data", Valtype: byte(listU8)},
		{Name: "remote-address", Valtype: byte(optAddr)},
	})))
	inDatagram := exportType("incoming-datagram", def(InnerTypeRecord([]RecordField{
		{Name: "data", Valtype: byte(listU8)},
		{Name: "remote-address", Valtype: byte(sockAddrT)},
	})))
	bUdp := def(InnerTypeBorrow(udpSock))
	bNet := def(InnerTypeBorrow(netT))
	bOut := def(InnerTypeBorrow(outStream))
	bIn := def(InnerTypeBorrow(inStream))
	ownPoll := def([]byte{0x69, byte(pollT)})
	resEmptyErr := def(InnerTypeResultErr(errT))
	ownIn := def([]byte{0x69, byte(inStream)})
	ownOut := def([]byte{0x69, byte(outStream)})
	tupStreams := def(InnerTypeTuple([]byte{byte(ownIn), byte(ownOut)}))
	resStream := def(InnerTypeResultOkErr(tupStreams, errT))
	resU64 := def(InnerTypeResultOkErr(CValtypeU64, errT))
	listDatagram := def(InnerTypeList(byte(outDatagram)))
	listIn := def(InnerTypeList(byte(inDatagram)))
	resListIn := def(InnerTypeResultOkErr(listIn, errT))
	resAddr := def(InnerTypeResultOkErr(sockAddrT, errT))

	method("[method]udp-socket.start-bind",
		[]string{"self", "network", "local-address"}, []byte{byte(bUdp), byte(bNet), byte(sockAddrT)}, byte(resEmptyErr))
	method("[method]udp-socket.finish-bind", []string{"self"}, []byte{byte(bUdp)}, byte(resEmptyErr))
	method("[method]udp-socket.stream", []string{"self", "remote-address"}, []byte{byte(bUdp), byte(optAddr)}, byte(resStream))
	method("[method]outgoing-datagram-stream.check-send", []string{"self"}, []byte{byte(bOut)}, byte(resU64))
	method("[method]outgoing-datagram-stream.send", []string{"self", "datagrams"}, []byte{byte(bOut), byte(listDatagram)}, byte(resU64))
	method("[method]outgoing-datagram-stream.subscribe", []string{"self"}, []byte{byte(bOut)}, byte(ownPoll))
	method("[method]udp-socket.local-address", []string{"self"}, []byte{byte(bUdp)}, byte(resAddr))
	method("[method]udp-socket.remote-address", []string{"self"}, []byte{byte(bUdp)}, byte(resAddr))
	method("[method]incoming-datagram-stream.receive", []string{"self", "max-results"}, []byte{byte(bIn), CValtypeU64}, byte(resListIn))
	method("[method]incoming-datagram-stream.subscribe", []string{"self"}, []byte{byte(bIn)}, byte(ownPoll))

	body := []byte{0x01, 0x42}
	body = leb128.UlebU64(body, uint64(declCount))
	return append(body, decls...)
}

func WasiSocketsUdpCreateSocketInstanceTypeBody(ipAddrFamilyT, errorCodeT, udpSocketT uint32) []byte {
	body := []byte{0x01, 0x42, 0x07}
	body = append(body, OuterAliasTypeDecl(1, ipAddrFamilyT)...)
	body = append(body, OuterAliasTypeDecl(1, errorCodeT)...)
	body = append(body, OuterAliasTypeDecl(1, udpSocketT)...)
	body = append(body, 0x01, 0x69, 0x02)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(3, 1)...)
	body = append(body, tcpMethodFuncDecl("create-udp-socket", []string{"address-family"}, []byte{0x00}, 0x04)...)
	body = append(body, 0x04, 0x00, byte(len("create-udp-socket")))
	body = append(body, "create-udp-socket"...)
	body = append(body, 0x01, 0x05)
	return body
}

func WasiFilesystemTypesDescriptorInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x01}
	body = append(body, ExportSubResourceDecl("descriptor")...)
	return body
}

func WasiFilesystemTypesReadViaStreamInstanceTypeBody(outerInputStreamTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0a}
	body = append(body, ExportSubResourceDecl("descriptor")...)
	body = append(body, OuterAliasTypeDecl(1, outerInputStreamTypeidx)...)
	body = append(body, ExportTypeEqDecl("input-stream", 1)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiFilesystemErrorCodeNames)...)
	body = append(body, ExportTypeEqDecl("error-code", 3)...)
	body = append(body, 0x01, 0x69, 0x02)
	body = append(body, 0x01, 0x68, 0x00)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(5, 4)...)
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x06, 'o', 'f', 'f', 's', 'e', 't', CValtypeU64,
		0x00, 0x07,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]descriptor.read-via-stream")))
	body = append(body, "[method]descriptor.read-via-stream"...)
	body = append(body, 0x01, 0x08)
	return body
}

func WasiFilesystemTypesWriteViaStreamInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0a}
	body = append(body, ExportSubResourceDecl("descriptor")...)
	body = append(body, OuterAliasTypeDecl(1, outerOutputStreamTypeidx)...)
	body = append(body, ExportTypeEqDecl("output-stream", 1)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiFilesystemErrorCodeNames)...)
	body = append(body, ExportTypeEqDecl("error-code", 3)...)
	body = append(body, 0x01, 0x69, 0x02)
	body = append(body, 0x01, 0x68, 0x00)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(5, 4)...)
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x06, 'o', 'f', 'f', 's', 'e', 't', CValtypeU64,
		0x00, 0x07,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]descriptor.write-via-stream")))
	body = append(body, "[method]descriptor.write-via-stream"...)
	body = append(body, 0x01, 0x08)
	return body
}

func WasiFilesystemTypesOpenAtInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x0e}
	body = append(body, ExportSubResourceDecl("descriptor")...)
	body = append(body, 0x01)
	body = append(body, InnerTypeEnum(WasiFilesystemErrorCodeNames)...)
	body = append(body, ExportTypeEqDecl("error-code", 1)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeFlags([]string{"symlink-follow"})...)
	body = append(body, ExportTypeEqDecl("path-flags", 3)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeFlags([]string{"create", "directory", "exclusive", "truncate"})...)
	body = append(body, ExportTypeEqDecl("open-flags", 5)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeFlags([]string{
		"read", "write", "file-integrity-sync", "data-integrity-sync",
		"requested-write-sync", "mutate-directory",
	})...)
	body = append(body, ExportTypeEqDecl("descriptor-flags", 7)...)
	body = append(body, 0x01, 0x69, 0x00)
	body = append(body, 0x01, 0x68, 0x00)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(9, 2)...)
	body = append(body,
		0x01, 0x40, 0x05,
		0x04, 's', 'e', 'l', 'f', 0x0a,
		0x0a, 'p', 'a', 't', 'h', '-', 'f', 'l', 'a', 'g', 's', 0x04,
		0x04, 'p', 'a', 't', 'h', CValtypeString,
		0x0a, 'o', 'p', 'e', 'n', '-', 'f', 'l', 'a', 'g', 's', 0x06,
		0x05, 'f', 'l', 'a', 'g', 's', 0x08,
		0x00, 0x0b,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]descriptor.open-at")))
	body = append(body, "[method]descriptor.open-at"...)
	body = append(body, 0x01, 0x0c)
	return body
}

type FsFeatures struct {
	OpenAt   bool
	Read     bool
	Write    bool
	Append   bool
	Unlink   bool
	Mkdir    bool
	Rmdir    bool
	Link     bool
	Symlink  bool
	Readlink bool
	Rename   bool
	SetTimes bool
	SetSize  bool
	Sync     bool
	SyncData bool
	Advise   bool
	Stat     bool
	StatSelf bool
	ReadDir  bool
	DropDesc bool
}

func (f FsFeatures) Any() bool {
	return f.OpenAt || f.Read || f.Write || f.Append ||
		f.Unlink || f.Mkdir || f.Rmdir || f.Link || f.Symlink || f.Readlink ||
		f.Rename || f.SetTimes || f.SetSize || f.Sync || f.SyncData || f.Advise ||
		f.Stat || f.StatSelf || f.ReadDir || f.DropDesc
}

func WasiFilesystemTypesPathInstanceTypeBody(inT, outT uint32, f FsFeatures) []byte {
	b := &instTypeBuilder{}
	v := fsPrelude(b, fsNeeds{
		in: inT, out: outT,
		needIn:       f.Read,
		needOut:      f.Write || f.Append,
		needUnit:     f.Unlink || f.Mkdir || f.Rmdir || f.Link || f.Symlink || f.Rename || f.SetTimes || f.SetSize || f.Sync || f.SyncData || f.Advise,
		needDescType: f.Stat || f.StatSelf || f.ReadDir,
	})
	if f.OpenAt {
		fsOpenAt(b, v)
	}
	if f.Read {
		fsViaStream(b, v, "read-via-stream", v.rIn)
	}
	if f.Write {
		fsViaStream(b, v, "write-via-stream", v.rOut)
	}
	if f.Append {
		fsViaStream(b, v, "append-via-stream", v.rOut)
	}
	if f.Unlink {
		fsPathMutator(b, v, "unlink-file-at")
	}
	if f.Mkdir {
		fsPathMutator(b, v, "create-directory-at")
	}
	if f.Rmdir {
		fsPathMutator(b, v, "remove-directory-at")
	}
	if f.Link {
		fsLinkAt(b, v)
	}
	if f.Symlink {
		fsSymlinkAt(b, v)
	}
	if f.Readlink {
		fsReadlinkAt(b, v)
	}
	if f.Stat || f.StatSelf {
		fsStat(b, &v, f.Stat, f.StatSelf)
	}
	if f.ReadDir {
		fsReadDir(b, v)
	}
	if f.Rename {
		fsRenameAt(b, v)
	}
	if f.SetTimes {
		fsSetTimesAt(b, &v)
	}
	if f.SetSize {
		fsSetSize(b, v)
	}
	if f.Sync {
		fsSyncSelf(b, v, "sync")
	}
	if f.SyncData {
		fsSyncSelf(b, v, "sync-data")
	}
	if f.Advise {
		fsAdvise(b, v)
	}
	return b.body()
}

func WasiFilesystemPreopensInstanceTypeBody(outerDescriptorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x07}
	body = append(body, OuterAliasTypeDecl(1, outerDescriptorTypeidx)...)
	body = append(body, ExportTypeEqDecl("descriptor", 0)...)
	body = append(body, 0x01, 0x69, 0x01)
	body = append(body, 0x01, 0x6f, 0x02, 0x02, CValtypeString)
	body = append(body, 0x01, 0x70, 0x03)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x04)
	body = append(body, 0x04, 0x00, byte(len("get-directories")))
	body = append(body, "get-directories"...)
	body = append(body, 0x01, 0x05)
	return body
}

func WasiCliStdoutInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	return wasiCliStreamGetterInstanceTypeBody(outerOutputStreamTypeidx, "get-stdout", "output-stream")
}

func WasiCliStderrInstanceTypeBody(outerOutputStreamTypeidx uint32) []byte {
	return wasiCliStreamGetterInstanceTypeBody(outerOutputStreamTypeidx, "get-stderr", "output-stream")
}

func WasiClocksWallClockInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x04}
	body = append(body, 0x01)
	body = append(body, 0x72, 0x02)
	body = append(body, 0x07, 's', 'e', 'c', 'o', 'n', 'd', 's', CValtypeU64)
	body = append(body, 0x0b, 'n', 'a', 'n', 'o', 's', 'e', 'c', 'o', 'n', 'd', 's', CValtypeU32)
	body = append(body, ExportTypeEqDecl("datetime", 0)...)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x01)
	body = append(body, 0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x02)
	return body
}

func WasiCliEnvironmentArgsInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x03}
	body = append(body, 0x01, 0x70, CValtypeString)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x00)
	body = append(body, 0x04, 0x00, byte(len("get-arguments")))
	body = append(body, "get-arguments"...)
	body = append(body, 0x01, 0x01)
	return body
}

func WasiCliEnvironmentGetEnvironmentInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x04}
	body = append(body, 0x01, 0x6f, 0x02, CValtypeString, CValtypeString)
	body = append(body, 0x01, 0x70, 0x00)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x01)
	body = append(body, 0x04, 0x00, byte(len("get-environment")))
	body = append(body, "get-environment"...)
	body = append(body, 0x01, 0x02)
	return body
}

func WasiCliEnvironmentArgsAndEnvInstanceTypeBody() []byte {
	body := []byte{0x01, 0x42, 0x07}
	body = append(body, 0x01, 0x70, CValtypeString)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x00)
	body = append(body, 0x04, 0x00, byte(len("get-arguments")))
	body = append(body, "get-arguments"...)
	body = append(body, 0x01, 0x01)
	body = append(body, 0x01, 0x6f, 0x02, CValtypeString, CValtypeString)
	body = append(body, 0x01, 0x70, 0x02)
	body = append(body, 0x01, 0x40, 0x00, 0x00, 0x03)
	body = append(body, 0x04, 0x00, byte(len("get-environment")))
	body = append(body, "get-environment"...)
	body = append(body, 0x01, 0x04)
	return body
}

func WasiCliStdinInstanceTypeBody(outerInputStreamTypeidx uint32) []byte {
	return wasiCliStreamGetterInstanceTypeBody(outerInputStreamTypeidx, "get-stdin", "input-stream")
}

func wasiCliStreamGetterInstanceTypeBody(outerStreamTypeidx uint32, getFuncName, streamType string) []byte {
	body := []byte{0x01, 0x42, 0x05}
	body = append(body, OuterAliasTypeDecl(1, outerStreamTypeidx)...)
	body = append(body, ExportTypeEqDecl(streamType, 0)...)
	body = append(body, 0x01, 0x69, 0x01)
	body = append(body,
		0x01,
		0x40, 0x00,
		0x00, 0x02,
	)
	body = append(body, 0x04, 0x00)
	body = append(body, byte(len(getFuncName)))
	body = append(body, getFuncName...)
	body = append(body, 0x01, 0x03)
	return body
}

func WasiIoStreamsInstanceTypeBody(outerErrorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0b}
	body = append(body, ExportSubResourceDecl("output-stream")...)
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...)
	body = append(body, ExportTypeEqDecl("error", 1)...)
	body = append(body, 0x01, 0x69, 0x02)
	body = append(body, 0x01)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})...)
	body = append(body, ExportTypeEqDecl("stream-error", 4)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeBorrow(0)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeListU8...)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultErr(5)...)
	body = append(body,
		0x01,
		0x40,
		0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x07,
		0x00, 0x08,
	)
	body = append(body, 0x04, 0x00, 0x2e)
	body = append(body, "[method]output-stream.blocking-write-and-flush"...)
	body = append(body, 0x01, 0x09)
	return body
}

func WasiIoStreamsReadWriteInstanceTypeBody(outerErrorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x10}
	body = append(body, ExportSubResourceDecl("output-stream")...)
	body = append(body, ExportSubResourceDecl("input-stream")...)
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...)
	body = append(body, ExportTypeEqDecl("error", 2)...)
	body = append(body, 0x01, 0x69, 0x03)
	body = append(body, 0x01)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x04},
		{Name: "closed"},
	})...)
	body = append(body, ExportTypeEqDecl("stream-error", 5)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeBorrow(0)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeBorrow(1)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeListU8...)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultErr(6)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(9, 6)...)
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x07,
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x09,
		0x00, 0x0a,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]output-stream.blocking-write-and-flush")))
	body = append(body, "[method]output-stream.blocking-write-and-flush"...)
	body = append(body, 0x01, 0x0c)
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x08,
		0x03, 'l', 'e', 'n', CValtypeU64,
		0x00, 0x0b,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]input-stream.blocking-read")))
	body = append(body, "[method]input-stream.blocking-read"...)
	body = append(body, 0x01, 0x0d)
	return body
}

func WasiIoStreamsReadWriteReactorInstanceTypeBody(outerErrorTypeidx, pollableT uint32) []byte {
	body := WasiIoStreamsReadWriteInstanceTypeBody(outerErrorTypeidx)
	body[2] = 0x18
	body = append(body, OuterAliasTypeDecl(1, pollableT)...)
	body = append(body, 0x01, 0x69, 0x0e)
	body = append(body, tcpMethodFuncDecl("subscribe", []string{"self"}, []byte{0x08}, 0x0f)...)
	body = append(body, 0x04, 0x00, byte(len("[method]input-stream.subscribe")))
	body = append(body, "[method]input-stream.subscribe"...)
	body = append(body, 0x01, 0x10)
	body = append(body, tcpMethodFuncDecl("subscribe", []string{"self"}, []byte{0x07}, 0x0f)...)
	body = append(body, 0x04, 0x00, byte(len("[method]output-stream.subscribe")))
	body = append(body, "[method]output-stream.subscribe"...)
	body = append(body, 0x01, 0x11)
	body = append(body,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x08,
		0x03, 'l', 'e', 'n', CValtypeU64,
		0x00, 0x0b,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]input-stream.read")))
	body = append(body, "[method]input-stream.read"...)
	body = append(body, 0x01, 0x12)
	return body
}

func WasiIoStreamsReadInstanceTypeBody(outerErrorTypeidx uint32) []byte {
	body := []byte{0x01, 0x42, 0x0b}
	body = append(body, ExportSubResourceDecl("input-stream")...)
	body = append(body, OuterAliasTypeDecl(1, outerErrorTypeidx)...)
	body = append(body, ExportTypeEqDecl("error", 1)...)
	body = append(body, 0x01, 0x69, 0x02)
	body = append(body, 0x01)
	body = append(body, InnerTypeVariant([]VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})...)
	body = append(body, ExportTypeEqDecl("stream-error", 4)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeBorrow(0)...)
	body = append(body, 0x01)
	body = append(body, InnerTypeListU8...)
	body = append(body, 0x01)
	body = append(body, InnerTypeResultOkErr(7, 5)...)
	body = append(body,
		0x01,
		0x40,
		0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x03, 'l', 'e', 'n', CValtypeU64,
		0x00, 0x08,
	)
	body = append(body, 0x04, 0x00, byte(len("[method]input-stream.blocking-read")))
	body = append(body, "[method]input-stream.blocking-read"...)
	body = append(body, 0x01, 0x09)
	return body
}

func PutImportSectionOneInstance(buf []byte, name string, instanceTypeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = putName(body, name)
	body = append(body, 0x05)
	body = leb128.UlebU64(body, uint64(instanceTypeidx))
	return wrapSection(buf, SectionImport, body)
}

func PutAliasSectionInstanceExportFunc(buf []byte, instanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(instanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

func PutAliasSectionInstanceExportType(buf []byte, instanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x03)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(instanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

func PutCanonSectionLowerNoOpts(buf []byte, funcIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 0)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonResourceDrop(buf []byte, resourceTypeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(resourceTypeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonWaitableSetNew(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x1f)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonWaitableSetWait(buf []byte, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x20)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonWaitableSetDrop(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x22)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonWaitableJoin(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x23)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSubtaskDrop(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x0d)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonThreadYield(buf []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x0c)
	body = append(body, 0x00)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonFutureNew(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x15)
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonFutureRead(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x16)
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonFutureWrite(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x17)
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonStreamNew(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x0e)
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonStreamRead(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x0f)
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonStreamWrite(buf []byte, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x10)
	body = leb128.UlebU64(body, uint64(typeidx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonStreamDropReadable(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x13)
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonStreamDropWritable(buf []byte, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x14)
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLowerWithMemory(buf []byte, funcIdx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLowerAsync(buf []byte, funcIdx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLowerAsyncRealloc(buf []byte, funcIdx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 3)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04)
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLowerWithMemoryRealloc(buf []byte, funcIdx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04)
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCoreInstanceSectionFromOneFuncExport(buf []byte, exportName string, coreFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, 1)
	body = putName(body, exportName)
	body = append(body, CoreSortFunc)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	return wrapSection(buf, SectionCoreInstance, body)
}

type CoreInstanceExport struct {
	Name string
	Sort byte
	Idx  uint32
}

func PutCoreInstanceSectionFromExports(buf []byte, exports []CoreInstanceExport) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, uint64(len(exports)))
	for _, e := range exports {
		body = putName(body, e.Name)
		body = append(body, e.Sort)
		body = leb128.UlebU64(body, uint64(e.Idx))
	}
	return wrapSection(buf, SectionCoreInstance, body)
}

func PutAliasSectionCoreExport(buf []byte, coreSort byte, coreInstanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, coreSort)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, uint64(coreInstanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

func PutCoreInstanceSectionInstantiateWithInstanceArgs(buf []byte, moduleIdx uint32, argNames []string, instanceIdxs []uint32) []byte {
	if len(argNames) != len(instanceIdxs) {
		panic("component: argNames and instanceIdxs must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(moduleIdx))
	body = leb128.UlebU64(body, uint64(len(argNames)))
	for i := range argNames {
		body = putName(body, argNames[i])
		body = append(body, CoreSortInstance)
		body = leb128.UlebU64(body, uint64(instanceIdxs[i]))
	}
	return wrapSection(buf, SectionCoreInstance, body)
}

func PutCoreInstanceSectionInstantiate(buf []byte, moduleIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(moduleIdx))
	body = leb128.UlebU64(body, 0)
	return wrapSection(buf, SectionCoreInstance, body)
}

func PutAliasSectionCoreExportFunc(buf []byte, coreInstanceIdx uint32, name string) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, uint64(coreInstanceIdx))
	body = putName(body, name)
	return wrapSection(buf, SectionAlias, body)
}

const (
	cFunctypeSync  = 0x40
	cFunctypeAsync = 0x43
)

func PutTypeSectionOneFunc(buf []byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	return putTypeSectionOneFuncTag(buf, cFunctypeSync, paramNames, paramValtypes, resultValtype)
}

func PutTypeSectionOneFuncAsync(buf []byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	return putTypeSectionOneFuncTag(buf, cFunctypeAsync, paramNames, paramValtypes, resultValtype)
}

func putTypeSectionOneFuncTag(buf []byte, tag byte, paramNames []string, paramValtypes []byte, resultValtype byte) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, tag)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	body = append(body, 0x00)
	body = append(body, resultValtype)
	return wrapSection(buf, SectionType, body)
}

func PutTypeSectionOneDefined(buf []byte, defBody []byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, defBody...)
	return wrapSection(buf, SectionType, body)
}

func PutTypeSectionOneFuncResultIdx(buf []byte, paramNames []string, paramValtypes []byte, resultIdx uint32) []byte {
	return putTypeSectionOneFuncResultIdxTag(buf, cFunctypeSync, paramNames, paramValtypes, resultIdx)
}

func PutTypeSectionOneFuncResultIdxAsync(buf []byte, paramNames []string, paramValtypes []byte, resultIdx uint32) []byte {
	return putTypeSectionOneFuncResultIdxTag(buf, cFunctypeAsync, paramNames, paramValtypes, resultIdx)
}

func putTypeSectionOneFuncResultIdxTag(buf []byte, tag byte, paramNames []string, paramValtypes []byte, resultIdx uint32) []byte {
	if len(paramNames) != len(paramValtypes) {
		panic("component: paramNames and paramValtypes must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, tag)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramValtypes[i])
	}
	body = append(body, 0x00)
	body = leb128.SlebI64(body, int64(resultIdx))
	return wrapSection(buf, SectionType, body)
}

func leb128SlebBytes(idx uint32) []byte { return leb128.SlebI64(nil, int64(idx)) }

func PutTypeSectionOneFuncGeneral(buf []byte, paramNames []string, paramVals [][]byte, resultVal []byte) []byte {
	return putTypeSectionOneFuncGeneralTag(buf, cFunctypeSync, paramNames, paramVals, resultVal)
}

func PutTypeSectionOneFuncGeneralAsync(buf []byte, paramNames []string, paramVals [][]byte, resultVal []byte) []byte {
	return putTypeSectionOneFuncGeneralTag(buf, cFunctypeAsync, paramNames, paramVals, resultVal)
}

func putTypeSectionOneFuncGeneralTag(buf []byte, tag byte, paramNames []string, paramVals [][]byte, resultVal []byte) []byte {
	if len(paramNames) != len(paramVals) {
		panic("component: paramNames and paramVals must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, tag)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramVals[i]...)
	}
	body = append(body, 0x00)
	body = append(body, resultVal...)
	return wrapSection(buf, SectionType, body)
}

func PutTypeSectionOneFuncGeneralVoid(buf []byte, paramNames []string, paramVals [][]byte) []byte {
	if len(paramNames) != len(paramVals) {
		panic("component: paramNames and paramVals must have equal length")
	}
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x40)
	body = leb128.UlebU64(body, uint64(len(paramNames)))
	for i := range paramNames {
		body = putName(body, paramNames[i])
		body = append(body, paramVals[i]...)
	}
	body = append(body, 0x01)
	body = leb128.UlebU64(body, 0)
	return wrapSection(buf, SectionType, body)
}

func PutCanonSectionLiftNoOpts(buf []byte, coreFuncIdx uint32, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 0)
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLiftAsync(buf []byte, coreFuncIdx uint32, typeidx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x06)
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLiftAsyncWithMemory(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLiftAsyncWithMemoryRealloc(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 3)
	body = append(body, 0x06)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04)
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonTaskReturnSingle(buf []byte, resultValtype byte) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x09)
	body = append(body, 0x00)
	body = append(body, resultValtype)
	body = append(body, 0x00)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonTaskReturnStringWithMemory(buf []byte, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x09)
	body = append(body, 0x00)
	body = append(body, cValtypeString)
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonTaskReturnTypeIdxWithMemory(buf []byte, typeIdx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x09)
	body = append(body, 0x00)
	body = append(body, leb128SlebBytes(typeIdx)...)
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonTaskReturnTypeIdx(buf []byte, typeIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x09)
	body = append(body, 0x00)
	body = append(body, leb128SlebBytes(typeIdx)...)
	body = append(body, 0x00)
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLiftWithMemory(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutCanonSectionLiftWithMemoryRealloc(buf []byte, coreFuncIdx uint32, typeidx uint32, memIdx uint32, reallocFuncIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = append(body, 0x00)
	body = leb128.UlebU64(body, uint64(coreFuncIdx))
	body = leb128.UlebU64(body, 2)
	body = append(body, 0x03)
	body = leb128.UlebU64(body, uint64(memIdx))
	body = append(body, 0x04)
	body = leb128.UlebU64(body, uint64(reallocFuncIdx))
	body = leb128.UlebU64(body, uint64(typeidx))
	return wrapSection(buf, SectionCanon, body)
}

func PutExportSectionOneFunc(buf []byte, name string, funcIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = putName(body, name)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, uint64(funcIdx))
	body = append(body, 0x00)
	return wrapSection(buf, SectionExport, body)
}

func PutTypeSectionResultEmptyAndUnitFuncReturningResult(buf []byte, resultTypeidx uint32) []byte {
	body := []byte{
		0x02,
		0x6a, 0x00, 0x00,
		0x40,
		0x00,
		0x00,
	}
	body = leb128.UlebU64(body, uint64(resultTypeidx))
	return wrapSection(buf, SectionType, body)
}

func PutInstanceSectionOnePackagedFunc(buf []byte, exportName string, funcIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = putName(body, exportName)
	body = append(body, 0x01)
	body = leb128.UlebU64(body, uint64(funcIdx))
	return wrapSection(buf, SectionInstance, body)
}

func PutExportSectionOneInstance(buf []byte, name string, instanceIdx uint32) []byte {
	body := []byte{}
	body = leb128.UlebU64(body, 1)
	body = append(body, 0x00)
	body = putName(body, name)
	body = append(body, 0x05)
	body = leb128.UlebU64(body, uint64(instanceIdx))
	body = append(body, 0x00)
	return wrapSection(buf, SectionExport, body)
}

func PutComponentTypeSection(buf []byte, payload []byte) []byte {
	body := putName(nil, "component-type")
	body = append(body, payload...)
	return wrapSection(buf, SectionCustom, body)
}
