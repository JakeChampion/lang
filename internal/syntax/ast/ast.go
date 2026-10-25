package ast

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/envflag"
)

type Position struct {
	Line int
	Col  int
}

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

type Comment struct {
	Pos      Position
	Text     string
	Trailing bool
}

type Type interface {
	String() string
	isType()
}

const WidthPtr = -1

type NumberType struct {
	Width       int
	Signed      bool
	Spelling    string
	Polymorphic bool
}
type BoolType struct{}
type VoidType struct{}
type StringType struct{}

type StrType struct{}

type CharType struct{}

type NeverType struct{}

type FloatType struct {
	Width       int
	Spelling    string
	Polymorphic bool
}

type LanesType struct {
	Width    int
	Spelling string
}

func (l LanesType) Lanes() int { return 128 / l.Width }

type ArrayType struct{ Elem Type }

type ConstArg struct {
	Value int64
	Raw   string
}

type StreamType struct{ Elem Type }

type SliceType struct{ Elem Type }

type TupleType struct {
	Elems []Type
}
type FuncType struct {
	Params   []Type
	ParamOwn []bool
	Result   Type
}

func (f *FuncType) OwnAt(i int) bool {
	return i >= 0 && i < len(f.ParamOwn) && f.ParamOwn[i]
}

func (f *FuncType) AnyOwn() bool {
	for _, o := range f.ParamOwn {
		if o {
			return true
		}
	}
	return false
}

