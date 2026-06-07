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
