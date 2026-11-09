package checker

import (
	"fmt"
	"strconv"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// noRCIntrinsics are the intrinsics that a @norc function may call. Each takes
// and returns scalars and touches no heap box. compiler/norc.fern's
// intrinsics() holds the same set.
var noRCIntrinsics = map[string]bool{
	"__load_u8": true, "__load_i32": true, "__load_i64": true, "__load_ptr": true,
	"__store_u8": true, "__store_i32": true, "__store_i64": true, "__store_ptr": true,
	"__mmio_load8": true, "__mmio_load16": true, "__mmio_load32": true, "__mmio_load64": true,
	"__mmio_store8": true, "__mmio_store16": true, "__mmio_store32": true, "__mmio_store64": true,
	"__mem_load8": true, "__mem_load16": true, "__mem_load32": true, "__mem_load64": true,
	"__mem_store8": true, "__mem_store16": true, "__mem_store32": true, "__mem_store64": true,
	"__fence": true, "__wfi": true, "__hvc": true, "__smc": true, "__boot_info": true, "__sysreg_read": true, "__sysreg_write": true,
	"__cache_clean": true, "__cache_flush": true, "__tlb_flush": true, "__tlb_flush_all": true, "__breakpoint": true,
	"__irq_mask": true, "__irq_unmask": true, "__irq_wait": true, "__irq_pending": true,
	"__port_in8": true, "__port_in16": true, "__port_in32": true, "__port_out8": true, "__port_out16": true, "__port_out32": true,
	"__clz32": true, "__clz64": true, "__ctz32": true, "__ctz64": true,
	"__popcount32": true, "__popcount64": true,
}

// noRCScalars are the scalar types. compiler/norc.fern's scalar() holds the
// same set.
var noRCScalars = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "u8": true, "u16": true, "u32": true, "u64": true,
	"usize": true, "f32": true, "f64": true, "boolean": true, "char": true,
}

func noRCScalar(t ast.Type) bool {
	return t != nil && noRCScalars[t.String()]
}

// interruptSlots are the named slots that a handler can take: the arm64 vector
// table and the x86-64 exceptions. A number from 32 to 255 names an x86-64
// external interrupt. compiler/norc.fern's arm64_slots() and x86_vector() hold
// the same sets.
var interruptSlots = map[string]bool{
	"sync": true, "irq": true, "fiq": true, "serror": true,
	"de": true, "db": true, "nmi": true, "bp": true, "of": true, "br": true, "ud": true, "nm": true, "df": true,
	"ts": true, "np": true, "ss": true, "gp": true, "pf": true, "mf": true, "ac": true, "mc": true, "xm": true, "ve": true, "cp": true,
}

func interruptSlot(slot string) bool {
	if interruptSlots[slot] {
		return true
	}
	n, err := strconv.Atoi(slot)
	return err == nil && n >= 32 && n <= 255 && strconv.Itoa(n) == slot
}

// checkInterruptFunctions reports E103: a handler takes no parameters and
// returns void, names a slot, and takes a slot that no other handler takes.
func (c *checker) checkInterruptFunctions(prog *ast.Program) {
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Interrupt == "" {
			continue
		}
		what := fmt.Sprintf("`@interrupt` function %q", fn.Name)
		if !interruptSlot(fn.Interrupt) {
			c.errfCode(fn.P, "E103", "%s names the slot %q, which is no slot of an arm64 vector table or an x86-64 IDT", what, fn.Interrupt)
		} else if seen[fn.Interrupt] {
			c.errfCode(fn.P, "E103", "%s takes the slot %q, which another handler already takes", what, fn.Interrupt)
		}
		seen[fn.Interrupt] = true
		if _, void := fn.ReturnType.(ast.VoidType); len(fn.Params) > 0 || (fn.ReturnType != nil && !void) {
			c.errfCode(fn.P, "E103", "%s must take no parameters and return void", what)
		}
	}
}

// checkEarlyFunctions reports E104: the @early hook takes no parameters and
// returns void, and a program has at most one. compiler/norc.fern also
// refuses it on a target with no reset entry.
func (c *checker) checkEarlyFunctions(prog *ast.Program) {
	first := ""
	for _, fn := range prog.Funcs {
		if !fn.Early {
			continue
		}
		what := fmt.Sprintf("`@early` function %q", fn.Name)
		if first != "" {
			c.errfCode(fn.P, "E104", "%s is a second hook: %q is the first, and the reset entry calls one", what, first)
		} else {
			first = fn.Name
		}
		if _, void := fn.ReturnType.(ast.VoidType); len(fn.Params) > 0 || (fn.ReturnType != nil && !void) {
			c.errfCode(fn.P, "E104", "%s must take no parameters and return void", what)
		}
	}
}