func OwnFlags(flags []bool, n int) []bool {
	any := false
	for _, o := range flags {
		if o {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	out := make([]bool, n)
	copy(out, flags)
	return out
}

type StructType struct {
	Name string
	Args []Type
}

type EnumType struct {
	Name string
	Args []Type
}

type ParamType struct{ Name string }

type UnboundType struct{}

type SelfType struct{}

type ProjType struct {
	Base Type
	Name string
}

type DynTraitType struct {
	Traits        []string
	Args          [][]Type
	AssocBindings [][]AssocBinding
}

type AssocBinding struct {
	Name string
	Type Type
}

func (d DynTraitType) ArgsFor(i int) []Type {
	if i < 0 || i >= len(d.Args) {
		return nil
	}
	return d.Args[i]
}

func (d DynTraitType) AssocFor(i int) []AssocBinding {
	if i < 0 || i >= len(d.AssocBindings) {
		return nil
	}
	return d.AssocBindings[i]
}

func NewDynTraitType(traits ...string) DynTraitType {
	if len(traits) <= 1 {
		return DynTraitType{Traits: append([]string(nil), traits...)}
	}
	cp := append([]string(nil), traits...)
	sort.Strings(cp)
	out := cp[:0]
	var prev string
	for i, t := range cp {
		if i == 0 || t != prev {
			out = append(out, t)
			prev = t
		}
	}
	return DynTraitType{Traits: out}
}

func NewDynTraitTypeFull(traits []string, args [][]Type, assoc [][]AssocBinding) DynTraitType {
	if len(args) != len(traits) {
		na := make([][]Type, len(traits))
		copy(na, args)
		args = na
	}
	if len(assoc) != len(traits) {
		nb := make([][]AssocBinding, len(traits))
		copy(nb, assoc)
		assoc = nb
	}
	any := false
	for _, a := range args {
		if len(a) > 0 {
			any = true
		}
	}
	for i := range assoc {
		if len(assoc[i]) > 0 {
			sort.SliceStable(assoc[i], func(x, y int) bool { return assoc[i][x].Name < assoc[i][y].Name })
			any = true
		}
	}
	if !any {
		return NewDynTraitType(traits...)
	}
	idx := make([]int, len(traits))
	for i := range idx {
		idx[i] = i
	}
	key := func(i int) string {
		return traits[i] + "\x00" + typeArgsString(args[i]) + "\x00" + assocString(assoc[i])
	}
	sort.SliceStable(idx, func(a, b int) bool { return key(idx[a]) < key(idx[b]) })
	outT := make([]string, 0, len(traits))
	outA := make([][]Type, 0, len(traits))
	outB := make([][]AssocBinding, 0, len(traits))
	var prev string
	for n, i := range idx {
		k := key(i)
		if n == 0 || k != prev {
			outT = append(outT, traits[i])
			outA = append(outA, args[i])
			outB = append(outB, assoc[i])
			prev = k
		}
	}
	return DynTraitType{Traits: outT, Args: outA, AssocBindings: outB}
}

func assocString(bs []AssocBinding) string {
	if len(bs) == 0 {
		return ""
	}
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.Name + " = " + b.Type.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func typeArgsString(args []Type) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func (d DynTraitType) Trait0() string {
	if len(d.Traits) == 0 {
		return ""
	}
	return d.Traits[0]
}

type HandleType struct {
	Resource string
	Borrowed bool
}

func (NumberType) isType()   {}
func (SelfType) isType()     {}
func (BoolType) isType()     {}
func (VoidType) isType()     {}
func (NeverType) isType()    {}
func (StringType) isType()   {}
func (StrType) isType()      {}
func (CharType) isType()     {}
func (FloatType) isType()    {}
func (LanesType) isType()    {}
func (ArrayType) isType()    {}
func (ConstArg) isType()     {}
func (StreamType) isType()   {}
func (SliceType) isType()    {}
func (TupleType) isType()    {}
func (*FuncType) isType()    {}
func (StructType) isType()   {}
func (EnumType) isType()     {}
func (ParamType) isType()    {}
func (UnboundType) isType()  {}
func (DynTraitType) isType() {}
func (HandleType) isType()   {}
func (ProjType) isType()     {}
func (n NumberType) String() string {
	if n.IsPointerWidth() {
		return "usize"
	}
	if n.IsSigned() {
		return fmt.Sprintf("i%d", n.NormalWidth())
	}
	return fmt.Sprintf("u%d", n.NormalWidth())
}
func (BoolType) String() string   { return "boolean" }
func (VoidType) String() string   { return "void" }
func (NeverType) String() string  { return "never" }
func (StringType) String() string { return "string" }
func (StrType) String() string    { return "str" }
func (CharType) String() string   { return "char" }
func (f FloatType) String() string {
	return fmt.Sprintf("f%d", f.NormalWidth())
}
func (l LanesType) String() string {
	return fmt.Sprintf("f%dx%d", l.Width, l.Lanes())
}
func (c ConstArg) String() string { return strconv.FormatInt(c.Value, 10) }

func typeString(t Type) string {
	if _, ok := t.(*FuncType); ok {
		return "(" + t.String() + ")"
	}
	return t.String()
}

func (a ArrayType) String() string {
	if a.Elem == nil {
		return "[]"
	}
	return typeString(a.Elem) + "[]"
}
func (s SliceType) String() string {
	if s.Elem == nil {
		return "[]"
	}
	return "[" + s.Elem.String() + "]"
}
func (s StreamType) String() string {
	if s.Elem == nil {
		return "Stream"
	}
	return "Stream[" + s.Elem.String() + "]"
}
func (t TupleType) String() string {
	out := "("
	for i, e := range t.Elems {
		if i > 0 {
			out += ", "
		}
		out += e.String()
	}
	if len(t.Elems) == 1 {
		out += ","
	}
	return out + ")"
}
func (s StructType) String() string {
	if len(s.Args) == 0 {
		return s.Name
	}
	out := s.Name + "["
	for i, a := range s.Args {
		if i > 0 {
			out += ", "
		}
		out += a.String()
	}
	return out + "]"
}
func (e EnumType) String() string {
	if len(e.Args) == 0 {
		return e.Name
	}
	out := e.Name + "["
	for i, a := range e.Args {
		if i > 0 {
			out += ", "
		}
		out += a.String()
	}
	return out + "]"
}
func (p ParamType) String() string { return p.Name }
func (UnboundType) String() string { return "_" }
func (SelfType) String() string    { return "Self" }
func (d DynTraitType) String() string {
	parts := make([]string, len(d.Traits))
	for i, t := range d.Traits {
		var inner []string
		for _, a := range d.ArgsFor(i) {
			inner = append(inner, a.String())
		}
		for _, b := range d.AssocFor(i) {
			inner = append(inner, b.Name+" = "+b.Type.String())
		}
		if len(inner) > 0 {
			parts[i] = t + "[" + strings.Join(inner, ", ") + "]"
		} else {
			parts[i] = t
		}
	}
	return "dyn " + strings.Join(parts, " + ")
}
func (h HandleType) String() string {
	if h.Borrowed {
		return "borrow " + h.Resource
	}
	return "own " + h.Resource
}
func (p ProjType) String() string {
	base := "<nil>"
	if p.Base != nil {
		base = p.Base.String()
	}
	return base + "::" + p.Name
}

func SubstSelf(t Type, self Type) Type {
	switch tt := t.(type) {
	case SelfType:
		return self
	case ArrayType:
		return ArrayType{Elem: SubstSelf(tt.Elem, self)}
	case StreamType:
		return StreamType{Elem: SubstSelf(tt.Elem, self)}
	case SliceType:
		return SliceType{Elem: SubstSelf(tt.Elem, self)}
	case TupleType:
		elems := make([]Type, len(tt.Elems))
		for i, e := range tt.Elems {
			elems[i] = SubstSelf(e, self)
		}
		return TupleType{Elems: elems}
	case StructType:
		if len(tt.Args) == 0 {
			return tt
		}
		args := make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			args[i] = SubstSelf(a, self)
		}
		return StructType{Name: tt.Name, Args: args}
	case EnumType:
		args := make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			args[i] = SubstSelf(a, self)
		}
		return EnumType{Name: tt.Name, Args: args}
	case *FuncType:
		params := make([]Type, len(tt.Params))
		for i, pt := range tt.Params {
			params[i] = SubstSelf(pt, self)
		}
		return &FuncType{Params: params, ParamOwn: tt.ParamOwn, Result: SubstSelf(tt.Result, self)}
	case ProjType:
		return ProjType{Base: SubstSelf(tt.Base, self), Name: tt.Name}
	default:
		return t
	}
}

func clonePatternNames(bindings []string, types []Type, fields []string) ([]string, []Type, []string) {
	return append([]string(nil), bindings...),
		append([]Type(nil), types...),
		append([]string(nil), fields...)
}

func CloneBlock(b *Block) *Block {
	if b == nil {
		return nil
	}
	out := &Block{P: b.P, Stmts: make([]Stmt, len(b.Stmts))}
	for i, s := range b.Stmts {
		out.Stmts[i] = CloneStmt(s)
	}
	return out
}

func CloneStmt(s Stmt) Stmt {
	switch x := s.(type) {
	case *Var:
		c := *x
		c.Init = CloneExpr(x.Init)
		return &c
	case *Destructure:
		c := *x
		c.Names = append([]string(nil), x.Names...)
		c.Fields = append([]string(nil), x.Fields...)
		c.Init = CloneExpr(x.Init)
		if x.Nested != nil {
			c.Nested = make([]*Destructure, len(x.Nested))
			for i, sub := range x.Nested {
				if sub != nil {
					c.Nested[i] = CloneStmt(sub).(*Destructure)
				}
			}
		}
		return &c
	case *ExprStmt:
		c := *x
		c.Expr = CloneExpr(x.Expr)
		return &c
	case *Return:
		c := *x
		c.Value = CloneExpr(x.Value)
		return &c
	case *Block:
		return CloneBlock(x)
	case *If:
		c := *x
		c.Cond = CloneExpr(x.Cond)
		c.Then = CloneStmt(x.Then).(*Block)
		if x.Else != nil {
			c.Else = CloneStmt(x.Else)
		}
		return &c
	case *While:
		c := *x
		c.Cond = CloneExpr(x.Cond)
		if b, ok := x.Body.(*Block); ok {
			c.Body = CloneBlock(b)
		} else {
			c.Body = CloneStmt(x.Body)
		}
		return &c
	case *For:
		c := *x
		if x.Init != nil {
			c.Init = CloneStmt(x.Init)
		}
		c.Cond = CloneExpr(x.Cond)
		if x.Step != nil {
			c.Step = CloneStmt(x.Step)
		}
		if b, ok := x.Body.(*Block); ok {
			c.Body = CloneBlock(b)
		} else {
			c.Body = CloneStmt(x.Body)
		}
		return &c
	case *Match:
		c := *x
		c.Tag = CloneExpr(x.Tag)
		c.Arms = make([]*MatchArm, len(x.Arms))
		for i, arm := range x.Arms {
			ac := *arm
			ac.Guard = CloneExpr(arm.Guard)
			ac.Body = CloneBlock(arm.Body)
			ac.TupleElems = append([]TuplePatElem(nil), arm.TupleElems...)
			ac.Bindings, ac.BindingTypes, ac.FieldNames = clonePatternNames(arm.Bindings, arm.BindingTypes, arm.FieldNames)
			c.Arms[i] = &ac
		}
		return &c
	case *Defer:
		c := *x
		c.Expr = CloneExpr(x.Expr)
		return &c
	case *Loop:
		c := *x
		if b, ok := x.Body.(*Block); ok {
			c.Body = CloneBlock(b)
		} else {
			c.Body = CloneStmt(x.Body)
		}
		return &c
	case *ForEach:
		c := *x
		c.Iter = CloneExpr(x.Iter)
		c.RangeHigh = CloneExpr(x.RangeHigh)
		if x.Pattern != nil {
			c.Pattern = CloneStmt(x.Pattern).(*Destructure)
		}
		if b, ok := x.Body.(*Block); ok {
			c.Body = CloneBlock(b)
		} else {
			c.Body = CloneStmt(x.Body)
		}
		return &c
	case *Break:
		c := *x
		return &c
	case *Continue:
		c := *x
		return &c
	case *FuncDecl:
		c := *x
		c.TypeParams = append([]string(nil), x.TypeParams...)
		c.Params = append([]Param(nil), x.Params...)
		c.Body = CloneBlock(x.Body)
		return &c
	}
	panic(fmt.Sprintf("ast: CloneStmt: unhandled statement kind %T (add a case for it)", s))
}

func CloneExpr(e Expr) Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *UnitLit:
		c := *x
		return &c
	case *CaptureRef:
		c := *x
		return &c
	case *BlockExpr:
		c := *x
		c.Stmts = make([]Stmt, len(x.Stmts))
		for i, st := range x.Stmts {
			c.Stmts[i] = CloneStmt(st)
		}
		c.Tail = CloneExpr(x.Tail)
		return &c
	case *EnumLit:
		c := *x
		c.Args = make([]Expr, len(x.Args))
		for i, a := range x.Args {
			c.Args[i] = CloneExpr(a)
		}
		return &c
	case *MapLit:
		c := *x
		c.Entries = make([]MapEntry, len(x.Entries))
		for i, en := range x.Entries {
			c.Entries[i] = MapEntry{Key: CloneExpr(en.Key), Value: CloneExpr(en.Value)}
		}
		return &c
	case *FString:
		c := *x
		c.Parts = make([]FStringPart, len(x.Parts))
		for i, pt := range x.Parts {
			pc := pt
			pc.Expr = CloneExpr(pt.Expr)
			c.Parts[i] = pc
		}
		c.Desugared = CloneExpr(x.Desugared)
		return &c
	case *DowncastExpr:
		c := *x
		c.Inner = CloneExpr(x.Inner)
		return &c
	case *MakeClosure:
		c := *x
		c.Captures = make([]Expr, len(x.Captures))
		for i, cp := range x.Captures {
			c.Captures[i] = CloneExpr(cp)
		}
		return &c
	case *Ident:
		c := *x
		c.TypeArgs = slices.Clone(x.TypeArgs)
		return &c
	case *NumberLit:
		c := *x
		return &c
	case *FloatLit:
		c := *x
		return &c
	case *BoolLit:
		c := *x
		return &c
	case *StringLit:
		c := *x
		return &c
	case *CharLit:
		c := *x
		return &c
	case *Binary:
		c := *x
		c.Left = CloneExpr(x.Left)
		c.Right = CloneExpr(x.Right)
		return &c
	case *Unary:
		c := *x
		c.Operand = CloneExpr(x.Operand)
		return &c
	case *Call:
		c := *x
		c.Callee = CloneExpr(x.Callee)
		c.Args = make([]Expr, len(x.Args))
		for i, a := range x.Args {
			c.Args[i] = CloneExpr(a)
		}
		c.TypeArgs = append([]Type(nil), x.TypeArgs...)
		return &c
	case *Index:
		c := *x
		c.Array = CloneExpr(x.Array)
		c.Idx = CloneExpr(x.Idx)
		return &c
	case *SliceExpr:
		c := *x
		c.Source = CloneExpr(x.Source)
		c.Low = CloneExpr(x.Low)
		c.High = CloneExpr(x.High)
		return &c
	case *FieldAccess:
		c := *x
		c.Target = CloneExpr(x.Target)
		return &c
	case *TryOp:
		c := *x
		c.Inner = CloneExpr(x.Inner)
		return &c
	case *IfExpr:
		c := *x
		c.Cond = CloneExpr(x.Cond)
		c.Then = CloneExpr(x.Then)
		c.Else = CloneExpr(x.Else)
		return &c
	case *MatchExpr:
		c := *x
		c.Tag = CloneExpr(x.Tag)
		c.Arms = make([]*MatchExprArm, len(x.Arms))
		for i, arm := range x.Arms {
			a := *arm
			if arm.Guard != nil {
				a.Guard = CloneExpr(arm.Guard)
			}
			a.Body = CloneExpr(arm.Body)
			a.TupleElems = append([]TuplePatElem(nil), arm.TupleElems...)
			a.Bindings, a.BindingTypes, a.FieldNames = clonePatternNames(arm.Bindings, arm.BindingTypes, arm.FieldNames)
			c.Arms[i] = &a
		}
		return &c
	case *ArrayLit:
		c := *x
		c.Elems = make([]Expr, len(x.Elems))
		for i, el := range x.Elems {
			c.Elems[i] = CloneExpr(el)
		}
		return &c
	case *TupleLit:
		c := *x
		c.Elems = make([]Expr, len(x.Elems))
		for i, el := range x.Elems {
			c.Elems[i] = CloneExpr(el)
		}
		return &c
	case *Lambda:
		c := *x
		c.Params = append([]Param(nil), x.Params...)
		c.Captures = append([]Param(nil), x.Captures...)
		c.Body = CloneBlock(x.Body)
		return &c
	case *StructLit:
		c := *x
		c.Base = CloneExpr(x.Base)
		c.Fields = make([]FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			c.Fields[i] = FieldInit{Name: f.Name, Value: CloneExpr(f.Value)}
		}
		c.TypeArgs = append([]Type(nil), x.TypeArgs...)
		return &c
	case *CastExpr:
		c := *x
		c.Inner = CloneExpr(x.Inner)
		return &c
	case *Assign:
		c := *x
		c.Target = CloneExpr(x.Target)
		c.Value = CloneExpr(x.Value)
		return &c
	}
	panic(fmt.Sprintf("ast: CloneExpr: unhandled expression kind %T (add a case for it)", e))
}

