// Package monomorph turns generic function declarations + their
// per-call-site type-argument inferences (filled in by the
// checker) into concrete, name-mangled clones — so every later
// stage (IR lowering, codegen, interp) only ever sees monomorphic
// functions.
//
// Pipeline ordering: the pass runs after `checker.Check` and
// before any IR / codegen. It mutates the program in place:
//
//   - For every Call whose Callee is a generic FuncDecl, the
//     mangled clone name overwrites the Callee identifier.
//   - For every unique (name, type-args) instantiation, a cloned
//     FuncDecl is appended to prog.Funcs with TypeParams cleared
//     and ParamType references substituted with the concrete
//     types.
//   - The original generic decls are removed so the IR pipeline
//     never sees a function with TypeParams set.
//
// We also re-run `checker.Check` against the rewritten program
// at the end of the pass: the cloned functions need their
// FuncSigs entries, and any generic-call body that referenced
// the type parameters has to be re-typed in the concrete-arg
// world. The re-check uses the same checker.Info struct (so
// upstream callers keep their accumulated metadata) but with
// the monomorphic decls in place. Errors there indicate a
// type-substitution bug in the pass itself, not user error.
package monomorph

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// instKey identifies a unique (generic-name, mangled-name) pair —
// one entry per cloned monomorphic function. Declared at package
// scope so the deterministic sort helper can take it by type.
type instKey struct {
	name string
	mang string
}

// maxStructInstRounds bounds the generic-struct instantiation
// fixpoint loop. Each round discovers the instantiations one nesting
// level deeper, so the bound is the deepest legal finite nesting of a
// generic struct inside another's type argument (e.g.
// Box[Box[…Box[i32]…]]). It's set generously so realistic code
// converges well within it; exceeding it means the family is unbounded
// (polymorphic recursion), which is reported as an error. See
// docs/ADVERSARIAL-REVIEW-2026-06.md (I3).
const maxStructInstRounds = 64

