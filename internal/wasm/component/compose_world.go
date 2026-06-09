package component

import (
	"fmt"

	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// compose_world.go is P2's payoff (docs/WIT-BRING-YOUR-OWN.md): build a real
// component whose import surface is declared from the full decoded WIT world —
// every interface, emitted by componenttype.EmitWorldImports — instead of the
// hand-written minimized type bodies. It reuses the existing
// gComposer.lower/finish suffix wiring unchanged; the only difference is that
// the composer is *seeded* with the world prefix and the world's component
// index layout rather than building a minimal prefix via the ensure* methods.
// The native Compose path is untouched.

// ComposeFromWorld wraps `core` into a component whose top-level imports are
// the full decoded `w` world, lifting `coreExportName` as wasi:cli/run.
// `imports` are the preview-2 imports the core actually uses (the rest of the
// world is imported but unused). `w` may be an embedded world (DecodeWorld) or
// a user-supplied one (DecodeWorldBytes) — the path is identical.
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
	// Seed the imported-instance map from the world's prefix layout so the
	// suffix aliases each interface's functions from the right instance.
	for _, iface := range w.Interfaces() {
		if idx := w.ImportInstanceIndex(iface.Name); idx >= 0 {
			g.inst[iface.Name] = uint32(idx)
		}
	}
	// Surface each dropped resource as a component-level type (an alias export
	// from its imported instance) and thread the index into the gDrop lowering
	// — the canon resource.drop needs a component type index for the resource,
	// which the world import prefix doesn't surface at the top level (P5,
	// docs/WIT-BRING-YOUR-OWN.md). Purely additive: a program with no
	// `[resource-drop]` imports emits no alias sections, so its bytes are
	// unchanged. Each resource is surfaced once and shared across its drops.
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

// coreFuncImport is one function import of a core module: its (module, name)
// and the param valtypes of its type (the trampoline signature a memory
// lowering mirrors).
type coreFuncImport struct {
	module, name string
	params       []byte
	results      []byte
}

// coreFuncImports parses a core module's type + import sections and returns
// each function import with its param valtypes. Non-function imports are
// skipped. Bails out (nil) on malformed input.
func coreFuncImports(bin []byte) []coreFuncImport {
	const preambleLen = 8
	if len(bin) < preambleLen {
		return nil
	}
	// pass 1: functype params + results from the type section (id 1).
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
	// pass 2: import section (id 2) → func imports with their type's params.
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
			case 0: // func: typeidx
				ti, ks := readULEB(b3)
				b3 = b3[ks:]
				var params, results []byte
				if int(ti) < len(typeParams) {
					params = typeParams[ti]
					results = typeResults[ti]
				}
				out = append(out, coreFuncImport{module: mod, name: fld, params: params, results: results})
			case 1: // table: reftype + limits
				if len(b3) >= 2 {
					b3 = b3[2:]
					_, ks := readULEB(b3)
					b3 = b3[ks:]
				}
			case 2: // memory: limits
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
			case 3: // global: valtype + mut
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

// ComposeFromWorldAuto wraps `core` into a wasi:cli/run component, deriving the
// imports it wires from the core module's own function imports: each import's
// lowering kind is classified against `world` (componenttype.Classify) and its
// trampoline params come from the core import's type. No hardcoded import
// list. Every imported interface must be declared by the world, and
// resource-drop imports are not handled yet (they appear in socket/http shapes,
// not CLI/fs ones).
// ComposeFromWorldAuto wraps `core` into a wasi:cli/run component, deriving the
// imports it wires from the core module's own function imports classified
// against the decoded world `w` (embedded or user-supplied). No hardcoded
// import list. Every imported interface must be declared by the world;
// resource-drop imports are not handled yet.
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
			// `[resource-drop]<res>` — drop an owned handle (P5,
			// docs/WIT-BRING-YOUR-OWN.md). The resource is surfaced as a
			// component-level type and threaded into the canon resource.drop by
			// ComposeFromWorld; here we just validate the world declares it and
			// record the gDrop lowering (resourceT filled in once `g` exists).
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
			iface:  imp.module,
			name:   imp.name,
			kind:   gKindFor(wi.Classify(f)),
			params: imp.params,
			// The core import already carries the flat-lowered result (e.g. a
			// memory-param `func(string) -> u32` imports as (i32,i32)->i32); a
			// memory trampoline must mirror it. Empty for the WASI imports
			// (results go through a retptr → void), so this is a no-op there.
			results: imp.results,
		})
	}
	return ComposeFromWorld(core, w, "_lang_run", imports)
}