func (f *FuncType) String() string {
	out := "("
	for i, p := range f.Params {
		if i > 0 {
			out += ", "
		}
		if f.OwnAt(i) {
			out += "own "
		}
		if p == nil {
			out += "<unknown>"
			continue
		}
		out += p.String()
	}
	out += ") => "
	if f.Result == nil {
		return out + "<unknown>"
	}
	return out + f.Result.String()
}

func (n NumberType) NormalWidth() int {
	if n.Width == 0 {
		return 32
	}
	return n.Width
}

func (n NumberType) IsPointerWidth() bool {
	return n.Width == WidthPtr
}

func (n NumberType) IsSigned() bool {
	if n.Width == 0 {
		return true
	}
	return n.Signed
}

func (f FloatType) NormalWidth() int {
	if f.Width == 0 {
		if f.Polymorphic {
			return 64
		}
		return 32
	}
	return f.Width
}

var CoverEnabled = envflag.On("FERN_COVER")

const CoverLinePrefix = "fern-cover: "

const CoverBranchPrefix = "fern-branch: "

var BacktraceEnabled = backtraceFromEnv()

func backtraceFromEnv() bool {
	v, set := os.LookupEnv("FERN_BACKTRACE")
	return !set || envflag.ValueOn(v)
}

const RcPoison = 0x7EEDFACE