// Run mutates prog in place, replacing every generic function +
// its call sites with monomorphic equivalents. After Run returns
// successfully, no FuncDecl / StructDecl in prog has non-empty
// TypeParams and no Call / StructLit has non-empty TypeArgs (the
// field is consumed by the rewrite). info is re-populated to
// reflect the new shape.
func Run(prog *ast.Program, info *checker.Info) error {
	if len(info.Generics) == 0 {
		return nil
	}

	// 1. Walk every body, rewriting Call sites that target a
	//    generic function. For each such call, mangle the callee
	//    name and record the instantiation in `instantiations`.
	instantiations := map[instKey][]ast.Type{}
	structInsts := map[instKey][]ast.Type{}

	// collectCalls rewrites every generic function call in `body` to
	// its mangled instantiation, records the instantiation, and
	// substitutes the concrete type args into the call's argument
	// expressions. Run on every original body below and on every
	// cloned body in the worklist loop — the latter is what makes a
	// generic function that calls another generic (`wrap[T]` calling
	// `id[T]`) instantiate the callee transitively.
	collectCalls := func(body *ast.Block) {
		walkBlock(body, func(c *ast.Call) {
			id, ok := c.Callee.(*ast.Ident)
			if !ok {
				return
			}
			gen, isGen := info.GenericFuncs[id.Name]
			if !isGen {
				return
			}
			if len(c.TypeArgs) != len(gen.TypeParams) {
				// Checker should have populated TypeArgs; if it
				// didn't, the call was already flagged as an
				// inference failure and we leave it alone (the
				// downstream stages will see a still-generic
				// callee and report a clearer error).
				return
			}
			if hasParamType(c.TypeArgs) {
				// Still parametric (a generic caller before its own
				// clone+substitution): leave it for the clone loop,
				// which re-runs collectCalls once the args are
				// concrete.
				return
			}
			mang := mangle(id.Name, c.TypeArgs)
			instantiations[instKey{name: id.Name, mang: mang}] = c.TypeArgs
			id.Name = mang
			// The checker may have stamped the callee's type
			// parameters onto the argument expressions (e.g. an
			// array literal passed for a `T[]` param gets ElemType=T);
			// rewrite those from the concrete TypeArgs.
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, name := range gen.TypeParams {
				sub[name] = c.TypeArgs[i]
			}
			for _, arg := range c.Args {
				substituteExpr(arg, sub)
			}
			c.TypeArgs = nil
		})
	}

	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		collectCalls(fn.Body)
		// Rewrite generic StructLits in the same body — TypeArgs
		// was stamped by the checker for every generic struct
		// literal, including those nested inside expressions.
		// Skip StructLits whose TypeArgs still contain a ParamType
		// (a type parameter from an enclosing generic function /
		// method): those get monomorphised per-clone in the
		// cloning loop below, after substituteBlock has replaced
		// each ParamType with the concrete instantiation arg.
		walkBlockStructLits(fn.Body, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 {
				return
			}
			if hasParamType(sl.TypeArgs) {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := mangle(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
	}
	// (Type-slot rewriting for generic StructType references
	// happens AFTER function cloning below, since function
	// clones get their concrete StructType[Args] from
	// substitution and need to be mangled in the same pass.)

	// 2. Generate the cloned, monomorphic FuncDecls. Walk the
	//    instantiations slice deterministically: name mangling is
	//    deterministic-by-construction, but Go's map iteration
	//    order isn't, so we sort the keys before cloning so wat
	//    emit + tests stay reproducible.
	keys := make([]instKey, 0, len(instantiations))
	for k := range instantiations {
		keys = append(keys, k)
	}
	sortKeys(keys)
	// Worklist: cloning a generic body can surface fresh
	// instantiations of any generic it calls (transitive
	// monomorphisation — `wrap[i32]`'s body calls `id[i32]`). New
	// keys are appended and drained to a fixpoint; `done` dedupes.
	done := make(map[instKey]bool, len(keys))
	var cloned []*ast.FuncDecl
	for i := 0; i < len(keys); i++ {
		k := keys[i]
		if done[k] {
			continue
		}
		done[k] = true
		gen := info.GenericFuncs[k.name]
		args := instantiations[k]
		sub := make(map[string]ast.Type, len(gen.TypeParams))
		for i, tp := range gen.TypeParams {
			sub[tp] = args[i]
		}
		c := cloneFuncDecl(gen)
		c.Name = k.mang
		c.TypeParams = nil
		for i := range c.Params {
			c.Params[i].Type = substituteType(c.Params[i].Type, sub)
		}
		c.ReturnType = substituteType(c.ReturnType, sub)
		substituteBlock(c.Body, sub)
		// The body's type parameters are now concrete, so any
		// generic call it makes (`id(x)` → `id[i32]`) can be
		// instantiated. collectCalls records the instantiation and
		// mangles the call; append any newly-seen keys to the
		// worklist.
		before := len(instantiations)
		collectCalls(c.Body)
		if len(instantiations) != before {
			// Sort before appending, for the same reason the initial
			// `keys` above is sorted: this ranges a map, so the order
			// fresh instantiations enter the worklist — and therefore
			// the order they are cloned and emitted — would otherwise
			// be Go's map iteration order. The instantiation SET is
			// unaffected (the worklist drains to a fixpoint and `done`
			// dedupes), so this is purely about reproducible output
			// (#6077).
			fresh := make([]instKey, 0, len(instantiations)-before)
			for nk := range instantiations {
				if !done[nk] {
					fresh = append(fresh, nk)
				}
			}
			sortKeys(fresh)
			keys = append(keys, fresh...)
		}
		// Walk the substituted body's StructLits a second time
		// to mangle any whose TypeArgs got substituted to concrete
		// types just now (the pre-clone walk above skips ParamType-
		// bearing TypeArgs since the substitution hasn't run yet
		// at that point). Generic methods that build a Box[T] in
		// their body need this so the resulting mangled name
		// ("Box__i32") matches the cloned struct decl, not the
		// pre-substitution placeholder.
		walkBlockStructLits(c.Body, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 {
				return
			}
			if hasParamType(sl.TypeArgs) {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := mangle(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
		cloned = append(cloned, c)
	}

	// 3. Same shape for generic structs: clone per-instantiation
	//    with substituted field types.
	structKeys := make([]instKey, 0, len(structInsts))
	for k := range structInsts {
		structKeys = append(structKeys, k)
	}
	sortKeys(structKeys)
	var clonedStructs []*ast.StructDecl
	for _, k := range structKeys {
		gen := info.GenericStructs[k.name]
		args := structInsts[k]
		sub := make(map[string]ast.Type, len(gen.TypeParams))
		for i, tp := range gen.TypeParams {
			sub[tp] = args[i]
		}
		c := *gen
		c.Name = k.mang
		c.TypeParams = nil
		c.Fields = make([]ast.Param, len(gen.Fields))
		for i, f := range gen.Fields {
			c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
		}
		clonedStructs = append(clonedStructs, &c)
	}

	// 4. Drop the original generic decls + append the clones. Both
	//    fn and struct decls flow through `info.Generics`, so the
	//    "is this name generic?" predicate is a single map lookup
	//    irrespective of which kind we're filtering.
	keep := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if _, isGen := info.Generics[fn.Name]; isGen {
			continue
		}
		keep = append(keep, fn)
	}
	prog.Funcs = append(keep, cloned...)
	keepStructs := prog.Structs[:0]
	for _, sd := range prog.Structs {
		if _, isGen := info.Generics[sd.Name]; isGen {
			continue
		}
		keepStructs = append(keepStructs, sd)
	}
	prog.Structs = append(keepStructs, clonedStructs...)

	// 4a-enum. Drop clone-needed generic enums (#3693); their concrete
	// `E__i32` clones are appended by the 4b worklist below. Generic enums
	// aren't in info.Generics, so this filters prog.Enums directly. A
	// bare-param generic enum (Option/Result) is kept generic.
	keepEnums := prog.Enums[:0]
	for _, ed := range prog.Enums {
		if len(ed.TypeParams) > 0 && enumNeedsClone(ed, info) {
			continue
		}
		keepEnums = append(keepEnums, ed)
	}
	prog.Enums = keepEnums

	// 4b. Now that only monomorphic decls remain in prog.Funcs /
	//     prog.Structs, walk every type slot to mangle remaining
	//     generic StructType references (`Pair[i32, string]` →
	//     `Pair__i32__string`). Each unique instantiation seen
	//     here joins structInsts so the clone-generation loop
	//     below can produce the matching StructDecl.
	//
	//     Iterate to a fixed point: cloning a struct may
	//     introduce more StructType references (a generic struct
	//     whose field type is itself a generic struct), and the
	//     newly-cloned struct's field types may need their own
	//     mangling. Two passes typically suffice but we loop
	//     until no new instantiations are discovered.
	converged := false
	for round := 0; round < maxStructInstRounds; round++ {
		before := len(structInsts)
		rewriteGenericStructTypes(prog, info, structInsts)
		// Append any structs the new pass found.
		for _, k := range collectKeys(structInsts) {
			// Enum instantiations (clone-needed generic enums, #3693) ride
			// the same `structInsts` map — instKey is type-agnostic and the
			// decl kind is recovered by lookup. Build an EnumDecl clone for
			// an enum key, a StructDecl clone otherwise.
			if ged := genericEnum(info, k.name); ged != nil {
				already := false
				for _, ed := range prog.Enums {
					if ed.Name == k.mang {
						already = true
						break
					}
				}
				if already {
					continue
				}
				args := structInsts[k]
				sub := make(map[string]ast.Type, len(ged.TypeParams))
				for i, tp := range ged.TypeParams {
					sub[tp] = args[i]
				}
				c := *ged
				c.Name = k.mang
				c.TypeParams = nil
				c.Monomorphized = true
				c.Variants = make([]ast.EnumVariant, len(ged.Variants))
				for i, v := range ged.Variants {
					nv := v
					nv.Payloads = make([]ast.Type, len(v.Payloads))
					for j, p := range v.Payloads {
						nv.Payloads[j] = substituteType(p, sub)
					}
					c.Variants[i] = nv
				}
				prog.Enums = append(prog.Enums, &c)
				continue
			}
			already := false
			for _, sd := range prog.Structs {
				if sd.Name == k.mang {
					already = true
					break
				}
			}
			if already {
				continue
			}
			gen := info.GenericStructs[k.name]
			args := structInsts[k]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			c := *gen
			c.Name = k.mang
			c.TypeParams = nil
			c.Fields = make([]ast.Param, len(gen.Fields))
			for i, f := range gen.Fields {
				c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
			}
			prog.Structs = append(prog.Structs, &c)
		}
		if len(structInsts) == before {
			converged = true
			break
		}
	}
	if !converged {
		// The instantiation set was still growing when we hit the round
		// cap. Since each round only expands one nesting level deeper, an
		// unbounded set means a generic struct is polymorphically
		// recursive — its own instantiation demands an ever-larger type
		// argument (e.g. `struct Nest[T] { tail: Nest[Nest[T]]; }`).
		// Fern monomorphises generics, so that family is infinite and
		// can't be lowered. Report it clearly instead of letting the
		// re-check below fail with a misleading "compiler bug". See
		// docs/ADVERSARIAL-REVIEW-2026-06.md (I3).
		return fmt.Errorf("monomorph: generic struct instantiation did not terminate after %d rounds — a generic type appears to be infinitely (polymorphically) recursive, e.g. a field typed `T[T]`-style that nests the struct inside its own type argument; such types can't be monomorphised", maxStructInstRounds)
	}

	// 4b-bis. Instantiate parametric-impl methods reached ONLY through a
	//     trait bound. The call-driven worklist (step 2) clones a parametric
	//     method (`impl[T] Iterator[T] for ArrayIter[T]`) only when a direct
	//     concrete call site exists (`x.next()` on a known `ArrayIter[i32]`).
	//     A method called solely on a bound type parameter inside a generic
	//     combinator (`it.next()` in `sum[I: Iterator[i32]]`) has no such site
	//     — at first check it dispatches through the bound, not a mangled name
	//     — so it is never enqueued, and the post-monomorph re-check then can't
	//     resolve `next` on the concrete `ArrayIter__i32`. Bridge the gap: for
	//     every concrete instantiation of a parametric impl's `for` type, clone
	//     the impl's methods under the receiver-dispatch name
	//     (`__method_ArrayIter__i32_next`). Loop to a fixpoint with struct +
	//     function instantiation, since a cloned body may build further generic
	//     structs or call further generics.
	existingFunc := func(name string) bool {
		for _, fn := range prog.Funcs {
			if fn.Name == name {
				return true
			}
		}
		return false
	}
	existingStruct := func(prog *ast.Program, name string) bool {
		for _, sd := range prog.Structs {
			if sd.Name == name {
				return true
			}
		}
		return false
	}
	mangleBodyStructLits := func(b *ast.Block) {
		walkBlockStructLits(b, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 || hasParamType(sl.TypeArgs) {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := mangle(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
	}
	methodDone := map[instKey]bool{}
	implDone := map[string]bool{}
	for mround := 0; mround < maxStructInstRounds; mround++ {
		progressed := false
		// (a) Clone parametric-impl methods for each concrete instantiation of
		//     their `for` type.
		var methodKeys []instKey
		methodArgs := map[instKey][]ast.Type{}
		methodRecv := map[instKey]string{}
		methodSimple := map[instKey]string{}
		var newImpls []*ast.ImplDecl
		for _, impl := range prog.Impls {
			if len(impl.TypeParams) == 0 {
				continue
			}
			baseName, ok := baseTypeName(impl.Type)
			if !ok {
				continue
			}
			pset := map[string]bool{}
			for _, tp := range impl.TypeParams {
				pset[tp] = true
			}
			for _, sk := range collectKeys(structInsts) {
				if sk.name != baseName {
					continue
				}
				psub := map[string]ast.Type{}
				if !unifyImplType(impl.Type, ast.StructType{Name: baseName, Args: structInsts[sk]}, pset, psub) {
					continue
				}
				// Synthesise a CONCRETE impl for this instantiation
				// (`impl Iterator[i32] for ArrayIter__i32`) so the re-check
				// records the conformance: trait-method dispatch
				// (methodImplementsTrait) needs `ArrayIter__i32` to be a known
				// implementor, which the dropped generic impl no longer
				// provides. TypeParams cleared → survives the keepImpls filter.
				implK := impl.Trait + "/" + sk.mang
				if !implDone[implK] {
					implDone[implK] = true
					ci := *impl
					ci.TypeParams = nil
					ci.Bounds = nil
					if _, isEnum := impl.Type.(ast.EnumType); isEnum {
						ci.Type = ast.EnumType{Name: sk.mang}
					} else {
						ci.Type = ast.StructType{Name: sk.mang}
					}
					if len(impl.TraitArgs) > 0 {
						ta := make([]ast.Type, len(impl.TraitArgs))
						for k := range impl.TraitArgs {
							ta[k] = substTypeByName(impl.TraitArgs[k], psub)
						}
						ci.TraitArgs = ta
					}
					if len(impl.AssocTypeBindings) > 0 {
						ab := make(map[string]ast.Type, len(impl.AssocTypeBindings))
						for name, t := range impl.AssocTypeBindings {
							ab[name] = substTypeByName(t, psub)
						}
						ci.AssocTypeBindings = ab
					}
					newImpls = append(newImpls, &ci)
					progressed = true
				}
				for _, mn := range impl.MethodNames {
					prefix := "__method_"
					genName := prefix + baseName + "_" + mn
					gen, isGen := info.GenericFuncs[genName]
					if !isGen {
						prefix = "__assoc_"
						genName = prefix + baseName + "_" + mn
						gen, isGen = info.GenericFuncs[genName]
					}
					if !isGen {
						continue
					}
					margs := make([]ast.Type, len(gen.TypeParams))
					complete := true
					for i, tp := range gen.TypeParams {
						if v, found := psub[tp]; found {
							margs[i] = v
						} else {
							complete = false
						}
					}
					if !complete || hasParamType(margs) {
						continue
					}
					mk := instKey{name: genName, mang: prefix + sk.mang + "_" + mn}
					if methodDone[mk] {
						continue
					}
					methodDone[mk] = true
					methodKeys = append(methodKeys, mk)
					methodArgs[mk] = margs
					methodRecv[mk] = sk.mang
					methodSimple[mk] = mn
				}
			}
		}
		prog.Impls = append(prog.Impls, newImpls...)
		sortKeys(methodKeys)
		for _, mk := range methodKeys {
			gen := info.GenericFuncs[mk.name]
			margs := methodArgs[mk]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = margs[i]
			}
			mc := cloneFuncDecl(gen)
			mc.Name = mk.mang
			mc.TypeParams = nil
			// Re-point the method identity at the CONCRETE receiver type so
			// the post-monomorph re-check registers it as a method of
			// `ArrayIter__i32` (not the generic `ArrayIter`). The re-check
			// re-registers from MethodRecv + "." + MethodSimpleName.
			mc.MethodRecv = methodRecv[mk]
			mc.MethodSimpleName = methodSimple[mk]
			for i := range mc.Params {
				mc.Params[i].Type = substituteType(mc.Params[i].Type, sub)
			}
			mc.ReturnType = substituteType(mc.ReturnType, sub)
			substituteBlock(mc.Body, sub)
			collectCalls(mc.Body)
			mangleBodyStructLits(mc.Body)
			prog.Funcs = append(prog.Funcs, mc)
			progressed = true
		}
		// (b) Drain any generic FUNCTION calls those bodies introduced (a
		//     parametric method that calls a generic free function).
		for _, fk := range collectKeys(instantiations) {
			if existingFunc(fk.mang) {
				continue
			}
			gen, isGen := info.GenericFuncs[fk.name]
			if !isGen {
				continue
			}
			args := instantiations[fk]
			if len(args) != len(gen.TypeParams) || hasParamType(args) {
				continue
			}
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			fc := cloneFuncDecl(gen)
			fc.Name = fk.mang
			fc.TypeParams = nil
			for i := range fc.Params {
				fc.Params[i].Type = substituteType(fc.Params[i].Type, sub)
			}
			fc.ReturnType = substituteType(fc.ReturnType, sub)
			substituteBlock(fc.Body, sub)
			collectCalls(fc.Body)
			mangleBodyStructLits(fc.Body)
			prog.Funcs = append(prog.Funcs, fc)
			progressed = true
		}
		// (c) Clone any new structs the bodies referenced + mangle remaining
		//     generic StructType slots (including the just-cloned funcs').
		beforeStructs := len(structInsts)
		rewriteGenericStructTypes(prog, info, structInsts)
		for _, k := range collectKeys(structInsts) {
			// Enum instantiations (clone-needed generic enums, #3693) ride
			// the same `structInsts` map. A later fixpoint pass can surface
			// a fresh enum key — e.g. a self-referential generic enum whose
			// variant payload is a function returning the enum itself
			// (`Wait(i32, (i32) => Step[T])`, std/async) — so this
			// loop must build the enum clone too, exactly like the first
			// worklist loop above; otherwise the GenericStructs lookup is
			// nil and the clone is dropped (panic / dangling generic enum).
			if ged := genericEnum(info, k.name); ged != nil {
				already := false
				for _, ed := range prog.Enums {
					if ed.Name == k.mang {
						already = true
						break
					}
				}
				if already {
					continue
				}
				args := structInsts[k]
				sub := make(map[string]ast.Type, len(ged.TypeParams))
				for i, tp := range ged.TypeParams {
					sub[tp] = args[i]
				}
				c := *ged
				c.Name = k.mang
				c.TypeParams = nil
				c.Monomorphized = true
				c.Variants = make([]ast.EnumVariant, len(ged.Variants))
				for i, v := range ged.Variants {
					nv := v
					nv.Payloads = make([]ast.Type, len(v.Payloads))
					for j, p := range v.Payloads {
						nv.Payloads[j] = substituteType(p, sub)
					}
					c.Variants[i] = nv
				}
				prog.Enums = append(prog.Enums, &c)
				progressed = true
				continue
			}
			if existingStruct(prog, k.mang) {
				continue
			}
			gen := info.GenericStructs[k.name]
			if gen == nil {
				continue
			}
			args := structInsts[k]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			c := *gen
			c.Name = k.mang
			c.TypeParams = nil
			c.Fields = make([]ast.Param, len(gen.Fields))
			for i, f := range gen.Fields {
				c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
			}
			prog.Structs = append(prog.Structs, &c)
			progressed = true
		}
		if !progressed && len(structInsts) == beforeStructs {
			break
		}
	}

	// 4c. Drop parametric impls (`impl[T] Trait for Box[T]`). Their
	//     conformance + coherence were validated on the first check;
	//     their methods have now been cloned per instantiation. The
	//     ImplDecl still points at the generic `Box[T]`, whose
	//     StructDecl was just removed above — so a re-check would
	//     report a spurious orphan / missing-type error against a
	//     type that no longer exists. A plain (non-parametric) impl
	//     stays: its concrete type survives and the re-check
	//     re-validates it unchanged. See docs/TRAITS.md.
	keepImpls := prog.Impls[:0]
	for _, impl := range prog.Impls {
		if len(impl.TypeParams) > 0 {
			continue
		}
		keepImpls = append(keepImpls, impl)
	}
	prog.Impls = keepImpls

	// 4d. Re-qualify variant references whose enum was cloned (#3693). The
	//     first check stamped EnumName = "E" (the generic enum) on each
	//     variant construction; that enum's decl was just dropped in favour
	//     of concrete `E__i32` clones, so the stale qualifier now names a
	//     missing enum. Clear it and let the re-check's bare-name
	//     resolveVariant bind to the clone (unique per variant name for a
	//     singly-instantiated enum). Covers a Call callee / value Ident and
	//     the payload-less EnumLit.
	clonedEnumNames := map[string]bool{}
	for name, ed := range info.Enums {
		if len(ed.TypeParams) > 0 && enumNeedsClone(ed, info) {
			clonedEnumNames[name] = true
		}
	}
	if len(clonedEnumNames) > 0 {
		ast.WalkProgram(prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if clonedEnumNames[x.EnumName] {
					x.EnumName = ""
				}
			case *ast.EnumLit:
				if clonedEnumNames[x.EnumName] {
					x.EnumName = ""
				}
			}
			return true
		})
	}

	// 5. Re-check. The cloned functions / structs need FuncSigs /
	//    Structs entries + body type-checking with the
	//    substituted types.
	info.GenericFuncs = map[string]*ast.FuncDecl{}
	info.GenericStructs = map[string]*ast.StructDecl{}
	newInfo, err := checker.Check(prog)
	if err != nil {
		return fmt.Errorf("monomorph: re-check failed (compiler bug): %w", err)
	}
	*info = *newInfo
	return nil
}

// genericEnum returns the generic EnumDecl for `name` (TypeParams
// non-empty), or nil. Generic enums aren't recorded in info.Generics
// (only funcs + structs are), so this checks info.Enums directly.
func genericEnum(info *checker.Info, name string) *ast.EnumDecl {
	ed, ok := info.Enums[name]
	if !ok || len(ed.TypeParams) == 0 {
		return nil
	}
	return ed
}

// typeRefsGenericNominal reports whether a type tree references a
// generic struct or generic enum by a *composite* (args-bearing)
// nominal — the shape the monomorphizer mangles + drops. A bare
// ParamType (`U`) is NOT such a reference: a variant payload that is
// just `U` re-checks fine against the leniently-unified call (the
// `enum Opt[T] { Sm(T) }` case), so its enum needs no clone.
func typeRefsGenericNominal(t ast.Type, info *checker.Info) bool {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) > 0 {
			if _, isGen := info.GenericStructs[x.Name]; isGen {
				return true
			}
		}
		for _, a := range x.Args {
			if typeRefsGenericNominal(a, info) {
				return true
			}
		}
	case ast.EnumType:
		if len(x.Args) > 0 && genericEnum(info, x.Name) != nil {
			return true
		}
		for _, a := range x.Args {
			if typeRefsGenericNominal(a, info) {
				return true
			}
		}
	case ast.ArrayType:
		return typeRefsGenericNominal(x.Elem, info)
	case ast.SliceType:
		return typeRefsGenericNominal(x.Elem, info)
	case ast.TupleType:
		for _, e := range x.Elems {
			if typeRefsGenericNominal(e, info) {
				return true
			}
		}
	case *ast.FuncType:
		// A generic-nominal reference behind a FUNCTION boundary does
		// NOT force an enum clone. A function-typed variant payload —
		// e.g. the self-referential `Wait(i32, (i32) => Step[T])` of
		// std/async's `Future[T]` — re-checks fine while the enum
		// stays generic (lenient unify), the behavior before the
		// composite-payload cloning (#3733/#3693). Cloning such an enum
		// is both unnecessary and (for the self-referential / signature-
		// used shape) incompletely implemented — the clone's
		// `(i32) => Step[i32]` payload and the `Step[i32]` slots in
		// function signatures aren't rewritten to `Step__i32`, so the
		// re-check fails. Treating the function boundary as opaque keeps
		// these enums generic (working) while #3733's target — a DIRECT
		// generic-struct/enum payload like `A(Box[U])` — still clones.
		return false
	}
	return false
}

// enumNeedsClone reports whether a generic enum must be cloned per
// instantiation (#3693): when some variant payload references a
// generic struct/enum, the monomorphizer mangles + drops that nominal,
// so the enum's still-generic payload (`Box[U]`) would dangle at the
// re-check ("unknown struct type Box"). Cloning emits a concrete
// `E__i32` whose payload is the mangled `Box__i32`. A generic enum
// whose payloads are only bare type params (Option / Result) is left
// generic — it re-checks fine and cloning it would collide variant
// names across instantiations.
func enumNeedsClone(ed *ast.EnumDecl, info *checker.Info) bool {
	for _, v := range ed.Variants {
		for _, p := range v.Payloads {
			if typeRefsGenericNominal(p, info) {
				return true
			}
		}
	}
	return false
}

// mangle generates a unique function name for a given
// instantiation. Format: `<base>__<arg1>__<arg2>…`. Type names
// come from ast.Type.String() which is already
// `i32` / `f32` / `Foo` / `Foo[i32]` style. Brackets and commas
// are stripped to keep the result a single identifier the rest
// of the pipeline accepts.
func mangle(base string, args []ast.Type) string {
	var b strings.Builder
	b.WriteString(base)
	for _, a := range args {
		b.WriteString("__")
		b.WriteString(mangleArg(a))
	}
	return b.String()
}

// mangleArg renders one type argument into a symbol token.
//
// The subtlety it solves: a nested generic instantiation like
// `Box[Box[i32]]` reaches `mangle` from two directions. The
// type-slot rewriter (rewriteType) recurses inner-first, so the
// outer call sees its arg ALREADY rewritten to a flat
// `StructType{Name:"Box__i32"}` (no Args). The struct-literal path
// instead passes the arg RAW as `StructType{Name:"Box", Args:[i32]}`.
// If we mangled via `sanitize(a.String())` those two render
// differently ("Box__i32" vs "Box_i32_"), the clone names diverge,
// and the trailing re-check fails with "cannot assign Box__Box_i32_
// to Box__Box__i32".
//
// Recursing through generic nominal types (and the composite shapes
// that can contain them) makes both directions converge: a raw
// `Box[i32]` arg mangles via `mangle("Box", [i32])` → "Box__i32",
// matching the pre-flattened form's `sanitize("Box__i32")`.
func mangleArg(t ast.Type) string {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) > 0 {
			return mangle(x.Name, x.Args)
		}
	case ast.EnumType:
		if len(x.Args) > 0 {
			return mangle(x.Name, x.Args)
		}
	case ast.ArrayType:
		return mangleArg(x.Elem) + "_arr"
	case ast.SliceType:
		return mangleArg(x.Elem) + "_slice"
	case ast.TupleType:
		var b strings.Builder
		b.WriteString("tup")
		for _, e := range x.Elems {
			b.WriteString("_")
			b.WriteString(mangleArg(e))
		}
		return b.String()
	}
	return sanitize(t.String())
}

