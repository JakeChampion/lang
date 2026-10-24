package componenttype

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

const maxFlatResults = 1

func (wi WorldInterface) Classify(f WorldFunc) LowerKind {
	if f.Sig == nil {
		return KindNoOpt
	}
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

func (wi WorldInterface) containsHeap(v Valtype) bool {
	if v.IsPrim {
		return v.Prim == primString
	}
	d := wi.ResolveDef(v)
	if d == nil {
		return false
	}
	o := wi.ownerOf(v.Idx)
	switch d.Tag {
	case tagList:
		return true
	case tagOption:
		return o.containsHeap(d.Elem)
	case tagTuple:
		for _, e := range d.Elems {
			if o.containsHeap(e) {
				return true
			}
		}
	case tagRecord:
		for _, f := range d.Fields {
			if o.containsHeap(f.Ty) {
				return true
			}
		}
	case tagVariant:
		for _, c := range d.Cases {
			if c.HasTy && o.containsHeap(c.Ty) {
				return true
			}
		}
	case tagResult:
		if d.HasOk && o.containsHeap(d.Ok) {
			return true
		}
		if d.HasErr && o.containsHeap(d.Err) {
			return true
		}
	}
	return false
}

func (wi WorldInterface) flattenCount(v Valtype) int {
	if v.IsPrim {
		if v.Prim == primString {
			return 2
		}
		return 1
	}
	d := wi.ResolveDef(v)
	if d == nil {
		return 1
	}
	o := wi.ownerOf(v.Idx)
	switch d.Tag {
	case tagList:
		return 2
	case tagOwn, tagBorrow, tagEnum, tagFlags:
		return 1
	case tagOption:
		return 1 + o.flattenCount(d.Elem)
	case tagTuple:
		n := 0
		for _, e := range d.Elems {
			n += o.flattenCount(e)
		}
		return n
	case tagRecord:
		n := 0
		for _, f := range d.Fields {
			n += o.flattenCount(f.Ty)
		}
		return n
	case tagVariant:
		return 1 + o.maxCaseFlat(d.Cases)
	case tagResult:
		ok, er := 0, 0
		if d.HasOk {
			ok = o.flattenCount(d.Ok)
		}
		if d.HasErr {
			er = o.flattenCount(d.Err)
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