func StructuralMapKey(t Type) bool {
	switch x := t.(type) {
	case TupleType:
		if len(x.Elems) == 0 {
			return false
		}
		for _, e := range x.Elems {
			if !structuralKeyElement(e) {
				return false
			}
		}
		return true
	case ArrayType:
		return structuralKeyElement(x.Elem)
	}
	return false
}

func structuralKeyElement(t Type) bool {
	switch t.(type) {
	case NumberType, BoolType, CharType, StringType:
		return true
	}
	return StructuralMapKey(t)
}

func IsPointerType(t Type) bool {
	switch t.(type) {
	case StringType, StrType, ArrayType, SliceType, TupleType,
		StructType, EnumType:
		return true
	case *FuncType:
		return true
	case DynTraitType:
		return true
	}
	return false
}

func ReceiverTypeName(t Type) (string, bool) {
	switch rt := t.(type) {
	case StructType:
		return rt.Name, true
	case EnumType:
		return rt.Name, true
	case StringType:
		return "string", true
	case StrType:
		return "string", true
	case CharType:
		return "char", true
	case NumberType:
		switch {
		case rt.IsPointerWidth():
			return "usize", true
		case rt.NormalWidth() == 64 && rt.IsSigned():
			return "i64", true
		case rt.NormalWidth() == 64 && !rt.IsSigned():
			return "u64", true
		case rt.NormalWidth() == 8:
			return "u8", true
		case !rt.IsSigned():
			return "u32", true
		default:
			return "i32", true
		}
	case FloatType:
		if rt.NormalWidth() == 64 {
			return "f64", true
		}
		return "f32", true
	case LanesType:
		return rt.String(), true
	case BoolType:
		return "boolean", true
	default:
		return "", false
	}
}