// sanitize maps a type's String() form into a token safe to embed
// in a backend symbol name. Any character outside [A-Za-z0-9_] is
// replaced with '_' — this covers the bracket/comma/space of
// generic + slice forms (`Foo[Bar]`, `i32[]`) AND the parentheses
// of a tuple type (`(i32, i32)`), whose literal `(`/`)` would
// otherwise survive into the assembler symbol and fail to
// assemble on the native backends (wasm tolerated them).
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlnum := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_'
		if !isAlnum {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func sortKeys(ks []instKey) {
	// Insertion sort — list lengths are tiny in practice (one
	// per unique generic instantiation in the program).
	for i := 1; i < len(ks); i++ {
		j := i
		for j > 0 && (ks[j-1].name > ks[j].name ||
			(ks[j-1].name == ks[j].name && ks[j-1].mang > ks[j].mang)) {
			ks[j-1], ks[j] = ks[j], ks[j-1]
			j--
		}
	}
}

// substituteType rewrites every ParamType in t to its concrete
// binding from sub. Mirrors the helper in the checker — duplicated
// here so the monomorph pass doesn't pull the checker's exported
// surface beyond what it needs.
// substTypeByName substitutes by NAME, treating a zero-arg StructType /
// EnumType as a potential type-param reference (the form an impl's trait
// args carry before name resolution rewrites them to ParamType). Used to
// concretise a parametric impl's TraitArgs / assoc bindings (`Iterator[T]`
// → `Iterator[i32]`) where substituteType — which only rewrites ParamType —
// would leave a bare `StructType{"T"}` untouched.
func substTypeByName(t ast.Type, sub map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.StructType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substTypeByName(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substTypeByName(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substTypeByName(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substTypeByName(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substTypeByName(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substTypeByName(x.Result, sub)}
		for _, p := range x.Params {
			out.Params = append(out.Params, substTypeByName(p, sub))
		}
		return out
	case ast.ProjType:
		return ast.ProjType{Base: substTypeByName(x.Base, sub), Name: x.Name}
	}
	return t
}

func substituteType(t ast.Type, sub map[string]ast.Type) ast.Type {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substituteType(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substituteType(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substituteType(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substituteType(x.Result, sub)}
		for _, p := range x.Params {
			out.Params = append(out.Params, substituteType(p, sub))
		}
		return out
	case ast.ProjType:
		// Associated-type projection: substitute inside the base
		// (`T::Item` → `IntBox::Item`); the checker re-check resolves the
		// now-concrete projection to its binding. See docs/ASSOCIATED-TYPES.md.
		return ast.ProjType{Base: substituteType(x.Base, sub), Name: x.Name}
	}
	return t
}

// substituteBlock walks the body of a cloned generic function
// and rewrites any Var declarations whose type uses the type
// parameters. Other expression types either don't carry types
// directly (the checker re-derives them) or carry types that
// don't reference the parameters (e.g. integer literals are
// fine — they don't depend on T).
func substituteBlock(b *ast.Block, sub map[string]ast.Type) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		substituteStmt(s, sub)
	}
}

func substituteStmt(s ast.Stmt, sub map[string]ast.Type) {
	switch x := s.(type) {
	case *ast.Var:
		x.Type = substituteType(x.Type, sub)
		substituteExpr(x.Init, sub)
	case *ast.Destructure:
		substituteExpr(x.Init, sub)
	case *ast.Block:
		substituteBlock(x, sub)
	case *ast.If:
		substituteExpr(x.Cond, sub)
		substituteStmt(x.Then, sub)
		if x.Else != nil {
			substituteStmt(x.Else, sub)
		}
	case *ast.IfLet:
		substituteExpr(x.Source, sub)
		// BindingTypes are concrete after the checker stamped
		// them in the original generic body; substitute so
		// per-clone they specialise to the concrete instantiation
		// of the enum / struct payload.
		for i := range x.BindingTypes {
			x.BindingTypes[i] = substituteType(x.BindingTypes[i], sub)
		}
		substituteStmt(x.Then, sub)
		if x.Else != nil {
			substituteStmt(x.Else, sub)
		}
	case *ast.LetElse:
		substituteExpr(x.Source, sub)
		for i := range x.BindingTypes {
			x.BindingTypes[i] = substituteType(x.BindingTypes[i], sub)
		}
		substituteBlock(x.Else, sub)
	case *ast.While:
		substituteExpr(x.Cond, sub)
		substituteStmt(x.Body, sub)
	case *ast.Loop:
		substituteStmt(x.Body, sub)
	case *ast.For:
		if x.Init != nil {
			substituteStmt(x.Init, sub)
		}
		substituteExpr(x.Cond, sub)
		if x.Step != nil {
			substituteStmt(x.Step, sub)
		}
		substituteStmt(x.Body, sub)
	case *ast.ExprStmt:
		substituteExpr(x.Expr, sub)
	case *ast.Return:
		substituteExpr(x.Value, sub)
	case *ast.Defer:
		substituteExpr(x.Expr, sub)
	case *ast.Match:
		substituteExpr(x.Tag, sub)
		for _, arm := range x.Arms {
			substituteExpr(arm.Guard, sub)
			substituteBlock(arm.Body, sub)
		}
	}
}

// substituteExpr walks an expression tree applying sub to every
// type-bearing node (StructLit.TypeArgs, CastExpr.Target,
// Call.TypeArgs). Doesn't touch type-free shapes — the checker
// re-derives those during the post-monomorph re-check.
// hasParamType reports whether any type in `types` is a ParamType
// (or recursively contains one). Used by the monomorpher to defer
// rewriting StructLit TypeArgs that still hold a ParamType — those
// only become concrete after substituteBlock runs over a clone.
func hasParamType(types []ast.Type) bool {
	for _, t := range types {
		if containsParamType(t) {
			return true
		}
	}
	return false
}

// baseTypeName returns the nominal name of a struct/enum type (the base of a
// possibly-generic `Foo[…]`), used to key a parametric impl's `for` type to
// its struct instantiations.
func baseTypeName(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name, true
	case ast.EnumType:
		return x.Name, true
	}
	return "", false
}

// unifyImplType matches a parametric impl's `for` pattern (`ArrayIter[T]`,
// whose param names are in `params`) against a concrete instantiation
// (`ArrayIter[i32]`), binding each impl param in `sub` (T=i32). It lets the
// method-instantiation pass recover the substitution needed to clone the
// impl's methods per concrete type. A param name may surface either as a
// ParamType or as a bare zero-arg StructType (depending on how far name
// resolution rewrote impl.Type), so both are treated as binders.
func unifyImplType(pattern, concrete ast.Type, params map[string]bool, sub map[string]ast.Type) bool {
	switch p := pattern.(type) {
	case ast.ParamType:
		if params[p.Name] {
			if ex, ok := sub[p.Name]; ok {
				return ast.Equal(ex, concrete)
			}
			sub[p.Name] = concrete
			return true
		}
		return ast.Equal(pattern, concrete)
	case ast.StructType:
		if len(p.Args) == 0 && params[p.Name] {
			if ex, ok := sub[p.Name]; ok {
				return ast.Equal(ex, concrete)
			}
			sub[p.Name] = concrete
			return true
		}
		c, ok := concrete.(ast.StructType)
		if !ok || c.Name != p.Name || len(c.Args) != len(p.Args) {
			return false
		}
		for i := range p.Args {
			if !unifyImplType(p.Args[i], c.Args[i], params, sub) {
				return false
			}
		}
		return true
	case ast.EnumType:
		c, ok := concrete.(ast.EnumType)
		if !ok || c.Name != p.Name || len(c.Args) != len(p.Args) {
			return false
		}
		for i := range p.Args {
			if !unifyImplType(p.Args[i], c.Args[i], params, sub) {
				return false
			}
		}
		return true
	case ast.ArrayType:
		c, ok := concrete.(ast.ArrayType)
		return ok && unifyImplType(p.Elem, c.Elem, params, sub)
	case ast.SliceType:
		c, ok := concrete.(ast.SliceType)
		return ok && unifyImplType(p.Elem, c.Elem, params, sub)
	case ast.TupleType:
		c, ok := concrete.(ast.TupleType)
		if !ok || len(c.Elems) != len(p.Elems) {
			return false
		}
		for i := range p.Elems {
			if !unifyImplType(p.Elems[i], c.Elems[i], params, sub) {
				return false
			}
		}
		return true
	}
	return ast.Equal(pattern, concrete)
}

func containsParamType(t ast.Type) bool {
	switch x := t.(type) {
	case ast.ParamType:
		return true
	case ast.StructType:
		return hasParamType(x.Args)
	case ast.EnumType:
		return hasParamType(x.Args)
	case ast.ArrayType:
		return containsParamType(x.Elem)
	case ast.SliceType:
		return containsParamType(x.Elem)
	case ast.TupleType:
		return hasParamType(x.Elems)
	case *ast.FuncType:
		if containsParamType(x.Result) {
			return true
		}
		return hasParamType(x.Params)
	}
	return false
}

// concreteTypeNameOf returns the receiver name a type argument resolves
// associated calls under — struct/enum names, and one name per scalar
// impl surface (`impl Default for i32` → `__assoc_i32_default`), so
// `T.f()` at `T = i32` rewrites to `i32.f()`. It is `ast.ReceiverTypeName`,
// the same mapping the checker registers those impls under. Reports false
// for a type that can't carry one (a still-parametric arg, a tuple, …).
func concreteTypeNameOf(t ast.Type) (string, bool) { return ast.ReceiverTypeName(t) }

func substituteExpr(e ast.Expr, sub map[string]ast.Type) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.StructLit:
		if len(x.TypeArgs) > 0 {
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
		}
		for _, f := range x.Fields {
			substituteExpr(f.Value, sub)
		}
	case *ast.Call:
		if len(x.TypeArgs) > 0 {
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
		}
		// Generic associated dispatch `T.f(args)`: the checker stamps the
		// call with Method.Receiver = ParamType(T) and leaves the callee a
		// FieldAccess whose target Ident *is* the type-param name (that's
		// what distinguishes it from a value-receiver `x.m()`, whose target
		// is a value). Rewrite the target to the concrete type so the
		// re-check resolves `Concrete.f()` → `__assoc_<Concrete>_f`.
		if x.Method != nil {
			if pt, ok := x.Method.Receiver.(ast.ParamType); ok {
				if fa, ok := x.Callee.(*ast.FieldAccess); ok {
					if tid, ok := fa.Target.(*ast.Ident); ok && tid.Name == pt.Name {
						if ct, ok := sub[pt.Name]; ok {
							if name, ok2 := concreteTypeNameOf(ct); ok2 {
								fa.Target = &ast.Ident{P: tid.P, Name: name}
							}
						}
					}
				}
			}
		}
		substituteExpr(x.Callee, sub)
		for _, a := range x.Args {
			substituteExpr(a, sub)
		}
	case *ast.Assign:
		// `out = out.push(x)` — the RHS (and a FieldAccess /
		// Index LHS) can hold a method-call whose stamped
		// TypeArgs reference the enclosing generic's params. Without
		// walking it, the cloned body keeps `push`'s TypeArgs as
		// `[T]`, the post-monomorph re-check substitutes the method
		// signature by `T→T` (a no-op), and the abstract `T[]`
		// expected type mismatches the concrete element type.
		substituteExpr(x.Target, sub)
		substituteExpr(x.Value, sub)
	case *ast.FString:
		for i := range x.Parts {
			substituteExpr(x.Parts[i].Expr, sub)
		}
		substituteExpr(x.Desugared, sub)
	case *ast.MapLit:
		x.KeyType = substituteType(x.KeyType, sub)
		x.ValueType = substituteType(x.ValueType, sub)
		for i := range x.Entries {
			substituteExpr(x.Entries[i].Key, sub)
			substituteExpr(x.Entries[i].Value, sub)
		}
	case *ast.EnumLit:
		for _, a := range x.Args {
			substituteExpr(a, sub)
		}
	case *ast.CastExpr:
		x.Target = substituteType(x.Target, sub)
		substituteExpr(x.Inner, sub)
	case *ast.DowncastExpr:
		x.Target = substituteType(x.Target, sub)
		substituteExpr(x.Inner, sub)
	case *ast.Binary:
		substituteExpr(x.Left, sub)
		substituteExpr(x.Right, sub)
	case *ast.Unary:
		substituteExpr(x.Operand, sub)
	case *ast.Index:
		substituteExpr(x.Array, sub)
		substituteExpr(x.Idx, sub)
	case *ast.SliceExpr:
		substituteExpr(x.Source, sub)
		substituteExpr(x.Low, sub)
		substituteExpr(x.High, sub)
	case *ast.FieldAccess:
		substituteExpr(x.Target, sub)
	case *ast.TryOp:
		x.Type = substituteType(x.Type, sub)
		substituteExpr(x.Inner, sub)
	case *ast.IfExpr:
		substituteExpr(x.Cond, sub)
		substituteExpr(x.Then, sub)
		substituteExpr(x.Else, sub)
	case *ast.MatchExpr:
		substituteExpr(x.Tag, sub)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				substituteExpr(arm.Guard, sub)
			}
			substituteExpr(arm.Body, sub)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			substituteStmt(st, sub)
		}
		if x.Tail != nil {
			substituteExpr(x.Tail, sub)
		}
	case *ast.ArrayLit:
		// Substitute the literal's element-type annotation too — it
		// drives the per-element store width at codegen, so leaving a
		// ParamType here built a `string[]` / pointer-element array
		// with single-word stores into two-word slots (the len word
		// stayed uninitialised → corruption on drop).
		x.ElemType = substituteType(x.ElemType, sub)
		for _, e := range x.Elems {
			substituteExpr(e, sub)
		}
	case *ast.TupleLit:
		for _, e := range x.Elems {
			substituteExpr(e, sub)
		}
	case *ast.Lambda:
		// Lambda's params + return type may reference the enclosing
		// generic's type parameters. Without substitution the
		// monomorphised body sees `i32`-typed exprs returning a
		// `(T) => T` lambda — re-check mismatches and errors with
		// `return type mismatch`. Walk the params slice in place
		// so the lambda's checker-stamped FuncType (which it
		// reads back via b.exprType later) reflects the
		// monomorphised types.
		for i := range x.Params {
			x.Params[i].Type = substituteType(x.Params[i].Type, sub)
		}
		x.ReturnType = substituteType(x.ReturnType, sub)
		for i := range x.Captures {
			x.Captures[i].Type = substituteType(x.Captures[i].Type, sub)
		}
		substituteBlock(x.Body, sub)
	}
}

