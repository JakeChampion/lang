// Dead-function elimination at the IR level.
//
// Treeshake runs at the AST level before lowering, so it removes
// helpers no user code references. After IR-level inlining +
// defunctionalisation + closure-pair elision, more functions
// become unreferenced — the inliner spliced their bodies into
// every caller, the defunctionaliser rewrote indirect dispatches
// to direct calls (potentially leaving the original closure
// target as the sole reference, then losing that too once the
// hoisted body got inlined too). This pass picks up those
// orphans.
//
// Reachability:
//   - main, handle (entry points the wasm exports) and any
//     names listed in `keepAlive` (PrintMainResult's
//     `int_to_string`, etc.) are roots.
//   - From each reachable function's body, every OpCallDirect /
//     OpCallClosureDirect / OpMakeClosure / OpMakeEnv /
//     OpConstFunc keeps its target alive.
//   - Codegen-level call-name aliases (`map_new` →
//     `map_new_impl`, `__array_append_jsonvalue` →
//     `__array_append_string`, etc.) are followed via the
//     `aliases` map so the impl bodies survive when only the
//     IR-side name is referenced.
//   - Iterate to fixpoint: a freshly-marked function may itself
//     reference more functions.
//
// LiveFunctions returns the reachable set; the caller is
// responsible for filtering the IR program in a way that
// preserves any AST-side bookkeeping (the wasm codegen's
// scan-for-uses passes still walk the original AST, so
// callers should NOT trim prog.Funcs in lockstep — only the
// IR's emission is gated, codegen iterates ip.Funcs and looks
// up matching FuncDecls by name).

package ir

// LiveFunctions is shorthand for LiveFunctionsWithAliases with
// no codegen aliases.
func LiveFunctions(prog *Program, keepAlive ...string) map[string]bool {
	return LiveFunctionsWithAliases(prog, nil, keepAlive...)
}

// LiveFunctionsWithAliases returns the set of function names
// reachable from the program's entry points (main / handle)
// plus the `keepAlive` extras, following OpCallDirect /
// OpCallClosureDirect / OpMakeClosure / OpMakeEnv /
// OpConstFunc references transitively. `aliases` carries
// codegen-level rewrites the IR walker can't see (e.g. wasm's
// `map_new → map_new_impl` emit-time substitution): when a
// live name has an entry in the map, its alias target is
// enqueued too.
//
// Returns nil when no entry point is present (rare —
// typically only unit-test programs); the caller should treat
// that as "keep everything".
func LiveFunctionsWithAliases(prog *Program, aliases map[string]string, keepAlive ...string) map[string]bool {
	if len(prog.Funcs) == 0 {
		return nil
	}
	byName := map[string]*Func{}
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}

	reached := map[string]bool{}
	var queue []string
	var enqueue func(name string)
	enqueue = func(name string) {
		if name == "" || reached[name] {
			return
		}
		// Only enqueue names that actually exist in the
		// program. Codegen-level aliases (see `aliases`) may
		// refer to absent names — those are no-ops for the
		// reachability map.
		if _, ok := byName[name]; ok {
			reached[name] = true
			queue = append(queue, name)
		}
		// Whether or not the name is in byName, follow any
		// codegen alias it has — the alias target is what
		// actually survives the rewrite at emit time.
		if dst, hasAlias := aliases[name]; hasAlias {
			enqueue(dst)
		}
	}
	enqueue("main")
	enqueue("handle")
	for _, name := range keepAlive {
		enqueue(name)
	}
	if !reached["main"] && !reached["handle"] {
		return nil
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		fn := byName[name]
		if fn == nil {
			continue
		}
		for _, op := range fn.Ops {
			switch op.Kind {
			case OpCallDirect, OpCallClosureDirect, OpMakeClosure, OpMakeEnv, OpConstFunc:
				enqueue(op.Str)
			}
		}
	}
	return reached
}
