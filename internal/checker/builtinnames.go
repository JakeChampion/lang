package checker

// BuiltinNames returns the predicate internal/effects needs to classify a
// call: a name is a runtime builtin when the checker registered a signature
// for it. Everything else an `*ast.Ident` callee can name is either a
// declared function or a value holding one, and the call-graph builder tells
// those apart by looking the name up among the program's declarations.
func (i *Info) BuiltinNames() func(name string) bool {
	return func(name string) bool {
		_, ok := i.FuncSigs[name]
		return ok
	}
}