// cloneFuncDecl produces a deep copy of fn suitable for
// post-substitution mutation. Body is structure-cloned so
// substituteBlock's in-place mutation doesn't leak into the
// generic source decl.
func cloneFuncDecl(fn *ast.FuncDecl) *ast.FuncDecl {
	c := *fn
	// An instantiation drops the generic's type params (it's concrete now);
	// the rest of the deep copy is the shared ast cloner.
	c.TypeParams = nil
	c.Params = append([]ast.Param(nil), fn.Params...)
	c.Body = ast.CloneBlock(fn.Body)
	return &c
}

// walkBlockStructLits is the StructLit analogue of walkBlock —
// invokes fn on every StructLit reachable from the body so the
// monomorpher can rewrite generic instantiations regardless of
// where they nest (struct literal inside a tuple inside a
// variable initialiser, etc).
func walkBlockStructLits(b *ast.Block, fn func(*ast.StructLit)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmtStructLits(s, fn)
	}
}

func walkStmtStructLits(s ast.Stmt, fn func(*ast.StructLit)) {
	switch x := s.(type) {
	case *ast.Var:
		walkExprStructLits(x.Init, fn)
	case *ast.Destructure:
		walkExprStructLits(x.Init, fn)
	case *ast.ExprStmt:
		walkExprStructLits(x.Expr, fn)
	case *ast.Return:
		walkExprStructLits(x.Value, fn)
	case *ast.If:
		walkExprStructLits(x.Cond, fn)
		walkStmtStructLits(x.Then, fn)
		if x.Else != nil {
			walkStmtStructLits(x.Else, fn)
		}
	case *ast.IfLet:
		walkExprStructLits(x.Source, fn)
		walkStmtStructLits(x.Then, fn)
		if x.Else != nil {
			walkStmtStructLits(x.Else, fn)
		}
	case *ast.LetElse:
		walkExprStructLits(x.Source, fn)
		walkBlockStructLits(x.Else, fn)
	case *ast.While:
		walkExprStructLits(x.Cond, fn)
		walkStmtStructLits(x.Body, fn)
	case *ast.Loop:
		walkStmtStructLits(x.Body, fn)
	case *ast.For:
		if x.Init != nil {
			walkStmtStructLits(x.Init, fn)
		}
		if x.Cond != nil {
			walkExprStructLits(x.Cond, fn)
		}
		if x.Step != nil {
			walkStmtStructLits(x.Step, fn)
		}
		walkStmtStructLits(x.Body, fn)
	case *ast.Block:
		walkBlockStructLits(x, fn)
	case *ast.Match:
		walkExprStructLits(x.Tag, fn)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExprStructLits(arm.Guard, fn)
			}
			walkBlockStructLits(arm.Body, fn)
		}
	}
}