func Equal(a, b Type) bool {
	switch x := a.(type) {
	case NumberType:
		y, ok := b.(NumberType)
		return ok && x.NormalWidth() == y.NormalWidth() && x.IsSigned() == y.IsSigned()
	case BoolType:
		_, ok := b.(BoolType)
		return ok
	case VoidType:
		_, ok := b.(VoidType)
		return ok
	case NeverType:
		_, ok := b.(NeverType)
		return ok
	case StringType:
		_, ok := b.(StringType)
		return ok
	case StrType:
		_, ok := b.(StrType)
		return ok
	case CharType:
		_, ok := b.(CharType)
		return ok
	case FloatType:
		y, ok := b.(FloatType)
		return ok && x.NormalWidth() == y.NormalWidth()
	case LanesType:
		y, ok := b.(LanesType)
		return ok && x.Width == y.Width
	case ConstArg:
		y, ok := b.(ConstArg)
		return ok && x.Value == y.Value
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && Equal(x.Elem, y.Elem)
	case StreamType:
		y, ok := b.(StreamType)
		return ok && Equal(x.Elem, y.Elem)
	case SliceType:
		y, ok := b.(SliceType)
		return ok && Equal(x.Elem, y.Elem)
	case TupleType:
		y, ok := b.(TupleType)
		if !ok || len(x.Elems) != len(y.Elems) {
			return false
		}
		for i := range x.Elems {
			if !Equal(x.Elems[i], y.Elems[i]) {
				return false
			}
		}
		return true
	case *FuncType:
		y, ok := b.(*FuncType)
		if !ok || len(x.Params) != len(y.Params) || !Equal(x.Result, y.Result) {
			return false
		}
		for i := range x.Params {
			if !Equal(x.Params[i], y.Params[i]) || x.OwnAt(i) != y.OwnAt(i) {
				return false
			}
		}
		return true
	case StructType:
		y, ok := b.(StructType)
		if !ok || x.Name != y.Name || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !Equal(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	case EnumType:
		y, ok := b.(EnumType)
		if !ok || x.Name != y.Name || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !Equal(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	case ParamType:
		y, ok := b.(ParamType)
		return ok && x.Name == y.Name
	case UnboundType:
		_, ok := b.(UnboundType)
		return ok
	case SelfType:
		_, ok := b.(SelfType)
		return ok
	case DynTraitType:
		y, ok := b.(DynTraitType)
		if !ok || len(x.Traits) != len(y.Traits) {
			return false
		}
		for i := range x.Traits {
			if x.Traits[i] != y.Traits[i] {
				return false
			}
			xa, ya := x.ArgsFor(i), y.ArgsFor(i)
			if len(xa) != len(ya) {
				return false
			}
			for j := range xa {
				if !Equal(xa[j], ya[j]) {
					return false
				}
			}
			xb, yb := x.AssocFor(i), y.AssocFor(i)
			if len(xb) != len(yb) {
				return false
			}
			for j := range xb {
				if xb[j].Name != yb[j].Name || !Equal(xb[j].Type, yb[j].Type) {
					return false
				}
			}
		}
		return true
	case HandleType:
		y, ok := b.(HandleType)
		return ok && x.Resource == y.Resource && x.Borrowed == y.Borrowed
	case ProjType:
		y, ok := b.(ProjType)
		return ok && x.Name == y.Name && Equal(x.Base, y.Base)
	}
	return false
}

type Expr interface {
	Pos() Position
	isExpr()
}

type NumberLit struct {
	P          Position
	Value      int64
	Raw        string
	Width      int
	IsUnsigned bool
	IsFloat    bool
	FloatWidth int
	ExceedsI64 bool
	ExceedsU64 bool
}

type CastExpr struct {
	P         Position
	Inner     Expr
	Target    Type
	InnerType Type
}

type DowncastExpr struct {
	P       Position
	Inner   Expr
	Target  Type
	Trait   string
	Traits  []string
	Lowered Expr
}

type BlockExpr struct {
	P     Position
	Stmts []Stmt
	Tail  Expr
}
type BoolLit struct {
	P     Position
	Value bool
}

type UnitLit struct {
	P Position
}
type StringLit struct {
	P     Position
	Value string
}

type CharLit struct {
	P      Position
	Value  int64
	Raw    string
	IsByte bool
}

type FString struct {
	P         Position
	Parts     []FStringPart
	Desugared Expr
}

type FStringPart struct {
	Lit  string
	Expr Expr
}
type FloatLit struct {
	P     Position
	Value float64
	Raw   string
	Width int
}
type Ident struct {
	P          Position
	Name       string
	TypeArgs   []Type
	EnumName   string
	Local      bool
	ConstParam bool
}
type ArrayLit struct {
	P        Position
	Elems    []Expr
	ElemType Type
	Const    bool
}
type Index struct {
	P           Position
	Array       Expr
	Idx         Expr
	IsString    bool
	IsSlice     bool
	IdxUnsigned bool
	ElemType    Type
	Unchecked   bool
}

type SliceExpr struct {
	P             Position
	Source        Expr
	Low           Expr
	High          Expr
	SourceIsSlice bool
	IsString      bool
	ElemType      Type
	Lent          bool
}
type Call struct {
	P               Position
	Callee          Expr
	Args            []Expr
	AssocConst      bool
	SourceCallee    string
	ArgNames        []string
	IsPipe          bool
	PipeHole        int
	TypeArgs        []Type
	TypeArgsWritten bool
	Method          *MethodCallSite
	Module          *ModuleCallSite
	IsVariantCall   bool
	DynTrait        string
}

type MethodCallSite struct {
	Field      string
	FieldPos   Position
	Receiver   Type
	OwnerTrait string
	Assoc      bool
}

type ModuleCallSite struct {
	Module    string
	ModulePos Position
	Field     string
	FieldPos  Position
	Mangled   string
}
type Binary struct {
	P              Position
	Op             string
	Left, Right    Expr
	IsStringConcat bool
	IsStringCmp    bool
	IsStringOrd    bool
	IsFloat        bool
	IsLanes        bool
	FloatWidth     int
	IntWidth       int
	IsUnsigned     bool
	EqCall         *Call
	EqNegate       bool
	CmpCall        *Call
	ParamCmp       bool
	ArithCall      *Call
	CheckedLowered Expr
}
type Unary struct {
	P           Position
	Op          string
	Operand     Expr
	CheckedType Type
	IsFloat     bool
	NegCall     *Call
}
type Assign struct {
	P      Position
	Target Expr
	Value  Expr
}

type TryKind int

const (
	TryKindBuild TryKind = iota
	TryKindForward
)

type TryOp struct {
	P       Position
	Inner   Expr
	Kind    TryKind
	Type    Type
	SrcEnum Type
	Lowered Expr
}

type IfExpr struct {
	P       Position
	Cond    Expr
	Then    Expr
	Else    Expr
	IsFloat bool
}

type StructLit struct {
	P               Position
	TypeName        string
	Fields          []FieldInit
	Base            Expr
	TypeArgs        []Type
	TypeArgsWritten bool
}

type TupleLit struct {
	P     Position
	Elems []Expr
}

type MapLit struct {
	P         Position
	Entries   []MapEntry
	KeyType   Type
	ValueType Type
}

type MapEntry struct {
	Key   Expr
	Value Expr
}

type FieldInit struct {
	Name    string
	NamePos Position
	Value   Expr
}

type EnumLit struct {
	P            Position
	EnumName     string
	VariantName  string
	VariantIndex int
	Args         []Expr
}

type FieldAccess struct {
	P        Position
	Target   Expr
	Field    string
	FieldPos Position
}

type Lambda struct {
	P                 Position
	Params            []Param
	ReturnType        Type
	ReturnUnannotated bool
	Body              *Block
	Captures          []Param
	Synthetic         *FuncDecl
}

type CaptureRef struct {
	P      Position
	Name   string
	Offset int
	Type   Type
}

type MakeClosure struct {
	P         Position
	FuncName  string
	FuncIndex int
	Captures  []Expr
}

func (e *NumberLit) Pos() Position    { return e.P }
func (e *CastExpr) Pos() Position     { return e.P }
func (e *DowncastExpr) Pos() Position { return e.P }
func (e *BlockExpr) Pos() Position    { return e.P }
func (e *BoolLit) Pos() Position      { return e.P }
func (e *UnitLit) Pos() Position      { return e.P }
func (e *StringLit) Pos() Position    { return e.P }
func (e *CharLit) Pos() Position      { return e.P }
func (e *FString) Pos() Position      { return e.P }
func (e *FloatLit) Pos() Position     { return e.P }
func (e *Ident) Pos() Position        { return e.P }
func (e *ArrayLit) Pos() Position     { return e.P }
func (e *Index) Pos() Position        { return e.P }
func (e *SliceExpr) Pos() Position    { return e.P }
func (e *Call) Pos() Position         { return e.P }
func (e *Binary) Pos() Position       { return e.P }
func (e *Unary) Pos() Position        { return e.P }
func (e *Assign) Pos() Position       { return e.P }
func (e *IfExpr) Pos() Position       { return e.P }
func (e *MatchExpr) Pos() Position    { return e.P }
func (e *TryOp) Pos() Position        { return e.P }
func (e *StructLit) Pos() Position    { return e.P }
func (e *TupleLit) Pos() Position     { return e.P }
func (e *MapLit) Pos() Position       { return e.P }
func (e *FieldAccess) Pos() Position  { return e.P }
func (e *EnumLit) Pos() Position      { return e.P }
func (e *CaptureRef) Pos() Position   { return e.P }
func (e *MakeClosure) Pos() Position  { return e.P }
func (e *Lambda) Pos() Position       { return e.P }

func (*NumberLit) isExpr()    {}
func (*CastExpr) isExpr()     {}
func (*DowncastExpr) isExpr() {}

func (e *DowncastExpr) String() string {
	return fmt.Sprintf("%v as? %s", e.Inner, e.Target)
}

func (*BlockExpr) isExpr() {}

func (e *BlockExpr) String() string {
	tail := "void"
	if e.Tail != nil {
		tail = fmt.Sprintf("%v", e.Tail)
	}
	if len(e.Stmts) == 0 {
		return fmt.Sprintf("{ %s }", tail)
	}
	return fmt.Sprintf("{ <%d stmt(s)>; %s }", len(e.Stmts), tail)
}
func (*BoolLit) isExpr()     {}
func (*UnitLit) isExpr()     {}
func (*StringLit) isExpr()   {}
func (*CharLit) isExpr()     {}
func (*FString) isExpr()     {}
func (*FloatLit) isExpr()    {}
func (*Ident) isExpr()       {}
func (*ArrayLit) isExpr()    {}
func (*Index) isExpr()       {}
func (*SliceExpr) isExpr()   {}
func (*Call) isExpr()        {}
func (*Binary) isExpr()      {}
func (*Unary) isExpr()       {}
func (*Assign) isExpr()      {}
func (*IfExpr) isExpr()      {}
func (*MatchExpr) isExpr()   {}
func (*TryOp) isExpr()       {}
func (*StructLit) isExpr()   {}
func (*TupleLit) isExpr()    {}
func (*MapLit) isExpr()      {}
func (*FieldAccess) isExpr() {}
func (*EnumLit) isExpr()     {}
func (*CaptureRef) isExpr()  {}
func (*MakeClosure) isExpr() {}
func (*Lambda) isExpr()      {}

type Stmt interface {
	Pos() Position
	isStmt()
}

type Block struct {
	P       Position
	End     Position
	Stmts   []Stmt
	Sugar   *ForEach
	IsTodo  bool
	TodoMsg Expr
}
type If struct {
	P        Position
	Cond     Expr
	Then     Stmt
	Else     Stmt
	IsAssert bool
}

type While struct {
	P     Position
	Cond  Expr
	Body  Stmt
	Label string
}

type Loop struct {
	P     Position
	Body  Stmt
	Label string
}

type For struct {
	P     Position
	Init  Stmt
	Cond  Expr
	Step  Stmt
	Body  Stmt
	Label string
}

type ForEach struct {
	P         Position
	ID        int
	Var       string
	VarPos    Position
	Iter      Expr
	Body      Stmt
	Label     string
	RangeHigh Expr
	RangeIncl bool
	Pattern   *Destructure
}

const ForEachIterPrefix = "__foreach_iter_"

func ForEachIterName(id int) string { return fmt.Sprintf("%s%d", ForEachIterPrefix, id) }

func ForEachElemName(id int) string { return fmt.Sprintf("__foreach_elem_%d", id) }

func ForEachArrayLoop(fe *ForEach, iterName string) []Stmt {
	kw := fe.P
	idxName := fmt.Sprintf("__foreach_idx_%d", fe.ID)
	lenName := fmt.Sprintf("__foreach_len_%d", fe.ID)
	mkIdent := func(name string) *Ident { return &Ident{P: kw, Name: name} }
	mkNum := func(v int64) *NumberLit { return &NumberLit{P: kw, Value: v} }

	declLen := &Var{P: kw, Name: lenName, Init: &Call{P: kw, Callee: &FieldAccess{P: kw, Target: mkIdent(iterName), Field: "len", FieldPos: kw}}}
	declIdx := &Var{P: kw, Name: idxName, Init: mkNum(0)}
	bindUser := &Var{P: fe.VarPos, Name: fe.Var, Init: &Index{P: fe.VarPos, Array: mkIdent(iterName), Idx: mkIdent(idxName), Unchecked: true}}
	stepStmt := &ExprStmt{P: kw, Expr: &Assign{P: kw, Target: mkIdent(idxName), Value: &Binary{P: kw, Op: "+", Left: mkIdent(idxName), Right: mkNum(1)}}}

	innerStmts := []Stmt{bindUser}
	if fe.Pattern != nil {
		fe.Pattern.Init = mkIdent(fe.Var)
		innerStmts = append(innerStmts, fe.Pattern)
	}
	innerStmts = append(innerStmts, foreachBodyStmts(fe)...)
	forLoop := &For{
		P:     kw,
		Cond:  &Binary{P: kw, Op: "<", Left: mkIdent(idxName), Right: mkIdent(lenName)},
		Step:  stepStmt,
		Body:  &Block{P: kw, Stmts: innerStmts},
		Label: fe.Label,
	}
	return []Stmt{declLen, declIdx, forLoop}
}

func ForEachMapLoop(fe *ForEach, iterName string) []Stmt {
	kw := fe.P
	keysName, valuesName := iterName+"_keys", iterName+"_values"
	idxName := fmt.Sprintf("__foreach_idx_%d", fe.ID)
	mkIdent := func(name string) *Ident { return &Ident{P: kw, Name: name} }
	call := func(name, method string) *Call {
		return &Call{P: kw, Callee: &FieldAccess{P: kw, Target: mkIdent(name), Field: method, FieldPos: kw}}
	}
	read := func(name string) *Index {
		return &Index{P: fe.Pattern.P, Array: mkIdent(name), Idx: mkIdent(idxName), Unchecked: true}
	}
	inner := []Stmt{
		&Var{P: fe.Pattern.P, Name: fe.Pattern.Names[0], Init: read(keysName)},
		&Var{P: fe.Pattern.P, Name: fe.Pattern.Names[1], Init: read(valuesName)},
	}
	for _, sub := range fe.Pattern.Nested {
		if sub != nil {
			inner = append(inner, sub)
		}
	}
	inner = append(inner, foreachBodyStmts(fe)...)
	loop := &For{P: kw, Cond: &Binary{P: kw, Op: "<", Left: mkIdent(idxName), Right: call(keysName, "len")},
		Step: &ExprStmt{P: kw, Expr: &Assign{P: kw, Target: mkIdent(idxName), Value: &Binary{P: kw, Op: "+", Left: mkIdent(idxName), Right: &NumberLit{P: kw, Value: 1}}}},
		Body: &Block{P: kw, Stmts: inner}, Label: fe.Label}
	return []Stmt{
		&Var{P: kw, Name: keysName, Init: call(iterName, "keys")},
		&Var{P: kw, Name: valuesName, Init: call(iterName, "values")},
		&Var{P: kw, Name: idxName, Init: &NumberLit{P: kw, Value: 0}}, loop,
	}
}

func ForEachIteratorLoop(fe *ForEach, iterName string) []Stmt {
	kw := fe.P
	stepName := fmt.Sprintf("__foreach_step_%d", fe.ID)
	mkIdent := func(name string) *Ident { return &Ident{P: kw, Name: name} }
	stepField := func(i string) *FieldAccess {
		return &FieldAccess{P: kw, Target: mkIdent(stepName), Field: i, FieldPos: kw}
	}
	innerStmts := []Stmt{
		&ExprStmt{P: kw, Expr: &Assign{P: kw, Target: mkIdent(iterName), Value: stepField("1")}},
		&Var{P: fe.VarPos, Name: fe.Var, Init: stepField("0")},
	}
	if fe.Pattern != nil {
		fe.Pattern.Init = mkIdent(fe.Var)
		innerStmts = append(innerStmts, fe.Pattern)
	}
	innerStmts = append(innerStmts, foreachBodyStmts(fe)...)
	next := &Call{P: kw, Callee: &FieldAccess{P: kw, Target: mkIdent(iterName), Field: "next", FieldPos: kw}}
	pull := &Match{P: kw, Tag: next, Arms: []*MatchArm{
		{P: kw, VariantName: "Some", Bindings: []string{stepName}, Body: &Block{P: kw, Stmts: innerStmts}},
		{P: kw, VariantName: "None", Body: &Block{P: kw, Stmts: []Stmt{&Break{P: kw}}}},
	}}
	return []Stmt{&Loop{P: kw, Body: &Block{P: kw, Stmts: []Stmt{pull}}, Label: fe.Label}}
}

func foreachBodyStmts(fe *ForEach) []Stmt {
	if blk, ok := fe.Body.(*Block); ok {
		return blk.Stmts
	}
	return []Stmt{fe.Body}
}

type Break struct {
	P     Position
	Label string
}
type Continue struct {
	P     Position
	Label string
}

type Return struct {
	P     Position
	Value Expr
}

type Defer struct {
	P       Position
	Expr    Expr
	OnError bool
}

type Var struct {
	P            Position
	Name         string
	Type         Type
	Init         Expr
	WasAnnotated bool
}

type Destructure struct {
	P           Position
	Names       []string
	Fields      []string
	StructName  string
	RestWritten bool
	Init        Expr
	AtName      string
	TempName    string
	Nested      []*Destructure
}
type ExprStmt struct {
	P    Position
	Expr Expr
}

const (
	OriginIfLet   = "if_let"
	OriginLetElse = "let_else"
)

type Match struct {
	P           Position
	Tag         Expr
	Arms        []*MatchArm
	StructMatch string
	Origin      string
}

type TuplePatElem struct {
	Name                string
	IsWildcard          bool
	Literal             Expr
	VariantName         string
	VariantModule       string
	VariantBindings     []string
	VariantBindingTypes []Type
	VariantFieldNames   []string
	VariantPayloads     []*TuplePatElem
	Nested              []TuplePatElem
	NestedTypes         []Type
	RangeHi             Expr
	RangeInclusive      bool
	AtBinding           string
	IsStruct            bool
	RestWritten         bool
}

type MatchArm struct {
	P               Position
	VariantName     string
	VariantModule   string
	Bindings        []string
	BindingTypes    []Type
	NamedFields     bool
	FieldNames      []string
	RestWritten     bool
	IsWildcard      bool
	Literal         Expr
	RangeHi         Expr
	RangeInclusive  bool
	TupleElems      []TuplePatElem
	Guard           Expr
	AtBinding       string
	EnumName        string
	VariantIndex    int
	Payloads        []*TuplePatElem
	CoversRemainder bool
	AltCont         bool
	Body            *Block
}

func MatchTakesPointerPayload(arms []*MatchArm) bool {
	for _, a := range arms {
		if armTakesPointer(a.Bindings, a.BindingTypes, a.AtBinding, a.TupleElems, a.Payloads) {
			return true
		}
	}
	return false
}

func MatchExprTakesPointerPayload(arms []*MatchExprArm) bool {
	for _, a := range arms {
		if armTakesPointer(a.Bindings, a.BindingTypes, a.AtBinding, a.TupleElems, a.Payloads) {
			return true
		}
	}
	return false
}

func armTakesPointer(bindings []string, types []Type, at string, tuple []TuplePatElem, payloads []*TuplePatElem) bool {
	if at != "" || len(tuple) > 0 {
		return true
	}
	for _, sub := range payloads {
		if sub != nil {
			return true
		}
	}
	for i, n := range bindings {
		if n != "" && n != "_" && i < len(types) && types[i] != nil && IsPointerType(types[i]) {
			return true
		}
	}
	return false
}

type MatchExpr struct {
	Origin      string
	P           Position
	Tag         Expr
	Arms        []*MatchExprArm
	IsFloat     bool
	StructMatch string
}

type MatchExprArm struct {
	P               Position
	VariantName     string
	VariantModule   string
	Bindings        []string
	BindingTypes    []Type
	NamedFields     bool
	FieldNames      []string
	RestWritten     bool
	IsWildcard      bool
	Literal         Expr
	RangeHi         Expr
	RangeInclusive  bool
	TupleElems      []TuplePatElem
	Guard           Expr
	AtBinding       string
	EnumName        string
	VariantIndex    int
	CoversRemainder bool
	Payloads        []*TuplePatElem
	AltCont         bool
	Body            Expr
}

func (a *MatchArm) Binders() []string {
	return armBinders(a.Bindings, a.AtBinding, a.TupleElems, a.Payloads)
}

func (a *MatchExprArm) Binders() []string {
	return armBinders(a.Bindings, a.AtBinding, a.TupleElems, a.Payloads)
}

func armBinders(bindings []string, atBinding string, tupleElems []TuplePatElem, payloads []*TuplePatElem) []string {
	out := appendNonEmpty(nil, bindings...)
	out = appendNonEmpty(out, atBinding)
	out = tuplePatBinders(out, tupleElems)
	for _, p := range payloads {
		if p != nil {
			out = tuplePatBinders(out, []TuplePatElem{*p})
		}
	}
	return out
}

func tuplePatBinders(acc []string, elems []TuplePatElem) []string {
	for _, el := range elems {
		acc = appendNonEmpty(acc, el.Name, el.AtBinding)
		acc = appendNonEmpty(acc, el.VariantBindings...)
		acc = tuplePatBinders(acc, el.Nested)
		for _, vp := range el.VariantPayloads {
			if vp != nil {
				acc = tuplePatBinders(acc, []TuplePatElem{*vp})
			}
		}
	}
	return acc
}

func (a *MatchExprArm) BinderType(name string, scrutinee Type) Type {
	if a == nil || name == "" {
		return nil
	}
	if a.AtBinding == name {
		return scrutinee
	}
	for i, bn := range a.Bindings {
		if bn == name && i < len(a.BindingTypes) {
			return a.BindingTypes[i]
		}
	}
	for i, p := range a.Payloads {
		if t := p.binderType(name, nthType(a.BindingTypes, i)); t != nil {
			return t
		}
	}
	for i := range a.TupleElems {
		if t := a.TupleElems[i].binderType(name, nthType(a.BindingTypes, i)); t != nil {
			return t
		}
	}
	return nil
}

func (el *TuplePatElem) binderType(name string, elT Type) Type {
	if el == nil {
		return nil
	}
	if el.Name == name || el.AtBinding == name {
		return elT
	}
	for i, bn := range el.VariantBindings {
		if bn == name && i < len(el.VariantBindingTypes) {
			return el.VariantBindingTypes[i]
		}
	}
	for i, p := range el.VariantPayloads {
		if t := p.binderType(name, nthType(el.VariantBindingTypes, i)); t != nil {
			return t
		}
	}
	for i := range el.Nested {
		if t := el.Nested[i].binderType(name, nthType(el.NestedTypes, i)); t != nil {
			return t
		}
	}
	return nil
}

func nthType(ts []Type, i int) Type {
	if i < len(ts) {
		return ts[i]
	}
	return nil
}

func appendNonEmpty(acc []string, names ...string) []string {
	for _, n := range names {
		if n != "" {
			acc = append(acc, n)
		}
	}
	return acc
}

func (s *Block) Pos() Position                  { return s.P }
func (s *If) Pos() Position                     { return s.P }
func (s *While) Pos() Position                  { return s.P }
func (s *Loop) Pos() Position                   { return s.P }
func (s *For) Pos() Position                    { return s.P }
func (s *ForEach) Pos() Position                { return s.P }
func (s *Break) Pos() Position                  { return s.P }
func (s *Continue) Pos() Position               { return s.P }
func (s *Return) Pos() Position                 { return s.P }
func (s *Defer) Pos() Position                  { return s.P }
func (s *Var) Pos() Position                    { return s.P }
func (s *Destructure) Pos() Position            { return s.P }
func (s *ExprStmt) Pos() Position               { return s.P }
func (s *Match) Pos() Position                  { return s.P }
func (s *FuncDecl) Pos() Position               { return s.P }
func (s *FuncDecl) GenericName() string         { return s.Name }
func (s *FuncDecl) GenericTypeParams() []string { return s.TypeParams }

func (*Block) isStmt()       {}
func (*If) isStmt()          {}
func (*While) isStmt()       {}
func (*Loop) isStmt()        {}
func (*For) isStmt()         {}
func (*ForEach) isStmt()     {}
func (*Break) isStmt()       {}
func (*Continue) isStmt()    {}
func (*Return) isStmt()      {}
func (*Defer) isStmt()       {}
func (*Var) isStmt()         {}
func (*Destructure) isStmt() {}
func (*ExprStmt) isStmt()    {}
func (*Match) isStmt()       {}
func (*FuncDecl) isStmt()    {}

type Param struct {
	Name    string
	NamePos Position
	Type    Type
	Own     bool
	Default Expr
	Pattern *Destructure
}

type InlineHint int

const (
	InlineHintNone InlineHint = iota
	InlineHintAlways
	InlineHintNever
)

type ImplSig struct {
	Params []Type
	Result Type
}

type FuncDecl struct {
	P                        Position
	Name                     string
	NamePos                  Position
	TypeParams               []string
	ConstParams              []string
	Bounds                   map[string][]string
	BoundArgs                map[string][][]Type
	Params                   []Param
	ReturnType               Type
	ReturnUnannotated        bool
	Body                     *Block
	ImportIface              string
	ImportWITName            string
	ExportIface              string
	ExportWITName            string
	Public                   bool
	PackageScoped            bool
	Receiver                 *Param
	MethodRecv               string
	MethodSimpleName         string
	ImplTrait                string
	TraitDefault             string
	AssocType                string
	AssocConst               bool
	ImplWritten              *ImplSig
	IsLocal                  bool
	Fip                      bool
	Fbip                     bool
	FipAllowance             int
	Async                    bool
	InlineHint               InlineHint
	IsSynthesisedHandlerMain bool
	Captures                 []Param
	UseSource                *Call
	SourceModule             string
	SourceFile               string
	DeclName                 string
	DefiningModule           string
}

func (f *FuncDecl) BodyModule() string {
	if f.DefiningModule != "" {
		return f.DefiningModule
	}
	return f.SourceModule
}

type GenericDecl interface {
	Node
	GenericName() string
	GenericTypeParams() []string
}

type StructDecl struct {
	P             Position
	Name          string
	TypeParams    []string
	Fields        []Param
	Public        bool
	PackageScoped bool
	Opaque        bool
	Derives       []string
	MustConsume   bool
	SourceModule  string
}

type EnumDecl struct {
	P             Position
	Name          string
	TypeParams    []string
	Variants      []EnumVariant
	Monomorphized bool
	Derives       []string
	MustConsume   bool
	Try           bool
	Public        bool
	PackageScoped bool
	SourceModule  string
}

type EnumVariant struct {
	P          Position
	Name       string
	Payloads   []Type
	FieldNames []string
}

type ResourceDecl struct {
	P             Position
	Name          string
	ImportIface   string
	ImportWITName string
	SourceModule  string
}

type UnionDecl struct {
	P             Position
	Name          string
	TypeParams    []string
	Members       []StructType
	MemberLines   []int
	Public        bool
	PackageScoped bool
	SourceModule  string
}

type TraitDecl struct {
	P           Position
	Name        string
	NamePos     Position
	Methods     []TraitMethod
	TypeParams  []string
	Supertraits []string
	// SupertraitArgs stores the type arguments for each parent in Supertraits order.
	SupertraitArgs [][]Type
	AssocTypes     []string
	Public         bool
	PackageScoped  bool
	SourceModule   string
}

type TraitMethod struct {
	P          Position
	Name       string
	Params     []Param
	Result     Type
	Assoc      bool
	Const      bool
	Body       *Block
	TypeParams []string
	Bounds     map[string][]string
	BoundArgs  map[string][][]Type
}

type ImplDecl struct {
	P                 Position
	Trait             string
	TraitPos          Position
	Type              Type
	TypePos           Position
	MethodNames       []string
	TraitArgs         []Type
	TypeParams        []string
	Bounds            map[string][]string
	BoundArgs         map[string][][]Type
	AssocTypeBindings map[string]Type
	SourceModule      string
	Methods           []*FuncDecl
}

func (s *TraitDecl) Pos() Position { return s.P }
func (s *ImplDecl) Pos() Position  { return s.P }

type Program struct {
	Funcs             []*FuncDecl
	Structs           []*StructDecl
	TodoSites         []Position
	Resources         []*ResourceDecl
	Traits            []*TraitDecl
	Impls             []*ImplDecl
	Enums             []*EnumDecl
	Unions            []*UnionDecl
	Consts            []*ConstDecl
	Imports           []*Import
	PubUses           []*PubUse
	ModuleImports     map[string]map[string]bool
	EntryModule       string
	DirectImports     map[string]map[string]bool
	LoadedStdlibPaths map[string]bool
	CapGrants         map[string][]string
	Comments          []Comment
	BlankLines        []int
	TypeRefs          []TypeRef
}

type TypeRef struct {
	P            Position
	Name         string
	SourceModule string
}

type ConstDecl struct {
	P             Position
	Name          string
	Type          Type
	Value         Expr
	Public        bool
	SourceModule  string
	PackageScoped bool
}

type Import struct {
	P         Position
	Path      string
	LocalName string
	Alias     string
}

type PubUse struct {
	P     Position
	Path  string
	Names []string
}

func (d *PubUse) Pos() Position { return d.P }

func (d *StructDecl) Pos() Position               { return d.P }
func (d *StructDecl) GenericName() string         { return d.Name }
func (d *StructDecl) GenericTypeParams() []string { return d.TypeParams }
func (d *EnumDecl) Pos() Position                 { return d.P }
func (d *UnionDecl) Pos() Position                { return d.P }
func (d *ConstDecl) Pos() Position                { return d.P }
func (d *Import) Pos() Position                   { return d.P }