// ifaceHasResource reports whether the world interface declares a resource of
// the given (WIT) name — the set a `[resource-drop]<name>` import may target.
func ifaceHasResource(wi componenttype.WorldInterface, name string) bool {
	for _, r := range wi.Resources {
		if r == name {
			return true
		}
	}
	return false
}

// ComposeExportsFromWorld wraps a reactor `core` — a library that provides one
// or more `@export` functions and no cli/run entry — into a component that
// EXPORTS each world export the core implements, lifting it with the WIT
// canonical ABI (P6 — docs/WIT-BRING-YOUR-OWN.md). It generalises the fixed
// `_lang_run` / `incoming-handler` lifts: the wasm backend surfaced a core
// export `iface#wit-name` per `@export` function, and this lifts each one whose
// (iface, wit-name) the world declares as an export. Scalar signatures only
// (this slice); composite params/results follow. Any core imports the reactor
// uses are wired from the world exactly as for a command.
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
			return nil, fmt.Errorf("component: resource-drop in a reactor export is not supported yet (%q)", imp.name)
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
	g.add(imports...)
	userInst := g.lower(core)

	coreExports := coreFuncExportNames(core)
	// A string/list export needs the core memory aliased for the lift. If the
	// import lowering already aliased it (a mem trampoline), reuse core-memory
	// index 0; otherwise alias it now. (Mixing mem-imports with composite
	// exports beyond this is a later refinement.)
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
	var memIdx uint32 // the first (only) aliased core memory
	var reallocIdx uint32
	if needRealloc {
		// A string/list PARAMETER lift uses cabi_realloc to materialise the
		// incoming bytes in the core's memory; the core exports it.
		reallocIdx = g.c.aliasReallocFunc(userInst)
	}

	emitted := 0
	// resourceInst maps each imported resource name to its instance index, so a
	// handle-typed export param can surface the right imported resource (P6).
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

// cValtypeString is the component-model primitive byte for `string` (it equals
// componenttype.primString — a primitive Valtype's Prim byte is the CValtype).
const cValtypeString = 0x73

// exportNeedsMemory reports whether lifting `sig` needs the core memory aliased
// — true when any parameter or the result is a string/list (which the lift
// reads from / writes to linear memory). Scalars need no memory. A `list<T>`
// result resolves through `wi` (its element-type index).
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
	// An option/result/tuple result flattens to > 1 core value, so it returns
	// indirectly through the core memory — the lift reads it.
	if _, ok := wi.OptionElemPrim(sig.Result); ok {
		return true
	}
	if _, _, ok := wi.ResultArmPrims(sig.Result); ok {
		return true
	}
	_, isTuple := wi.TupleElemPrims(sig.Result)
	return isTuple
}

// exportNeedsRealloc reports whether lifting `sig` needs cabi_realloc — true
// when any parameter is a string/list, which the canonical ABI materialises in
// the core's memory before the call.
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

// isStringOrList reports whether `v` is a `string` or a `list<T>` — the value
// types that carry linear-memory data across the canonical ABI (and so drive
// the memory/realloc lift options for an export).
func isStringOrList(wi componenttype.WorldInterface, v componenttype.Valtype) bool {
	if v.IsPrim {
		return v.Prim == cValtypeString
	}
	_, isList := wi.ListElemPrim(v)
	return isList
}