func walkExprStructLits(e ast.Expr, fn func(*ast.StructLit)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.StructLit:
		fn(x)
		for _, f := range x.Fields {
			walkExprStructLits(f.Value, fn)
		}
	case *ast.Call:
		walkExprStructLits(x.Callee, fn)
		for _, a := range x.Args {
			walkExprStructLits(a, fn)
		}
	case *ast.Binary:
		walkExprStructLits(x.Left, fn)
		walkExprStructLits(x.Right, fn)
	case *ast.Unary:
		walkExprStructLits(x.Operand, fn)
	case *ast.Index:
		walkExprStructLits(x.Array, fn)
		walkExprStructLits(x.Idx, fn)
	case *ast.SliceExpr:
		walkExprStructLits(x.Source, fn)
		walkExprStructLits(x.Low, fn)
		walkExprStructLits(x.High, fn)
	case *ast.FieldAccess:
		walkExprStructLits(x.Target, fn)
	case *ast.TryOp:
		walkExprStructLits(x.Inner, fn)
	case *ast.IfExpr:
		walkExprStructLits(x.Cond, fn)
		walkExprStructLits(x.Then, fn)
		walkExprStructLits(x.Else, fn)
	case *ast.MatchExpr:
		walkExprStructLits(x.Tag, fn)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExprStructLits(arm.Guard, fn)
			}
			walkExprStructLits(arm.Body, fn)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			walkStmtStructLits(st, fn)
		}
		if x.Tail != nil {
			walkExprStructLits(x.Tail, fn)
		}
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			walkExprStructLits(e, fn)
		}
	case *ast.TupleLit:
		for _, e := range x.Elems {
			walkExprStructLits(e, fn)
		}
	case *ast.CastExpr:
		walkExprStructLits(x.Inner, fn)
	case *ast.DowncastExpr:
		walkExprStructLits(x.Inner, fn)
	}
}