// checkNoRCFunctions reports E102 in each @norc function: a value that is not
// a scalar, or a call to a function that is not @norc. Such a function
// allocates nothing and does no RC operation.
func (c *checker) checkNoRCFunctions(prog *ast.Program) {
	callable := map[string]bool{}
	for k := range noRCIntrinsics {
		callable[k] = true
	}
	any := false
	for _, fn := range prog.Funcs {
		if fn.NoRC {
			callable[fn.Name] = true
			any = true
		}
	}
	if !any {
		return
	}
	for _, fn := range prog.Funcs {
		if fn.NoRC {
			c.checkNoRC(fn, callable)
		}
	}
}

func (c *checker) checkNoRC(fn *ast.FuncDecl, callable map[string]bool) {
	attr := "norc"
	if fn.Interrupt != "" {
		attr = "interrupt"
	} else if fn.Early {
		attr = "early"
	}
	bad := func(p ast.Position, format string, args ...any) {
		c.errfCode(p, "E102", "`@%s` function %q %s", attr, fn.Name, fmt.Sprintf(format, args...))
	}
	if fn.Receiver != nil || fn.MethodRecv != "" || len(fn.TypeParams) > 0 {
		bad(fn.P, "must be a plain function, not a method or a generic function")
		return
	}
	for _, p := range fn.Params {
		if !noRCScalar(p.Type) {
			bad(p.NamePos, "takes %q of type %s, which is not a scalar", p.Name, p.Type)
		}
	}
	if fn.ReturnType != nil && fn.ReturnType.String() != "void" && !noRCScalar(fn.ReturnType) {
		bad(fn.P, "returns %s, which is not a scalar", fn.ReturnType)
	}
	if fn.Body == nil {
		return
	}
	var visit func(ast.Node) bool
	visit = func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if x.WasAnnotated && !noRCScalar(x.Type) {
				bad(x.P, "declares %q of type %s, which is not a scalar", x.Name, x.Type)
			}
		case *ast.Block:
			if x.Sugar != nil {
				bad(x.P, "may not use a `for` loop, which iterates a value; use `while`")
				return false
			}
		case *ast.For, *ast.ForEach:
			bad(n.Pos(), "may not use a `for` loop, which iterates a value; use `while`")
			return false
		case *ast.Match, *ast.MatchExpr:
			bad(n.Pos(), "may not use `match`")
			return false
		case *ast.Assign:
			if _, ok := x.Target.(*ast.Ident); !ok {
				bad(x.P, "may not write to a field or an element")
				return false
			}
		case *ast.Ident:
			if t := c.noRCIdentTypes[x]; t != nil && !noRCScalar(t) {
				bad(x.P, "uses %q of type %s, which is not a scalar", x.Name, t)
			}
		case *ast.CastExpr:
			if !noRCScalar(x.Target) {
				bad(x.P, "casts to %s, which is not a scalar", x.Target)
			}
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok && x.Method == nil && !id.Local {
				if !callable[id.Name] {
					bad(x.P, "may call only `@norc` functions and the raw memory intrinsics, not %q", id.Name)
				}
				for i, arg := range x.Args {
					// The register of a system-register intrinsic is a literal that the instruction encodes.
					if i == 0 && (id.Name == "__sysreg_read" || id.Name == "__sysreg_write") {
						continue
					}
					ast.Walk(arg, visit)
				}
				return false
			}
			bad(x.P, "may not call a method or a function value")
			return false
		case *ast.StringLit, *ast.FString:
			bad(n.Pos(), "may not use a string")
			return false
		case *ast.ArrayLit:
			bad(x.P, "may not use an array literal")
			return false
		case *ast.MapLit:
			bad(x.P, "may not use a map literal")
			return false
		case *ast.StructLit:
			bad(x.P, "may not use a struct literal")
			return false
		case *ast.TupleLit:
			bad(x.P, "may not use a tuple")
			return false
		case *ast.Lambda:
			bad(x.P, "may not use a closure")
			return false
		case *ast.FieldAccess:
			bad(x.P, "may not use a field access")
			return false
		case *ast.Index:
			bad(x.P, "may not use an index")
			return false
		case *ast.SliceExpr:
			bad(x.P, "may not use a slice")
			return false
		case *ast.Defer:
			bad(x.P, "may not use this statement")
			return false
		}
		return true
	}
	ast.Walk(fn.Body, visit)
}
