package componenttype

// classify.go is P2 slice 3 of bring-your-own WIT
// (docs/WIT-BRING-YOUR-OWN.md): derive each imported function's lowering kind
// from its resolved signature, via the canonical-ABI flattening rules —
// reproducing the kinds the composer currently hard-codes (gNoOpt / gMem /
// gMemRealloc) so a WIT-driven path can replace the registry.
//
// Rules (for a guest→host lowered import):
//   - realloc is needed iff a RESULT carries heap data (a list or string,
//     possibly nested) — the host allocates it in guest memory on return.
//   - memory is needed iff realloc is, OR any PARAM carries heap data, OR the
//     result is returned indirectly (its flattened core value count exceeds
//     the single allowed flat result, so it spills through a return pointer).
//   - otherwise the call is scalar in/out: no-opt.

// LowerKind is an import's canonical-ABI lowering kind. The values match the
// suffix engine's import kinds (0 no-opt, 1 mem, 2 mem+realloc); resource.drop
// is synthesized by the composer, not derived from a world, so it has no entry
// here.
type LowerKind int

const (
	KindNoOpt LowerKind = iota
	KindMem
	KindMemRealloc
)

func (k LowerKind) String() string {
	switch k {
	case KindNoOpt:
		return "no-opt"
	case KindMem:
		return "mem"
	case KindMemRealloc:
		return "mem+realloc"
	default:
		return "?"
	}
}

// maxFlatResults is the canonical-ABI cap on a lowered function's flattened
// core results before the result is returned indirectly through memory.
const maxFlatResults = 1

// Classify returns the lowering kind for a function of this interface.
func (wi WorldInterface) Classify(f WorldFunc) LowerKind {
	if f.Sig == nil {
		return KindNoOpt
	}
	// realloc: any result carries heap data.
	needRealloc := false
	for _, r := range wi.resultTypes(f.Sig) {
		if wi.containsHeap(r) {
			needRealloc = true
			break
		}
	}
	if needRealloc {
		return KindMemRealloc
	}
	// memory: a heap param, or an indirect (multi-flat) result.
	for _, p := range f.Sig.Params {
		if wi.containsHeap(p.Ty) {
			return KindMem
		}
	}
	flat := 0
	for _, r := range wi.resultTypes(f.Sig) {
		flat += wi.flattenCount(r)
	}
	if flat > maxFlatResults {
		return KindMem
	}
	return KindNoOpt
}

// resultTypes returns a function's result value types (one for an unnamed
// single result, the list for named results).
func (wi WorldInterface) resultTypes(sig *FuncType) []Valtype {
	if sig.NamedResults {
		out := make([]Valtype, len(sig.Results))
		for i, r := range sig.Results {
			out[i] = r.Ty
		}
		return out
	}
	return []Valtype{sig.Result}
}

// containsHeap reports whether a value type carries linear-memory data (a
// list or string, possibly nested inside a record / variant / tuple / option
// / result). Handles (own/borrow), enums, flags and other primitives do not.
func (wi WorldInterface) containsHeap(v Valtype) bool {
	if v.IsPrim {
		return v.Prim == primString
	}
	d := wi.ResolveDef(v)
	if d == nil { // resource handle or unresolved alias — scalar
		return false
	}
	switch d.Tag {
	case tagList:
		return true
	case tagOption:
		return wi.containsHeap(d.Elem)
	case tagTuple:
		for _, e := range d.Elems {
			if wi.containsHeap(e) {
				return true
			}
		}
	case tagRecord:
		for _, f := range d.Fields {
			if wi.containsHeap(f.Ty) {
				return true
			}
		}
	case tagVariant:
		for _, c := range d.Cases {
			if c.HasTy && wi.containsHeap(c.Ty) {
				return true
			}
		}
	case tagResult:
		if d.HasOk && wi.containsHeap(d.Ok) {
			return true
		}
		if d.HasErr && wi.containsHeap(d.Err) {
			return true
		}
	}
	return false
}

// flattenCount returns the number of core values a value type lowers to under
// the canonical ABI.
func (wi WorldInterface) flattenCount(v Valtype) int {
	if v.IsPrim {
		if v.Prim == primString {
			return 2 // ptr + len
		}
		return 1
	}
	d := wi.ResolveDef(v)
	if d == nil { // handle
		return 1
	}
	switch d.Tag {
	case tagList:
		return 2 // ptr + len
	case tagOwn, tagBorrow, tagEnum, tagFlags:
		return 1
	case tagOption:
		return 1 + wi.flattenCount(d.Elem)
	case tagTuple:
		n := 0
		for _, e := range d.Elems {
			n += wi.flattenCount(e)
		}
		return n
	case tagRecord:
		n := 0
		for _, f := range d.Fields {
			n += wi.flattenCount(f.Ty)
		}
		return n
	case tagVariant:
		return 1 + wi.maxCaseFlat(d.Cases)
	case tagResult:
		ok, er := 0, 0
		if d.HasOk {
			ok = wi.flattenCount(d.Ok)
		}
		if d.HasErr {
			er = wi.flattenCount(d.Err)
		}
		if ok > er {
			return 1 + ok
		}
		return 1 + er
	}
	return 1
}

func (wi WorldInterface) maxCaseFlat(cases []VariantCase) int {
	max := 0
	for _, c := range cases {
		n := 0
		if c.HasTy {
			n = wi.flattenCount(c.Ty)
		}
		if n > max {
			max = n
		}
	}
	return max
}