// rewriteGenericStructTypes walks every Type slot in the program
// (function params + return types, var declarations, struct
// field types) and rewrites StructType references with non-empty
// Args into the mangled flat name — recording each unique
// instantiation in `into` so the clone-generation step below
// emits exactly one StructDecl per (name, args) pair.
func rewriteGenericStructTypes(prog *ast.Program, info *checker.Info, into map[instKey][]ast.Type) {
	rewrite := func(slot *ast.Type) {
		if slot == nil {
			return
		}
		*slot = rewriteType(*slot, info, into)
	}
	// Functions: receivers, params, return types, var decls.
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			rewrite(&fn.Receiver.Type)
		}
		for i := range fn.Params {
			rewrite(&fn.Params[i].Type)
		}
		rewrite(&fn.ReturnType)
		rewriteBlockTypes(fn.Body, info, into)
	}
	// Structs: field types of NON-GENERIC structs (generic ones
	// are about to be dropped). Field types of cloned structs
	// were already substituted during cloning above.
	for _, sd := range prog.Structs {
		if _, isGen := info.GenericStructs[sd.Name]; isGen {
			continue
		}
		for i := range sd.Fields {
			rewrite(&sd.Fields[i].Type)
		}
	}
	// Enums: variant payload types of concrete enums (TypeParams empty) —
	// the cloned `E__i32` from #3693 has its payload substituted to
	// `Box[i32]` during cloning, which this pass mangles to `Box__i32`
	// (and records the struct instantiation so its decl gets built). A
	// still-generic enum (Option/Result) is skipped: its `T` payloads are
	// substituted per use, not here.
	for _, ed := range prog.Enums {
		if len(ed.TypeParams) > 0 {
			continue
		}
		for vi := range ed.Variants {
			for pi := range ed.Variants[vi].Payloads {
				rewrite(&ed.Variants[vi].Payloads[pi])
			}
		}
	}
}