// liftExport lifts one world export: alias the surfaced core func, build its
// component functype from the WIT signature, canon-lift it, and export it as
// `wi.Name` exposing `witName`. A scalar result uses the no-opts lift; a string
// or `list<T>` result uses the memory lift (the core returns a pointer to the
// (ptr,len) return area, which the lift reads from `memIdx`). A `list<T>` result
// emits the `list<elem>` defined type first and references it from the functype.
// String/list PARAMETERS beyond `string` are not handled yet.
func (g *gComposer) liftExport(userInst uint32, wi componenttype.WorldInterface, witName, coreName string, sig *componenttype.FuncType, memIdx, reallocIdx uint32, resourceInst map[string]uint32) error {
	iface := wi.Name
	if sig == nil {
		return fmt.Errorf("component: export %s#%s has no resolved signature", iface, witName)
	}
	c := g.c
	coreF := c.aliasCoreFunc(userInst, coreName)

	// Surface any imported resource a handle param/result references *before*
	// capturing `nextIdx`: aliasType emits an alias section immediately and bumps
	// c.nType, so it must happen before the defined-type index mirror is taken.
	// (A handle is an i32 at the canonical ABI; the own/borrow defined type just
	// names the resource.) P6 — docs/WIT-BRING-YOUR-OWN.md.
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

	// Each defined type a param/result references (a `list<T>` or a handle's
	// own/borrow) must be emitted before the functype so its index resolves.
	// Collect the type sections to emit, then the per-slot valtype encodings (a
	// primitive's single byte, or the sleb-encoded index of a list/handle type).
	// `nextIdx` mirrors c.nType as those types are appended.
	var defs [][]byte // defined-type bodies, in emit order
	nextIdx := c.nType
	// encodeSlot encodes one param/result valtype, emitting any referenced
	// defined type into `defs`. The bool result reports whether the slot carries
	// linear-memory data (string / list / sum-type), which drives the memory /
	// realloc lift option. `allowSum` enables option/result encoding (results
	// only this slice — a sum-type *param* would need a param wrapper the wasm
	// backend doesn't build yet).
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
			// own<R> / borrow<R>: a handle is an i32 at the canonical ABI (no
			// memory). The resource type was surfaced in the pre-pass; reference it
			// from the own/borrow defined type.
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
				// tuple<...> flattens to its elements (> 1 for a multi-element
				// tuple) → indirect result, memory lift. Each prim's CValtype byte
				// is < 64, so InnerTypeTuple's single-byte elements are correct.
				idx := nextIdx
				nextIdx++
				defs = append(defs, InnerTypeTuple(elems))
				return leb128SlebBytes(idx), true, nil
			}
			if e, ok := wi.OptionElemPrim(v); ok {
				// option<prim> flattens to (disc, payload) > 1 → indirect result,
				// memory lift. A prim's CValtype byte is < 64, so InnerTypeOption's
				// single inner byte is correct.
				idx := nextIdx
				nextIdx++
				defs = append(defs, InnerTypeOption(e))
				return leb128SlebBytes(idx), true, nil
			}
			if okB, errB, ok := wi.ResultArmPrims(v); ok {
				// result<okPrim, errPrim>: each arm's CValtype byte is < 128, so the
				// uleb InnerTypeResultOkErr writes equals the prim valtype byte.
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
	if sig.NamedResults {
		return fmt.Errorf("component: export %s#%s: multi result (unsupported in this slice)", iface, witName)
	}
	rval, hasMemResult, err := encodeSlot(sig.Result, "result", true)
	if err != nil {
		return err
	}

	// Emit the referenced list types (in index order), then the functype.
	for _, d := range defs {
		c.buf = PutTypeSectionOneDefined(c.buf, d)
		c.nType++
	}
	funcType := c.nType
	c.buf = PutTypeSectionOneFuncGeneral(c.buf, pnames, pvals, rval)
	c.nType++

	switch {
	case hasMemParam:
		// A string / list<T> parameter: the canonical ABI materialises the
		// incoming bytes in the core's memory via cabi_realloc, then passes
		// (ptr,len) to the core func. The realloc lift carries both memory +
		// realloc (it also covers a string/list result, whose return-area read
		// needs memory).
		c.buf = PutCanonSectionLiftWithMemoryRealloc(c.buf, coreF, funcType, memIdx, reallocIdx)
	case hasMemResult:
		// A string / list<T> result flattens to (ptr,len) — more than one core
		// value, so the core func returns a pointer to the [ptr,len] return
		// area, which the memory lift reads from the core's linear memory.
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

// coreFuncExportNames returns the set of function-export names of a core
// module (export section, id 7, kind 0x00 = func). Used to find the
// `iface#wit-name` exports the wasm backend surfaced for `@export` functions.
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
		if id != 7 { // export section
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
			if kind == 0x00 { // func
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
