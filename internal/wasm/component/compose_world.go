package component

import (
	"fmt"

	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

func ComposeFromWorld(core []byte, w *componenttype.World, coreExportName string, imports []gImport) ([]byte, error) {
	prefix, err := w.EmitWorldImports()
	if err != nil {
		return nil, err
	}
	pl := w.PrefixLayout()
	g := &gComposer{
		c: &p2composer{
			buf:   append(PutComponentHeader(nil), prefix...),
			nType: pl.Types,
			nInst: pl.Instances,
		},
		surfaced: map[string]uint32{},
		inst:     map[string]uint32{},
	}
	for _, iface := range w.Interfaces() {
		if idx := w.ImportInstanceIndex(iface.Name); idx >= 0 {
			g.inst[iface.Name] = uint32(idx)
		}
	}
	for i := range imports {
		if imports[i].kind != gDrop {
			continue
		}
		res := imports[i].name[len("[resource-drop]"):]
		t, ok := g.surfaced[res]
		if !ok {
			instIdx, ok := g.inst[imports[i].iface]
			if !ok {
				return nil, fmt.Errorf("component: resource-drop import %q: interface %q not imported by the world", imports[i].name, imports[i].iface)
			}
			t = g.c.aliasType(instIdx, res)
			g.surfaced[res] = t
		}
		imports[i].resourceT = t
	}
	g.add(imports...)
	return g.finish(core, coreExportName, ""), nil
}

type coreFuncImport struct {
	module, name string
	params       []byte
	results      []byte
}

func coreFuncImports(bin []byte) []coreFuncImport {
	const preambleLen = 8
	if len(bin) < preambleLen {
		return nil
	}
	var typeParams [][]byte
	var typeResults [][]byte
	for off := preambleLen; off < len(bin); {
		id := bin[off]
		off++
		size, n := readULEB(bin[off:])
		if n == 0 || off+n+int(size) > len(bin) {
			return nil
		}
		off += n
		body := bin[off : off+int(size)]
		off += int(size)
		if id != 1 {
			continue
		}
		count, m := readULEB(body)
		if m == 0 {
			break
		}
		body = body[m:]
		for i := uint64(0); i < count && len(body) > 0; i++ {
			if body[0] != 0x60 {
				break
			}
			body = body[1:]
			pc, pm := readULEB(body)
			body = body[pm:]
			params := make([]byte, 0, pc)
			for j := uint64(0); j < pc && len(body) > 0; j++ {
				params = append(params, body[0])
				body = body[1:]
			}
			rc, rm := readULEB(body)
			body = body[rm:]
			results := make([]byte, 0, rc)
			for j := uint64(0); j < rc && len(body) > 0; j++ {
				results = append(results, body[0])
				body = body[1:]
			}
			typeParams = append(typeParams, params)
			typeResults = append(typeResults, results)
		}
		break
	}
	for off := preambleLen; off < len(bin); {
		id := bin[off]
		off++
		size, n := readULEB(bin[off:])
		if n == 0 || off+n+int(size) > len(bin) {
			return nil
		}
		off += n
		body := bin[off : off+int(size)]
		off += int(size)
		if id != 2 {
			continue
		}
		count, m := readULEB(body)
		if m == 0 {
			return nil
		}
		body = body[m:]
		var out []coreFuncImport
		for i := uint64(0); i < count && len(body) > 0; i++ {
			mod, b2 := readName(body)
			fld, b3 := readName(b2)
			if len(b3) < 1 {
				break
			}
			kind := b3[0]
			b3 = b3[1:]
			switch kind {
			case 0:
				ti, ks := readULEB(b3)
				b3 = b3[ks:]
				var params, results []byte
				if int(ti) < len(typeParams) {
					params = typeParams[ti]
					results = typeResults[ti]
				}
				out = append(out, coreFuncImport{module: mod, name: fld, params: params, results: results})
			case 1:
				if len(b3) >= 2 {
					b3 = b3[2:]
					_, ks := readULEB(b3)
					b3 = b3[ks:]
				}
			case 2:
				if len(b3) >= 1 {
					flag := b3[0]
					b3 = b3[1:]
					_, ks := readULEB(b3)
					b3 = b3[ks:]
					if flag == 1 {
						_, ks2 := readULEB(b3)
						b3 = b3[ks2:]
					}
				}
			case 3:
				if len(b3) >= 2 {
					b3 = b3[2:]
				}
			}
			body = b3
		}
		return out
	}
	return nil
}

func ComposeFromWorldAuto(core []byte, w *componenttype.World) ([]byte, error) {
	byIface := map[string]componenttype.WorldInterface{}
	for _, wi := range w.Interfaces() {
		byIface[wi.Name] = wi
	}
	var imports []gImport
	for _, imp := range coreFuncImports(core) {
		wi, ok := byIface[imp.module]
		if !ok {
			return nil, fmt.Errorf("component: core imports interface %q not declared by the world", imp.module)
		}
		if hasResourceDropPrefix(imp.name) {
			res := imp.name[len("[resource-drop]"):]
			if !ifaceHasResource(wi, res) {
				return nil, fmt.Errorf("component: world interface %q has no resource %q to drop", imp.module, res)
			}
			imports = append(imports, gImport{iface: imp.module, name: imp.name, kind: gDrop})
			continue
		}
		f, ok := worldFunc(wi, imp.name)
		if !ok {
			return nil, fmt.Errorf("component: interface %q has no function %q", imp.module, imp.name)
		}
		imports = append(imports, gImport{
			iface:   imp.module,
			name:    imp.name,
			kind:    gKindFor(wi.Classify(f)),
			params:  imp.params,
			results: imp.results,
		})
	}
	return ComposeFromWorld(core, w, "_lang_run", imports)
}

func ifaceHasResource(wi componenttype.WorldInterface, name string) bool {
	for _, r := range wi.Resources {
		if r == name {
			return true
		}
	}
	return false
}

func ComposeExportsFromWorld(core []byte, w *componenttype.World) ([]byte, error) {
	byIface := map[string]componenttype.WorldInterface{}
	for _, wi := range w.Interfaces() {
		byIface[wi.Name] = wi
	}
	var imports []gImport
	for _, imp := range coreFuncImports(core) {
		wi, ok := byIface[imp.module]
		if !ok {
			return nil, fmt.Errorf("component: core imports interface %q not declared by the world", imp.module)
		}
		if hasResourceDropPrefix(imp.name) {
			res := imp.name[len("[resource-drop]"):]
			if !ifaceHasResource(wi, res) {
				return nil, fmt.Errorf("component: world interface %q has no resource %q to drop", imp.module, res)
			}
			imports = append(imports, gImport{iface: imp.module, name: imp.name, kind: gDrop})
			continue
		}
		f, ok := worldFunc(wi, imp.name)
		if !ok {
			return nil, fmt.Errorf("component: interface %q has no function %q", imp.module, imp.name)
		}
		imports = append(imports, gImport{
			iface:   imp.module,
			name:    imp.name,
			kind:    gKindFor(wi.Classify(f)),
			params:  imp.params,
			results: imp.results,
		})
	}

	prefix, err := w.EmitWorldImports()
	if err != nil {
		return nil, err
	}
	pl := w.PrefixLayout()
	g := &gComposer{
		c: &p2composer{
			buf:   append(PutComponentHeader(nil), prefix...),
			nType: pl.Types,
			nInst: pl.Instances,
		},
		surfaced: map[string]uint32{},
		inst:     map[string]uint32{},
	}
	for _, iface := range w.Interfaces() {
		if idx := w.ImportInstanceIndex(iface.Name); idx >= 0 {
			g.inst[iface.Name] = uint32(idx)
		}
	}
	for i := range imports {
		if imports[i].kind != gDrop {
			continue
		}
		res := imports[i].name[len("[resource-drop]"):]
		t, ok := g.surfaced[res]
		if !ok {
			instIdx, ok := g.inst[imports[i].iface]
			if !ok {
				return nil, fmt.Errorf("component: resource-drop import %q: interface %q not imported by the world", imports[i].name, imports[i].iface)
			}
			t = g.c.aliasType(instIdx, res)
			g.surfaced[res] = t
		}
		imports[i].resourceT = t
	}
	g.add(imports...)
	userInst := g.lower(core)

	coreExports := coreFuncExportNames(core)
	lowerAliasedMem := false
	for i := range imports {
		if imports[i].kind == gMem || imports[i].kind == gMemRealloc {
			lowerAliasedMem = true
		}
	}
	needMem := false
	needRealloc := false
	for _, wi := range w.ExportedInterfaces() {
		for _, f := range wi.FuncSigs {
			if !coreExports[wi.Name+"#"+f.Name] {
				continue
			}
			if exportNeedsMemory(wi, f.Sig) {
				needMem = true
			}
			if exportNeedsRealloc(wi, f.Sig) {
				needMem = true
				needRealloc = true
			}
		}
	}
	if needMem && !lowerAliasedMem {
		g.c.aliasMemory(userInst)
	}
	var memIdx uint32
	var reallocIdx uint32
	if needRealloc {
		reallocIdx = g.c.aliasReallocFunc(userInst)
	}

	emitted := 0
	resourceInst := map[string]uint32{}
	for _, wi := range w.Interfaces() {
		if idx, ok := g.inst[wi.Name]; ok {
			for _, r := range wi.Resources {
				resourceInst[r] = idx
			}
		}
	}
	for _, wi := range w.ExportedInterfaces() {
		for _, f := range wi.FuncSigs {
			coreName := wi.Name + "#" + f.Name
			if !coreExports[coreName] {
				continue
			}
			if err := g.liftExport(userInst, wi, f.Name, coreName, f.Sig, memIdx, reallocIdx, resourceInst); err != nil {
				return nil, err
			}
			emitted++
		}
	}
	if emitted == 0 {
		return nil, fmt.Errorf("component: no world export matched a surfaced core export (iface#wit-name)")
	}
	return g.c.buf, nil
}

const cValtypeString = 0x73

func exportNeedsMemory(wi componenttype.WorldInterface, sig *componenttype.FuncType) bool {
	if sig == nil {
		return false
	}
	for i := range sig.Params {
		if isStringOrList(wi, sig.Params[i].Ty) {
			return true
		}
	}
	if sig.NamedResults {
		return false
	}
	if isStringOrList(wi, sig.Result) {
		return true
	}
	if _, ok := wi.OptionElemPrim(sig.Result); ok {
		return true
	}
	if _, _, ok := wi.ResultArmPrims(sig.Result); ok {
		return true
	}
	_, isTuple := wi.TupleElemPrims(sig.Result)
	return isTuple
}

func exportNeedsRealloc(wi componenttype.WorldInterface, sig *componenttype.FuncType) bool {
	if sig == nil {
		return false
	}
	for i := range sig.Params {
		if isStringOrList(wi, sig.Params[i].Ty) {
			return true
		}
	}
	return false
}

func isStringOrList(wi componenttype.WorldInterface, v componenttype.Valtype) bool {
	if v.IsPrim {
		return v.Prim == cValtypeString
	}
	_, isList := wi.ListElemPrim(v)
	return isList
}

func (g *gComposer) liftExport(userInst uint32, wi componenttype.WorldInterface, witName, coreName string, sig *componenttype.FuncType, memIdx, reallocIdx uint32, resourceInst map[string]uint32) error {
	iface := wi.Name
	if sig == nil {
		return fmt.Errorf("component: export %s#%s has no resolved signature", iface, witName)
	}
	c := g.c
	coreF := c.aliasCoreFunc(userInst, coreName)

	surfaceHandle := func(v componenttype.Valtype) error {
		rname, _, ok := wi.HandleResource(v)
		if !ok {
			return nil
		}
		if _, done := g.surfaced[rname]; done {
			return nil
		}
		instIdx, found := resourceInst[rname]
		if !found {
			return fmt.Errorf("component: export %s#%s: resource %q for a handle parameter is not imported by the world", iface, witName, rname)
		}
		g.surfaced[rname] = g.c.aliasType(instIdx, rname)
		return nil
	}
	for i := range sig.Params {
		if err := surfaceHandle(sig.Params[i].Ty); err != nil {
			return err
		}
	}
	if !sig.NamedResults {
		if err := surfaceHandle(sig.Result); err != nil {
			return err
		}
	}

	var defs [][]byte
	nextIdx := c.nType
	encodeSlot := func(v componenttype.Valtype, what string, allowSum bool) ([]byte, bool, error) {
		if v.IsPrim {
			return []byte{v.Prim}, v.Prim == cValtypeString, nil
		}
		if e, ok := wi.ListElemPrim(v); ok {
			idx := nextIdx
			nextIdx++
			defs = append(defs, InnerTypeList(e))
			return leb128SlebBytes(idx), true, nil
		}
		if rname, owned, ok := wi.HandleResource(v); ok {
			rt, surfaced := g.surfaced[rname]
			if !surfaced {
				return nil, false, fmt.Errorf("component: export %s#%s: resource %q not surfaced", iface, witName, rname)
			}
			idx := nextIdx
			nextIdx++
			if owned {
				defs = append(defs, InnerTypeOwn(rt))
			} else {
				defs = append(defs, InnerTypeBorrow(rt))
			}
			return leb128SlebBytes(idx), false, nil
		}
		if allowSum {
			if elems, ok := wi.TupleElemPrims(v); ok {
				idx := nextIdx
				nextIdx++
				defs = append(defs, InnerTypeTuple(elems))
				return leb128SlebBytes(idx), true, nil
			}
			if e, ok := wi.OptionElemPrim(v); ok {
				idx := nextIdx
				nextIdx++
				defs = append(defs, InnerTypeOption(e))
				return leb128SlebBytes(idx), true, nil
			}
			if okB, errB, ok := wi.ResultArmPrims(v); ok {
				idx := nextIdx
				nextIdx++
				defs = append(defs, InnerTypeResultOkErr(uint32(okB), uint32(errB)))
				return leb128SlebBytes(idx), true, nil
			}
		}
		return nil, false, fmt.Errorf("component: export %s#%s: non-scalar / unsupported %s", iface, witName, what)
	}

	var pnames []string
	var pvals [][]byte
	hasMemParam := false
	for i := range sig.Params {
		p := sig.Params[i]
		enc, isMem, err := encodeSlot(p.Ty, "parameter", false)
		if err != nil {
			return err
		}
		if isMem {
			hasMemParam = true
		}
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("p%d", i)
		}
		pnames = append(pnames, name)
		pvals = append(pvals, enc)
	}
	isVoid := sig.NamedResults && len(sig.Results) == 0
	if sig.NamedResults && !isVoid {
		return fmt.Errorf("component: export %s#%s: multi result (unsupported in this slice)", iface, witName)
	}
	var rval []byte
	hasMemResult := false
	if !isVoid {
		var err error
		rval, hasMemResult, err = encodeSlot(sig.Result, "result", true)
		if err != nil {
			return err
		}
	}

	for _, d := range defs {
		c.buf = PutTypeSectionOneDefined(c.buf, d)
		c.nType++
	}
	funcType := c.nType
	if isVoid {
		c.buf = PutTypeSectionOneFuncGeneralVoid(c.buf, pnames, pvals)
	} else {
		c.buf = PutTypeSectionOneFuncGeneral(c.buf, pnames, pvals, rval)
	}
	c.nType++

	switch {
	case hasMemParam:
		c.buf = PutCanonSectionLiftWithMemoryRealloc(c.buf, coreF, funcType, memIdx, reallocIdx)
	case hasMemResult:
		c.buf = PutCanonSectionLiftWithMemory(c.buf, coreF, funcType, memIdx)
	default:
		c.buf = PutCanonSectionLiftNoOpts(c.buf, coreF, funcType)
	}
	lifted := c.nCFunc
	c.nCFunc++
	c.buf = PutInstanceSectionOnePackagedFunc(c.buf, witName, lifted)
	inst := c.nInst
	c.nInst++
	c.buf = PutExportSectionOneInstance(c.buf, iface, inst)
	return nil
}