// collectKeys returns a deterministic-order key list for the
// fixed-point loop below — same insertion-sort the function
// instantiation step uses, kept inline so the wat output stays
// reproducible across runs.
func collectKeys(m map[instKey][]ast.Type) []instKey {
	out := make([]instKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortKeys(out)
	return out
}

// rewriteType rewrites a single Type tree in place: any
// StructType whose Name appears in info.GenericStructs and whose
// Args is populated gets flattened to a mangled StructType. The
// mangled name lookup populates `into` so the caller can build
// clones for every unique instantiation.
func rewriteType(t ast.Type, info *checker.Info, into map[instKey][]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		// Recursively rewrite nested args first — a generic
		// struct's args may themselves reference other generic
		// structs.
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteType(x.Args[i], info, into)
		}
		if _, isGen := info.GenericStructs[x.Name]; !isGen {
			return ast.StructType{Name: x.Name, Args: args}
		}
		mang := mangle(x.Name, args)
		into[instKey{name: x.Name, mang: mang}] = args
		return ast.StructType{Name: mang}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		// Recursively rewrite nested args first (a generic enum's args
		// may reference other generic nominals).
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteType(x.Args[i], info, into)
		}
		// A clone-needed generic enum (composite payload, #3693) flattens
		// to a mangled, concrete `E__i32` whose decl the clone loop builds
		// from `into`. A bare-param generic enum (Option/Result) stays
		// generic — it re-checks fine and must keep its single decl so its
		// variant names don't collide across instantiations.
		if ed := genericEnum(info, x.Name); ed != nil && enumNeedsClone(ed, info) {
			mang := mangle(x.Name, args)
			into[instKey{name: x.Name, mang: mang}] = args
			return ast.EnumType{Name: mang}
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: rewriteType(x.Elem, info, into)}
	case ast.SliceType:
		return ast.SliceType{Elem: rewriteType(x.Elem, info, into)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = rewriteType(x.Elems[i], info, into)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: rewriteType(x.Result, info, into)}
		for _, p := range x.Params {
			out.Params = append(out.Params, rewriteType(p, info, into))
		}
		return out
	}
	return t
}