func coreFuncExportNames(bin []byte) map[string]bool {
	out := map[string]bool{}
	const preambleLen = 8
	if len(bin) < preambleLen {
		return out
	}
	for off := preambleLen; off < len(bin); {
		id := bin[off]
		off++
		size, n := readULEB(bin[off:])
		if n == 0 || off+n+int(size) > len(bin) {
			return out
		}
		off += n
		body := bin[off : off+int(size)]
		off += int(size)
		if id != 7 {
			continue
		}
		count, m := readULEB(body)
		if m == 0 {
			return out
		}
		body = body[m:]
		for i := uint64(0); i < count && len(body) > 0; i++ {
			name, rest := readName(body)
			if len(rest) < 1 {
				break
			}
			kind := rest[0]
			rest = rest[1:]
			_, ks := readULEB(rest)
			rest = rest[ks:]
			if kind == 0x00 {
				out[name] = true
			}
			body = rest
		}
		return out
	}
	return out
}

func worldFunc(wi componenttype.WorldInterface, name string) (componenttype.WorldFunc, bool) {
	for _, f := range wi.FuncSigs {
		if f.Name == name {
			return f, true
		}
	}
	return componenttype.WorldFunc{}, false
}

func gKindFor(k componenttype.LowerKind) gLowerKind {
	switch k {
	case componenttype.KindMem:
		return gMem
	case componenttype.KindMemRealloc:
		return gMemRealloc
	default:
		return gNoOpt
	}
}

func hasResourceDropPrefix(name string) bool {
	const p = "[resource-drop]"
	return len(name) >= len(p) && name[:len(p)] == p
}