func rewriteBlockTypes(b *ast.Block, info *checker.Info, into map[instKey][]ast.Type) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		rewriteStmtTypes(s, info, into)
	}
}

func rewriteStmtTypes(s ast.Stmt, info *checker.Info, into map[instKey][]ast.Type) {
	switch x := s.(type) {
	case *ast.Var:
		if x.Type != nil {
			x.Type = rewriteType(x.Type, info, into)
		}
	case *ast.Destructure:
		// No types stored on the node itself — element types
		// flow from the synthesised temp `*ast.Var` in
		// info.Locals, which the existing Var case handles.
	case *ast.Block:
		rewriteBlockTypes(x, info, into)
	case *ast.If:
		rewriteStmtTypes(x.Then, info, into)
		if x.Else != nil {
			rewriteStmtTypes(x.Else, info, into)
		}
	case *ast.IfLet:
		for i := range x.BindingTypes {
			x.BindingTypes[i] = rewriteType(x.BindingTypes[i], info, into)
		}
		rewriteStmtTypes(x.Then, info, into)
		if x.Else != nil {
			rewriteStmtTypes(x.Else, info, into)
		}
	case *ast.LetElse:
		for i := range x.BindingTypes {
			x.BindingTypes[i] = rewriteType(x.BindingTypes[i], info, into)
		}
		rewriteBlockTypes(x.Else, info, into)
	case *ast.While:
		rewriteStmtTypes(x.Body, info, into)
	case *ast.Loop:
		rewriteStmtTypes(x.Body, info, into)
	case *ast.For:
		if x.Init != nil {
			rewriteStmtTypes(x.Init, info, into)
		}
		rewriteStmtTypes(x.Body, info, into)
	case *ast.Match:
		for _, arm := range x.Arms {
			rewriteBlockTypes(arm.Body, info, into)
		}
	}
}

// walkBlock invokes fn on every Call expression reachable from
// the block. Generic function call sites — the only thing the
// monomorph pass cares about — are necessarily Call nodes, so we
// don't need to recurse into other expression shapes that don't
// hold Call children.
func walkBlock(b *ast.Block, fn func(*ast.Call)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmt(s, fn)
	}
}

func walkStmt(s ast.Stmt, fn func(*ast.Call)) {
	switch x := s.(type) {
	case *ast.Var:
		walkExpr(x.Init, fn)
	case *ast.Destructure:
		walkExpr(x.Init, fn)
	case *ast.ExprStmt:
		walkExpr(x.Expr, fn)
	case *ast.Return:
		walkExpr(x.Value, fn)
	case *ast.If:
		walkExpr(x.Cond, fn)
		walkStmt(x.Then, fn)
		if x.Else != nil {
			walkStmt(x.Else, fn)
		}
	case *ast.IfLet:
		walkExpr(x.Source, fn)
		walkStmt(x.Then, fn)
		if x.Else != nil {
			walkStmt(x.Else, fn)
		}
	case *ast.LetElse:
		walkExpr(x.Source, fn)
		walkBlock(x.Else, fn)
	case *ast.While:
		walkExpr(x.Cond, fn)
		walkStmt(x.Body, fn)
	case *ast.Loop:
		walkStmt(x.Body, fn)
	case *ast.For:
		if x.Init != nil {
			walkStmt(x.Init, fn)
		}
		if x.Cond != nil {
			walkExpr(x.Cond, fn)
		}
		if x.Step != nil {
			walkStmt(x.Step, fn)
		}
		walkStmt(x.Body, fn)
	case *ast.Block:
		walkBlock(x, fn)
	case *ast.Match:
		walkExpr(x.Tag, fn)
		for _, arm := range x.Arms {
			walkStmt(arm.Body, fn)
		}
	case *ast.FuncDecl:
		// Nested function declarations (`function f() { ... }`
		// as a stmt — IsLocal=true). The body can contain
		// generic calls that need rewriting just like a
		// top-level decl's body. Without this case, an
		// `id(x)` / `pick(...)` inside a local fn survives
		// past the rewrite step and the monomorph re-check
		// fails with "undefined identifier".
		walkBlock(x.Body, fn)
	case *ast.Defer:
		walkExpr(x.Expr, fn)
	}
}

func walkExpr(e ast.Expr, fn func(*ast.Call)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.Call:
		fn(x)
		walkExpr(x.Callee, fn)
		for _, a := range x.Args {
			walkExpr(a, fn)
		}
	case *ast.Binary:
		walkExpr(x.Left, fn)
		walkExpr(x.Right, fn)
	case *ast.Unary:
		walkExpr(x.Operand, fn)
	case *ast.Index:
		walkExpr(x.Array, fn)
		walkExpr(x.Idx, fn)
	case *ast.SliceExpr:
		walkExpr(x.Source, fn)
		walkExpr(x.Low, fn)
		walkExpr(x.High, fn)
	case *ast.FieldAccess:
		walkExpr(x.Target, fn)
	case *ast.TryOp:
		walkExpr(x.Inner, fn)
	case *ast.IfExpr:
		walkExpr(x.Cond, fn)
		walkExpr(x.Then, fn)
		walkExpr(x.Else, fn)
	case *ast.MatchExpr:
		walkExpr(x.Tag, fn)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, fn)
			}
			walkExpr(arm.Body, fn)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			walkStmt(st, fn)
		}
		if x.Tail != nil {
			walkExpr(x.Tail, fn)
		}
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			walkExpr(e, fn)
		}
	case *ast.TupleLit:
		for _, e := range x.Elems {
			walkExpr(e, fn)
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, fn)
		}
	case *ast.MapLit:
		// Map literals carry arbitrary expressions for both
		// keys and values; either slot can host a generic
		// call (`Map { id(k): v, k2: pick(c, a, b) }`). Without
		// this case the rewriter leaves the inner call's
		// callee Ident pointing at the (about-to-be-dropped)
		// generic decl, and the post-monomorph re-check fails
		// with "undefined identifier".
		for _, ent := range x.Entries {
			walkExpr(ent.Key, fn)
			walkExpr(ent.Value, fn)
		}
	case *ast.FString:
		// F-string interpolants — `f"...{id(x)}..."`. Each
		// FStringPart with a non-nil Expr is a sub-expression
		// that can itself contain generic calls.
		for _, p := range x.Parts {
			if p.Expr != nil {
				walkExpr(p.Expr, fn)
			}
		}
	case *ast.Assign:
		walkExpr(x.Target, fn)
		walkExpr(x.Value, fn)
	case *ast.Lambda:
		// Lambda expression — `function (...) { ... }` in
		// expression position. The body is a Block that can
		// host generic calls just like a top-level decl's
		// body.
		walkBlock(x.Body, fn)
	case *ast.CastExpr:
		walkExpr(x.Inner, fn)
	case *ast.DowncastExpr:
		walkExpr(x.Inner, fn)
	}
}
