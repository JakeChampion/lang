package checker

import (
	"context"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/check/defaultargs"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/stdlib"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

type Error struct {
	Pos     ast.Position
	Span    int
	Note    string
	Msg     string
	Path    string
	ErrCode string
	Fix     *diag.Suggestion
	Related []diag.Label
}

func (e *Error) Error() string          { return fmt.Sprintf("type error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) Length() int            { return e.Span }
func (e *Error) Hint() string           { return e.Note }
func (e *Error) File() string           { return e.Path }
func (e *Error) SetFile(p string)       { e.Path = p }
func (e *Error) Code() string           { return e.ErrCode }

func (e *Error) Suggestion() *diag.Suggestion { return e.Fix }
func (e *Error) Labels() []diag.Label {
	if len(e.Related) == 0 {
		return nil
	}
	return append([]diag.Label{{Pos: e.Pos, Length: e.Span, Kind: diag.LabelPrimary}}, e.Related...)
}

var _ diag.FileSetter = (*Error)(nil)

type Info struct {
	VarTypes            map[*ast.Var]ast.Type
	Target              string
	IntrinsicCalls      map[*ast.Call]IntrinsicCall
	StatefulHandler     bool
	BoxedCells          map[string]bool
	Locals              map[*ast.FuncDecl][]*ast.Var
	FuncSigs            map[string]*ast.FuncType
	OwnFuncs            map[string][]bool
	Structs             map[string]*ast.StructDecl
	Enums               map[string]*ast.EnumDecl
	Resources           map[string]*ast.ResourceDecl
	Methods             map[string]string
	TraitMethods        map[string]string
	MethodOwners        map[string][]string
	MethodInsts         map[string][]string
	MethodDeclSites     map[string]ast.Position
	MethodSources       map[string]string
	ModuleImports       map[string]map[string]bool
	DirectImports       map[string]map[string]bool
	EnumConstructions   map[ast.Expr]EnumConstruction
	GenericFuncs        map[string]*ast.FuncDecl
	GenericStructs      map[string]*ast.StructDecl
	Generics            map[string]ast.GenericDecl
	Traits              map[string]*ast.TraitDecl
	Impls               map[string]map[string]bool
	ImplTraitArgs       map[string]map[string][]ast.Type
	ImplForPattern      map[string]map[string]ast.Type
	AssocBindings       map[string]map[string]ast.Type
	AssocConsts         map[string]bool
	TryShapes           map[string]TryShape
	AssocBindingPattern map[string]map[string]ast.Type
	DynCoercions        map[ast.Expr]DynCoercion
}

func (c *checker) tryEnumWithOk(t ast.EnumType, ok ast.Type) (ast.EnumType, bool) {
	shape, isTry := c.info.TryShapes[t.Name]
	if !isTry {
		return t, false
	}
	ed, haveDecl := c.info.Enums[t.Name]
	if !haveDecl || len(ed.TypeParams) != len(t.Args) {
		return t, false
	}
	var okParam string
	switch p := shape.Ok.(type) {
	case ast.ParamType:
		okParam = p.Name
	case ast.StructType:
		if len(p.Args) == 0 {
			okParam = p.Name
		}
	}
	if okParam == "" {
		return t, false
	}
	for i, tp := range ed.TypeParams {
		if tp != okParam {
			continue
		}
		args := append([]ast.Type{}, t.Args...)
		args[i] = ok
		return ast.EnumType{Name: t.Name, Args: args}, true
	}
	return t, false
}

func (c *checker) tryPayloadType(t ast.EnumType, payload ast.Type) (ast.Type, bool) {
	ed, ok := c.info.Enums[t.Name]
	if !ok {
		return nil, false
	}
	if len(ed.TypeParams) != len(t.Args) {
		return nil, false
	}
	if len(ed.TypeParams) == 0 {
		return payload, true
	}
	sub := make(map[string]ast.Type, len(ed.TypeParams))
	for i, tp := range ed.TypeParams {
		sub[tp] = t.Args[i]
	}
	return substByName(payload, sub), true
}

func (c *checker) notATryTypeMsg(t ast.Type) string {
	if et, ok := t.(ast.EnumType); ok {
		return fmt.Sprintf("`?` operator requires an Option, a Result, or an `@try` enum, got %s — mark it `@try` to make it one", demangle(et.Name))
	}
	return fmt.Sprintf("`?` operator requires an Option, a Result, or an `@try` enum, got %s", t)
}

func (c *checker) registerTryShape(ed *ast.EnumDecl) {
	builtin := (ed.Name == "Option" || ed.Name == "Result") && ed.P == (ast.Position{})
	if !ed.Try && !builtin {
		return
	}
	if len(ed.Variants) != 2 {
		c.errfCode(ed.P, "E078",
			"@try enum %s must have exactly two variants — a success variant then a failure variant — but has %d",
			ed.Name, len(ed.Variants))
		return
	}
	success, failure := ed.Variants[0], ed.Variants[1]
	if len(success.Payloads) != 1 {
		c.errfCode(ed.P, "E078",
			"@try enum %s: the success variant %q must carry exactly one payload, but carries %d",
			ed.Name, success.Name, len(success.Payloads))
		return
	}
	if len(failure.Payloads) > 1 {
		c.errfCode(ed.P, "E078",
			"@try enum %s: the failure variant %q must carry at most one payload, but carries %d",
			ed.Name, failure.Name, len(failure.Payloads))
		return
	}
	shape := TryShape{Ok: success.Payloads[0]}
	if len(failure.Payloads) == 1 {
		shape.Residual = failure.Payloads[0]
	}
	c.info.TryShapes[ed.Name] = shape
}

type TryShape struct {
	Ok       ast.Type
	Residual ast.Type
}

type DynCoercion struct {
	Trait    string
	Traits   []string
	Concrete string
}

func IsReservedName(name string) bool {
	for _, ed := range builtinEnumDecls() {
		if ed.Name == name {
			return true
		}
	}
	for _, sd := range builtinStructDecls() {
		if sd.Name == name {
			return true
		}
	}
	return false
}

func builtinEnumDecls() []*ast.EnumDecl {
	return []*ast.EnumDecl{
		{
			Name:       "Option",
			TypeParams: []string{"T"},
			Variants: []ast.EnumVariant{
				{Name: "Some", Payloads: []ast.Type{ast.ParamType{Name: "T"}}},
				{Name: "None"},
			},
		},
		{
			Name:       "Result",
			TypeParams: []string{"T", "E"},
			Variants: []ast.EnumVariant{
				{Name: "Ok", Payloads: []ast.Type{ast.ParamType{Name: "T"}}},
				{Name: "Err", Payloads: []ast.Type{ast.ParamType{Name: "E"}}},
			},
		},
		{
			Name: "IoError",
			Variants: []ast.EnumVariant{
				{Name: "NotFound", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "PermissionDenied", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "AlreadyExists", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "InvalidUtf8", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "Interrupted"},
				{Name: "Unsupported"},
				{Name: "Other", Payloads: []ast.Type{ast.StringType{}, ast.StringType{}, ast.NumberType{}}},
			},
		},
		{
			Name: "Body",
			Variants: []ast.EnumVariant{
				{Name: "BodyText", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "BodyBytes", Payloads: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}}},
				{Name: "BodyStream", Payloads: []ast.Type{ast.StructType{Name: "Stream"}}},
				{Name: "BodyFile", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "BodyChunks", Payloads: []ast.Type{ast.StructType{Name: "ChunkProducer"}}},
				{Name: "BodyFileRanges", Payloads: []ast.Type{ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}}, ast.ArrayType{Elem: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}}}},
			},
		},
	}
}

func builtinStructDecls() []*ast.StructDecl {
	return []*ast.StructDecl{
		{
			Name:   "Reader",
			Fields: []ast.Param{{Name: "fd", Type: ast.NumberType{}}},
		},
		{
			Name:   "Writer",
			Fields: []ast.Param{{Name: "fd", Type: ast.NumberType{}}},
		},
		{
			Name:   "Dir",
			Fields: []ast.Param{{Name: "fd", Type: ast.NumberType{}}},
		},
		{
			Name: "HttpRequest",
			Fields: []ast.Param{
				{Name: "method", Type: ast.StringType{}},
				{Name: "path", Type: ast.StringType{}},
				{Name: "body", Type: ast.StructType{Name: "Stream"}},
				{Name: "headers", Type: ast.StructType{Name: "HeaderMap"}},
				{Name: "trailers", Type: ast.StructType{Name: "HeaderMap"}},
			},
		},
		{
			Name: "HttpResponse",
			Fields: []ast.Param{
				{Name: "status", Type: ast.NumberType{}},
				{Name: "body", Type: ast.EnumType{Name: "Body"}},
				{Name: "headers", Type: ast.StructType{Name: "HeaderMap"}},
				{Name: "trailers", Type: ast.StructType{Name: "HeaderMap"}},
				{Name: "background", Type: ast.EnumType{Name: "Option", Args: []ast.Type{&ast.FuncType{Params: []ast.Type{}, Result: ast.VoidType{}}}}},
			},
		},
		{
			Name: "HeaderMap",
			Fields: []ast.Param{
				{Name: "names", Type: ast.ArrayType{Elem: ast.StringType{}}},
				{Name: "values", Type: ast.ArrayType{Elem: ast.StringType{}}},
			},
		},
		{
			Name: "Stream",
			Fields: []ast.Param{
				{Name: "data", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
				{Name: "pos", Type: ast.NumberType{}},
				{Name: "source", Type: ast.EnumType{Name: "Option", Args: []ast.Type{ast.StructType{Name: "BodySource"}}}},
			},
		},
		{
			Name: "BodySource",
			Fields: []ast.Param{
				{Name: "next", Type: &ast.FuncType{
					Result: ast.EnumType{Name: "Option", Args: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}}},
				}},
				{Name: "fault", Type: ast.StructType{Name: "Cell", Args: []ast.Type{ast.NumberType{}}}},
				{Name: "send", Type: &ast.FuncType{
					Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
					Result: ast.NumberType{},
				}},
			},
		},
		{
			Name: "ChunkProducer",
			Fields: []ast.Param{
				{Name: "next", Type: &ast.FuncType{
					Params: []ast.Type{ast.NumberType{}},
					Result: ast.EnumType{Name: "Option", Args: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}}},
				}},
				{Name: "idle_ms", Type: ast.NumberType{}},
				{Name: "filler", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
				{Name: "trailers", Type: &ast.FuncType{Params: []ast.Type{}, Result: ast.StructType{Name: "HeaderMap"}}},
			},
		},
		{
			Name: "BytesWriter",
			Fields: []ast.Param{
				{Name: "data", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
			},
		},
		{
			Name: "Instant",
			Fields: []ast.Param{
				{Name: "sec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "nsec", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Date",
			Fields: []ast.Param{
				{Name: "year", Type: ast.NumberType{}},
				{Name: "month", Type: ast.NumberType{}},
				{Name: "day", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Time",
			Fields: []ast.Param{
				{Name: "hour", Type: ast.NumberType{}},
				{Name: "minute", Type: ast.NumberType{}},
				{Name: "second", Type: ast.NumberType{}},
				{Name: "nsec", Type: ast.NumberType{}},
			},
		},
		{
			Name: "DateTime",
			Fields: []ast.Param{
				{Name: "date", Type: ast.StructType{Name: "Date"}},
				{Name: "time", Type: ast.StructType{Name: "Time"}},
			},
		},
		{
			Name: "TimeZone",
			Fields: []ast.Param{
				{Name: "name", Type: ast.StringType{}},
				{Name: "offset_seconds", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Zoned",
			Fields: []ast.Param{
				{Name: "instant", Type: ast.StructType{Name: "Instant"}},
				{Name: "zone", Type: ast.StructType{Name: "TimeZone"}},
			},
		},
		{
			Name: "Span",
			Fields: []ast.Param{
				{Name: "years", Type: ast.NumberType{}},
				{Name: "months", Type: ast.NumberType{}},
				{Name: "weeks", Type: ast.NumberType{}},
				{Name: "days", Type: ast.NumberType{}},
				{Name: "hours", Type: ast.NumberType{}},
				{Name: "minutes", Type: ast.NumberType{}},
				{Name: "seconds", Type: ast.NumberType{}},
				{Name: "nanos", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Duration",
			Fields: []ast.Param{
				{Name: "sec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "nsec", Type: ast.NumberType{}},
			},
		},
		{
			Name: "ProcessResult",
			Fields: []ast.Param{
				{Name: "stdout", Type: ast.StringType{}},
				{Name: "stderr", Type: ast.StringType{}},
				{Name: "exit_code", Type: ast.NumberType{}},
			},
		},
		{
			Name: "FileStat",
			Fields: []ast.Param{
				{Name: "is_file", Type: ast.BoolType{}},
				{Name: "is_dir", Type: ast.BoolType{}},
				{Name: "size", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "mode", Type: ast.NumberType{Width: 32, Signed: false}},
				{Name: "nlink", Type: ast.NumberType{Width: 32, Signed: false}},
				{Name: "uid", Type: ast.NumberType{Width: 32, Signed: false}},
				{Name: "gid", Type: ast.NumberType{Width: 32, Signed: false}},
				{Name: "dev", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "rdev", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "ino", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "blksize", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "blocks", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "atime", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "atime_nsec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "mtime", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "mtime_nsec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "ctime", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "ctime_nsec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "btime", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "btime_nsec", Type: ast.NumberType{Width: 64, Signed: true}},
			},
		},
		{
			Name: "DirEntry",
			Fields: []ast.Param{
				{Name: "name", Type: ast.StringType{}},
				{Name: "ino", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "kind", Type: ast.NumberType{Width: 32, Signed: false}},
			},
		},
		{
			Name: "FsStat",
			Fields: []ast.Param{
				{Name: "block_size", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "blocks", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "blocks_free", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "blocks_avail", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "files", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "files_free", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "name_max", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "path_max", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "fs_type", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "fsid", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "frag_size", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "fs_type_name", Type: ast.StringType{}},
			},
		},
		{
			Name: "MountEntry",
			Fields: []ast.Param{
				{Name: "source", Type: ast.StringType{}},
				{Name: "target", Type: ast.StringType{}},
				{Name: "fstype", Type: ast.StringType{}},
				{Name: "dev", Type: ast.NumberType{Width: 64, Signed: true}},
			},
		},
		{
			Name: "WinSize",
			Fields: []ast.Param{
				{Name: "rows", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "cols", Type: ast.NumberType{Width: 64, Signed: true}},
			},
		},
		{
			Name: "Pipe",
			Fields: []ast.Param{
				{Name: "r", Type: ast.StructType{Name: "Reader"}},
				{Name: "w", Type: ast.StructType{Name: "Writer"}},
			},
		},
		{
			Name:       "Map",
			TypeParams: []string{"K", "V"},
			Fields: []ast.Param{
				{Name: "data", Type: ast.NumberType{}},
			},
		},
		{
			Name:       "MapIter",
			TypeParams: []string{"K", "V"},
			Fields: []ast.Param{
				{Name: "data", Type: ast.NumberType{Width: 64}},
				{Name: "i", Type: ast.NumberType{}},
				{Name: "pad", Type: ast.NumberType{}},
				{Name: "map", Type: ast.NumberType{Width: 64}},
			},
		},
		{
			Name:       "Cell",
			TypeParams: []string{"T"},
			Fields: []ast.Param{
				{Name: "value", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Url",
			Fields: []ast.Param{
				{Name: "scheme", Type: ast.StringType{}},
				{Name: "host", Type: ast.StringType{}},
				{Name: "port", Type: ast.NumberType{}},
				{Name: "path", Type: ast.StringType{}},
				{Name: "query", Type: ast.StringType{}},
				{Name: "fragment", Type: ast.StringType{}},
			},
		},
	}
}

func Check(prog *ast.Program) (*Info, error) {
	return CheckContext(context.Background(), prog)
}

func CheckTarget(prog *ast.Program, target string) (*Info, error) {
	return checkTarget(context.Background(), prog, target)
}

func CheckContext(ctx context.Context, prog *ast.Program) (*Info, error) {
	return checkTarget(ctx, prog, "")
}

func checkTarget(ctx context.Context, prog *ast.Program, target string) (*Info, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if hasHandleDecl(prog) {
		adaptResultHandler(prog)
	}
	if targetHasEnv(target) {
		configFromEnv(prog)
	}
	info, err := checkImpl(ctx, prog, target)
	if info != nil {
		info.Target = target
	}
	return info, err
}

func targetHasEnv(target string) bool {
	d := platforms.ForTarget(target)
	return d == nil || slices.Contains(d.Capabilities, "env")
}

func configFromEnv(prog *ast.Program) {
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if call, ok := n.(*ast.Call); ok {
			if id, ok := call.Callee.(*ast.Ident); ok && id.Name == "config_get" {
				call.SourceCallee = id.Name
				id.Name = "env"
			}
		}
		return true
	})
}

func targetSupervises(target string) bool {
	d := platforms.ForTarget(target)
	return d == nil || slices.Contains(d.Capabilities, "proc")
}

const WasiHTTPStatefulHandler = "a wasi:http handler takes `(req, plat)` and no state: the proxy world starts a fresh instance for every request, so nothing carries from one to the next; build a stateful handler for a native target"

const HandlerNeedsServe = "a top-level `handle` requires `std/serve`: add `import \"std/serve\";`"

func wasiHTTPHandlerShape(n int) string {
	if n == 3 {
		return WasiHTTPStatefulHandler
	}
	return fmt.Sprintf("a wasi:http handler takes `(req, plat)`; this `handle` takes %d parameter(s)", n)
}

func checkImpl(ctx context.Context, prog *ast.Program, target string) (*Info, error) {
	supervised := targetSupervises(target)
	wasiHTTP := target == "wasm32-wasi-http"
	userEnums := map[string]*ast.EnumDecl{}
	for _, ed := range prog.Enums {
		userEnums[ed.Name] = ed
	}
	userStructs := map[string]*ast.StructDecl{}
	for _, sd := range prog.Structs {
		userStructs[sd.Name] = sd
	}
	var shadowedEnums []*ast.EnumDecl
	var shadowedStructs []*ast.StructDecl
	shadow := func(name string) bool {
		if ed, ok := userEnums[name]; ok && ed.P != (ast.Position{}) {
			shadowedEnums = append(shadowedEnums, ed)
			return true
		}
		if sd, ok := userStructs[name]; ok && sd.P != (ast.Position{}) {
			shadowedStructs = append(shadowedStructs, sd)
			return true
		}
		return false
	}
	{
		var inject []*ast.EnumDecl
		for _, ed := range builtinEnumDecls() {
			if existing, dup := userEnums[ed.Name]; dup && existing.P == (ast.Position{}) {
				continue
			}
			if shadow(ed.Name) {
				continue
			}
			inject = append(inject, ed)
		}
		if len(inject) > 0 {
			prog.Enums = append(inject, prog.Enums...)
		}
	}
	{
		var inject []*ast.StructDecl
		for _, sd := range builtinStructDecls() {
			if existing, dup := userStructs[sd.Name]; dup && existing.P == (ast.Position{}) {
				continue
			}
			if shadow(sd.Name) {
				continue
			}
			inject = append(inject, sd)
		}
		if len(inject) > 0 {
			prog.Structs = append(inject, prog.Structs...)
		}
	}
	c := &checker{
		funcSources: map[string]string{},
		entryModule: prog.EntryModule,
		info: &Info{
			VarTypes:            map[*ast.Var]ast.Type{},
			Locals:              map[*ast.FuncDecl][]*ast.Var{},
			FuncSigs:            map[string]*ast.FuncType{},
			Structs:             map[string]*ast.StructDecl{},
			Enums:               map[string]*ast.EnumDecl{},
			Resources:           map[string]*ast.ResourceDecl{},
			Methods:             map[string]string{},
			TraitMethods:        map[string]string{},
			MethodOwners:        map[string][]string{},
			MethodInsts:         map[string][]string{},
			MethodDeclSites:     map[string]ast.Position{},
			MethodSources:       map[string]string{},
			ModuleImports:       prog.ModuleImports,
			DirectImports:       prog.DirectImports,
			GenericFuncs:        map[string]*ast.FuncDecl{},
			GenericStructs:      map[string]*ast.StructDecl{},
			Generics:            map[string]ast.GenericDecl{},
			Traits:              map[string]*ast.TraitDecl{},
			Impls:               map[string]map[string]bool{},
			ImplTraitArgs:       map[string]map[string][]ast.Type{},
			ImplForPattern:      map[string]map[string]ast.Type{},
			AssocBindings:       map[string]map[string]ast.Type{},
			AssocConsts:         map[string]bool{},
			TryShapes:           map[string]TryShape{},
			AssocBindingPattern: map[string]map[string]ast.Type{},
		},
		variantOf:            map[string][]variantRef{},
		shadowedGenericCalls: map[*ast.Call]bool{},
		reservedShadows:      map[string]bool{},
	}
	if prog.LoadedStdlibPaths != nil && !prog.LoadedStdlibPaths["stdlib://core/map.fern"] {
		c.requireMapImport = true
	}
	for _, fe := range defaultargs.Fill(prog) {
		c.errfCode(fe.Pos, fe.Code, "%s", fe.Msg)
	}

	for _, ed := range shadowedEnums {
		c.errfCode(ed.P, "E010", "enum %q is a reserved built-in name and cannot be redeclared", ed.Name)
		c.reservedShadows[ed.Name] = true
	}
	for _, sd := range shadowedStructs {
		c.errfCode(sd.P, "E010", "struct %q is a reserved built-in name and cannot be redeclared", sd.Name)
		c.reservedShadows[sd.Name] = true
	}

	for _, sd := range prog.Structs {
		if _, dup := c.info.Structs[sd.Name]; dup {
			c.errfCode(sd.P, "E006", "struct %q redeclared", sd.Name)
			continue
		}
		seen := map[string]bool{}
		for _, f := range sd.Fields {
			if seen[f.Name] {
				c.errfCode(sd.P, "E007", "duplicate field %q in struct %s", f.Name, sd.Name)
			}
			seen[f.Name] = true
		}
		c.info.Structs[sd.Name] = sd
		if len(sd.TypeParams) > 0 && !isRuntimeGenericStruct(sd.Name) {
			c.info.GenericStructs[sd.Name] = sd
			c.info.Generics[sd.Name] = sd
		}
	}

	for _, ud := range prog.Unions {
		if _, dup := c.info.Structs[ud.Name]; dup {
			c.errfCode(ud.P, "E016", "union %q collides with a struct of the same name", ud.Name)
			continue
		}
		if _, dup := c.info.Enums[ud.Name]; dup {
			c.errfCode(ud.P, "E016", "union %q collides with an enum of the same name", ud.Name)
			continue
		}
		variants := make([]ast.EnumVariant, 0, len(ud.Members))
		seen := map[string]bool{}
		ok := true
		for _, member := range ud.Members {
			if seen[member.Name] {
				c.errfCode(ud.P, "E016", "duplicate member %q in union %s", member.Name, ud.Name)
				ok = false
				continue
			}
			seen[member.Name] = true
			sd, isStruct := c.info.Structs[member.Name]
			if !isStruct {
				c.errfCode(ud.P, "E016", "union %s member %q does not name a struct in scope", ud.Name, member.Name)
				ok = false
				continue
			}
			if len(member.Args) != len(sd.TypeParams) {
				if len(sd.TypeParams) == 0 {
					c.errfCode(ud.P, "E016", "union %s member %q is not generic but was given %d type argument(s)", ud.Name, member.Name, len(member.Args))
				} else {
					params := strings.Join(sd.TypeParams, ", ")
					c.errfCode(ud.P, "E016", "union %s member %q takes %d type argument(s), got %d; a generic struct member needs them supplied (e.g. `type %s[%s] = %s[%s] | …`)",
						ud.Name, member.Name, len(sd.TypeParams), len(member.Args), ud.Name, params, member.Name, params)
				}
				ok = false
				continue
			}
			variants = append(variants, ast.EnumVariant{
				P:        ud.P,
				Name:     member.Name,
				Payloads: []ast.Type{member},
			})
		}
		if !ok {
			continue
		}
		prog.Enums = append(prog.Enums, &ast.EnumDecl{
			P:            ud.P,
			Name:         ud.Name,
			TypeParams:   ud.TypeParams,
			Variants:     variants,
			Public:       ud.Public,
			SourceModule: ud.SourceModule,
		})
	}
	prog.Unions = nil

	for _, ed := range prog.Enums {
		if _, dup := c.info.Enums[ed.Name]; dup {
			c.errfCode(ed.P, "E006", "enum %q redeclared", ed.Name)
			continue
		}
		c.info.Enums[ed.Name] = ed
		seen := map[string]bool{}
		for i := range ed.Variants {
			v := &ed.Variants[i]
			if seen[v.Name] {
				c.errfCode(v.P, "E017", "duplicate variant %q in enum %s", v.Name, ed.Name)
				continue
			}
			seen[v.Name] = true
			c.variantOf[v.Name] = append(c.variantOf[v.Name], variantRef{
				enumName:  ed.Name,
				index:     i,
				payloads:  v.Payloads,
				srcModule: ed.SourceModule,
			})
		}
		c.registerTryShape(ed)
	}

	for _, rd := range prog.Resources {
		if _, dup := c.info.Resources[rd.Name]; dup {
			c.errfCode(rd.P, "E006", "resource %q redeclared", rd.Name)
			continue
		}
		if _, isStruct := c.info.Structs[rd.Name]; isStruct {
			c.errfCode(rd.P, "E006", "resource %q conflicts with a struct of the same name", rd.Name)
			continue
		}
		if _, isEnum := c.info.Enums[rd.Name]; isEnum {
			c.errfCode(rd.P, "E006", "resource %q conflicts with an enum of the same name", rd.Name)
			continue
		}
		c.info.Resources[rd.Name] = rd
	}

	c.traitNames = desugarTraitParams(prog)

	for _, fn := range prog.Funcs {
		if fn.Receiver == nil {
			continue
		}
		var vars []string
		seen := map[string]bool{}
		for _, tp := range fn.TypeParams {
			seen[tp] = true
		}
		c.collectFreeTypeVars(fn.Receiver.Type, &vars, seen, fn.SourceModule)
		fn.TypeParams = append(vars, fn.TypeParams...)
	}

	c.consts = map[string]*ast.ConstDecl{}
	c.constTypes = map[string]ast.Type{}
	c.constBusy = map[string]bool{}
	for _, cd := range prog.Consts {
		c.consts[cd.Name] = cd
	}

	c.resolveTypeNames(prog)

	c.validateKnownTypes(prog)

	c.info.FuncSigs["putchar"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["print"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["write"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["eprint"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["args"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.ArrayType{Elem: ast.StringType{}},
	}
	c.info.FuncSigs["set_args"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.StringType{}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["environ"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.ArrayType{Elem: ast.StringType{}},
	}
	c.info.FuncSigs["env"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}},
	}
	c.info.FuncSigs["config_get"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}},
	}
	c.info.FuncSigs["exit"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}
	bufH := ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}
	c.info.FuncSigs["buf_new"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: bufH,
	}
	c.info.FuncSigs["buf_push"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.StringType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_range"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.StringType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_bytes_range"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_mapped"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_bytes_mapped"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_filtered"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_bytes_filtered"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_expanded"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_bytes_expanded"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_byte"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_push_u64"] = &ast.FuncType{
		Params: []ast.Type{bufH, ast.NumberType{Width: 64, Signed: false}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_len"] = &ast.FuncType{
		Params: []ast.Type{bufH},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["buf_take"] = &ast.FuncType{
		Params: []ast.Type{bufH},
		Result: ast.StringType{},
	}
	c.info.FuncSigs["buf_take_bytes"] = &ast.FuncType{
		Params: []ast.Type{bufH},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
	}
	c.info.FuncSigs["buf_free"] = &ast.FuncType{
		Params: []ast.Type{bufH},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["buf_clear"] = &ast.FuncType{
		Params: []ast.Type{bufH},
		Result: ast.VoidType{},
	}
	u8ArrType := ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}
	c.info.FuncSigs["__rc_inc"] = &ast.FuncType{
		Params: []ast.Type{u8ArrType},
		Result: u8ArrType,
	}
	c.info.FuncSigs["__rc_dec"] = &ast.FuncType{
		Params: []ast.Type{u8ArrType},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__rc_get"] = &ast.FuncType{
		Params: []ast.Type{u8ArrType},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__rc_underflow_count"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__arr_push_shared_count"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__arr_push_shared_bytes"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["__map_hash_seed"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__heap_bump_bytes"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["__heap_alloc_count"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	for _, n := range []string{"__ct_secret", "__ct_public"} {
		c.info.FuncSigs[n] = &ast.FuncType{
			Params: []ast.Type{ast.SliceType{Elem: ast.NumberType{Width: 8, Signed: false}}},
			Result: ast.VoidType{},
		}
	}
	u8View := ast.SliceType{Elem: ast.NumberType{Width: 8, Signed: false}}
	u8Arr := ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}
	c.info.FuncSigs["__bytes_owned"] = &ast.FuncType{Params: []ast.Type{u8View}, Result: u8Arr}
	rangeBound := ast.NumberType{Width: 32, Signed: true}
	c.info.FuncSigs["__bytes_range"] = &ast.FuncType{Params: []ast.Type{u8View, rangeBound, rangeBound}, Result: u8Arr}
	c.info.FuncSigs["__bytes_into"] = &ast.FuncType{Params: []ast.Type{u8Arr, rangeBound, u8View, rangeBound, rangeBound}, Result: u8Arr}
	c.info.FuncSigs["__aes_expand_key"] = &ast.FuncType{Params: []ast.Type{u8View}, Result: u8Arr}
	c.info.FuncSigs["__aes_ctr32"] = &ast.FuncType{Params: []ast.Type{u8View, u8View, u8View}, Result: u8Arr}
	c.info.FuncSigs["__ghash"] = &ast.FuncType{Params: []ast.Type{u8View, u8View, u8View}, Result: u8Arr}
	for _, n := range []string{"__aes_gcm_seal", "__aes_gcm_open"} {
		c.info.FuncSigs[n] = &ast.FuncType{Params: []ast.Type{u8View, u8View, u8View, u8View, u8View}, Result: u8Arr}
	}
	c.info.FuncSigs["__sha256_hw"] = &ast.FuncType{Params: []ast.Type{}, Result: ast.BoolType{}}
	c.info.FuncSigs["__sha256_hw_blocks"] = &ast.FuncType{Params: []ast.Type{u8View, u8View}, Result: u8Arr}
	c.info.FuncSigs["__sha1_hw"] = &ast.FuncType{Params: []ast.Type{}, Result: ast.BoolType{}}
	c.info.FuncSigs["__sha1_hw_blocks"] = &ast.FuncType{Params: []ast.Type{u8View, u8View}, Result: u8Arr}
	for _, n := range []string{"__clz32", "__ctz32", "__popcount32"} {
		c.info.FuncSigs[n] = &ast.FuncType{
			Params: []ast.Type{ast.NumberType{Width: 32, Signed: false}},
			Result: ast.NumberType{Width: 32, Signed: true},
		}
	}
	for _, n := range []string{"__clz64", "__ctz64", "__popcount64"} {
		c.info.FuncSigs[n] = &ast.FuncType{
			Params: []ast.Type{ast.NumberType{Width: 64, Signed: false}},
			Result: ast.NumberType{Width: 32, Signed: true},
		}
	}
	c.info.FuncSigs["__mulhi_u64"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: false}, ast.NumberType{Width: 64, Signed: false}},
		Result: ast.NumberType{Width: 64, Signed: false},
	}
	c.info.FuncSigs["__memchr"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__count_byte_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8}}, ast.NumberType{}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__sum_bytes_array"] = &ast.FuncType{
		Params: []ast.Type{ast.SliceType{Elem: ast.NumberType{Width: 8}}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__crc32_cksum_array"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}, ast.SliceType{Elem: ast.NumberType{Width: 8}}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__bsd_sum_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.SliceType{Elem: ast.NumberType{Width: 8}}, ast.NumberType{}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__memchr_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__rmemchr_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__mismatch_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8}}, ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8}}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__mismatch_bytes_str"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8}}, ast.NumberType{}, ast.StringType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__ascii_run"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__scan_set"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__scan_set_bytes"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
			ast.NumberType{Width: 32, Signed: true},
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__bsd_sum"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__str_hash"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__count_runs"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__count_runs_bytes"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
			ast.NumberType{Width: 32, Signed: true},
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__rmemchr"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__count_byte"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__sum_bytes"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__scale_f64"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: ast.FloatType{Width: 64}},
			ast.FloatType{Width: 64},
		},
		Result: ast.ArrayType{Elem: ast.FloatType{Width: 64}},
	}
	c.info.FuncSigs["__outer_mul_f64"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.FloatType{Width: 64}}, ast.ArrayType{Elem: ast.FloatType{Width: 64}}},
		Result: ast.ArrayType{Elem: ast.FloatType{Width: 64}},
	}
	c.info.FuncSigs["__inner_mul_add_f64"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.FloatType{Width: 64}}, ast.ArrayType{Elem: ast.FloatType{Width: 64}}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.FloatType{Width: 64}},
		Result: ast.ArrayType{Elem: ast.FloatType{Width: 64}},
	}
	c.info.FuncSigs["__crc32_cksum"] = &ast.FuncType{
		Params: []ast.Type{
			ast.NumberType{Width: 32, Signed: true},
			ast.StringType{},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__mismatch"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["__heap_mark"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["__heap_release_to"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}
	i32T := ast.NumberType{}
	for name, sig := range map[string]*ast.FuncType{
		"__task_new":           {Params: []ast.Type{}, Result: i32T},
		"__task_free":          {Params: []ast.Type{i32T}, Result: ast.VoidType{}},
		"__task_cur":           {Params: []ast.Type{}, Result: i32T},
		"__task_enter":         {Params: []ast.Type{i32T, i32T}, Result: ast.VoidType{}},
		"__task_leave":         {Params: []ast.Type{}, Result: i32T},
		"__task_park":          {Params: []ast.Type{ast.ArrayType{Elem: i32T}, i32T}, Result: i32T},
		"__task_wait":          {Params: []ast.Type{i32T}, Result: ast.ArrayType{Elem: i32T}},
		"__task_timeout":       {Params: []ast.Type{i32T}, Result: i32T},
		"__task_set_ready":     {Params: []ast.Type{i32T, i32T}, Result: ast.VoidType{}},
		"__task_set_cancelled": {Params: []ast.Type{i32T}, Result: ast.VoidType{}},
		"__task_cancelled":     {Params: []ast.Type{}, Result: i32T},
	} {
		c.info.FuncSigs[name] = sig
	}
	c.info.FuncSigs["f32_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.FloatType{Width: 32}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["f32_from_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.FloatType{Width: 32},
	}
	f64ToF64Builtin := &ast.FuncType{
		Params: []ast.Type{ast.FloatType{Width: 64}},
		Result: ast.FloatType{Width: 64},
	}
	c.info.FuncSigs["__sqrt_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__floor_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__ceil_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__round_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__trunc_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__abs_f64"] = f64ToF64Builtin
	c.registerLaneBuiltins()
	c.info.FuncSigs["f64_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.FloatType{Width: 64}},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["f64_from_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.FloatType{Width: 64},
	}
	c.info.FuncSigs["random_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
	}
	c.info.FuncSigs["random_i32"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_listen"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_accept"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_local_port"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	u8s := ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}
	c.info.FuncSigs["tcp_listen_with"] = &ast.FuncType{
		Params: []ast.Type{u8s, ast.NumberType{}, ast.NumberType{}, ast.BoolType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_socket_ctl"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_sendfile"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_send_buf"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_connect_with"] = &ast.FuncType{
		Params: []ast.Type{u8s, ast.NumberType{}, ast.BoolType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["unix_listen"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["unix_connect"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["reactor_new"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["reactor_ctl"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["reactor_wait"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{}}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["ring_new"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["ring_ctl"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["ring_wait"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.ArrayType{Elem: ast.NumberType{}}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["ring_recv"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["ring_send_zc"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_ktls"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_recv_into"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_send_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_close"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_pollable"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["poll"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{}}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["timer_fd"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["wasm_timer_pollable"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["wasm_block"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["wasm_poll"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["wasm_pollable_drop"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["udp_bind"] = &ast.FuncType{
		Params: []ast.Type{u8s, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["udp_bind_with"] = &ast.FuncType{
		Params: []ast.Type{u8s, ast.NumberType{}, ast.BoolType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["udp_connect"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, u8s, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["udp_sendto"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, u8s, ast.NumberType{}, ast.StringType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["udp_sendto_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, u8s, ast.NumberType{}, u8s},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["udp_recvfrom"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, u8s, u8s},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["read_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["read_file_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["write_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["write_file_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{ast.VoidType{}, ast.EnumType{Name: "IoError"}}},
	}
	c.info.FuncSigs["write_file_exec"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	readerType := ast.StructType{Name: "Reader"}
	writerType := ast.StructType{Name: "Writer"}
	ioErrType := ast.EnumType{Name: "IoError"}
	voidIoResult := ast.EnumType{Name: "Result", Args: []ast.Type{ast.VoidType{}, ioErrType}}
	c.info.FuncSigs["open_reader"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{readerType, ioErrType}},
	}
	c.info.FuncSigs["open_writer"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{writerType, ioErrType}},
	}
	c.info.FuncSigs["open_appender"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{writerType, ioErrType}},
	}
	c.info.FuncSigs["open_exclusive"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{writerType, ioErrType}},
	}
	c.info.FuncSigs["open_reader_with"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{readerType, ioErrType}},
	}
	c.info.FuncSigs["open_writer_with"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{writerType, ioErrType}},
	}
	c.info.FuncSigs["now_unix_ms"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["monotonic_ns"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["now_ns"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["clock_resolution"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["clock_set"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{Width: 64, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["sleep_ms"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["sleep_ns"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["proc_fork"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["proc_waitpid"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["proc_waitpid_nohang"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["proc_waitpid_status"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["proc_exec"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.ArrayType{Elem: ast.StringType{}}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["proc_exec_as"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.ArrayType{Elem: ast.StringType{}}, ast.ArrayType{Elem: ast.StringType{}}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["pipe"] = &ast.FuncType{
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{ast.StructType{Name: "Pipe"}, ioErrType}},
	}
	c.info.FuncSigs["statfs"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StructType{Name: "FsStat"},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["mounts"] = &ast.FuncType{
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.StructType{Name: "MountEntry"}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["sysctl"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["rlimit_nofile"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["disable_core_dumps"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.BoolType{},
	}
	c.info.FuncSigs["process_alive"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.BoolType{},
	}
	c.info.FuncSigs["signal_send"] = &ast.FuncType{
		Params: []ast.Type{
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["set_process_group"] = &ast.FuncType{
		Params: []ast.Type{
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["temp_dir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["read_dir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.StringType{}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["read_dir_all"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.StringType{}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["read_dir_ino"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.StructType{Name: "DirEntry"}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["read_dir_ino_all"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.StructType{Name: "DirEntry"}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["stat"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StructType{Name: "FileStat"},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["lstat"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StructType{Name: "FileStat"},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["access"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["geteuid"] = &ast.FuncType{
		Params: nil,
		Result: ast.NumberType{Width: 32, Signed: false},
	}
	c.info.FuncSigs["getegid"] = &ast.FuncType{
		Params: nil,
		Result: ast.NumberType{Width: 32, Signed: false},
	}
	c.info.FuncSigs["__account_entry"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}, ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}},
		Result: ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"},
	}
	c.info.FuncSigs["getuid"] = &ast.FuncType{
		Params: nil,
		Result: ast.NumberType{Width: 32, Signed: false},
	}
	c.info.FuncSigs["getgid"] = &ast.FuncType{
		Params: nil,
		Result: ast.NumberType{Width: 32, Signed: false},
	}
	c.info.FuncSigs["getgroups"] = &ast.FuncType{
		Params: nil,
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}},
	}
	c.info.FuncSigs["hostname"] = &ast.FuncType{
		Params: nil,
		Result: ast.StringType{},
	}
	c.info.FuncSigs["uname_field"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.StringType{},
	}
	c.info.FuncSigs["getcwd"] = &ast.FuncType{
		Params: nil,
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["chdir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["chroot"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["setgroups"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["setgid"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["setuid"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["cpu_count"] = &ast.FuncType{
		Params: nil,
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["signal_ignore"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["signal_default"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["signal_catch"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["signal_taken"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.BoolType{},
	}
	c.info.FuncSigs["signal_catch_interrupting"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["signal_raise"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["signal_mask"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}, ast.NumberType{Width: 64, Signed: true}},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["signal_disposition"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["remove_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["create_dir_all"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["create_dir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["remove_dir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["create_link"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["clone_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["create_symlink"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["read_link"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["getxattr"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["lgetxattr"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	for _, name := range []string{"setxattr", "lsetxattr"} {
		c.info.FuncSigs[name] = &ast.FuncType{
			Params: []ast.Type{ast.StringType{}, ast.StringType{}, ast.StringType{}},
			Result: ast.EnumType{Name: "Result", Args: []ast.Type{
				ast.VoidType{},
				ast.EnumType{Name: "IoError"},
			}},
		}
	}
	for _, name := range []string{"getxattr_bytes", "lgetxattr_bytes"} {
		c.info.FuncSigs[name] = &ast.FuncType{
			Params: []ast.Type{ast.StringType{}, ast.StringType{}},
			Result: ast.EnumType{Name: "Result", Args: []ast.Type{
				ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.EnumType{Name: "IoError"},
			}},
		}
	}
	for _, name := range []string{"setxattr_bytes", "lsetxattr_bytes"} {
		c.info.FuncSigs[name] = &ast.FuncType{
			Params: []ast.Type{ast.StringType{}, ast.StringType{}, ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
			Result: ast.EnumType{Name: "Result", Args: []ast.Type{
				ast.VoidType{}, ast.EnumType{Name: "IoError"},
			}},
		}
	}
	c.info.FuncSigs["rename"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["rename_noreplace"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["rename_exchange"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["chmod"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["chmod_at"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}, ast.BoolType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["truncate"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{Width: 64, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["mknod"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["chown_at"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 32, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
			ast.BoolType{},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["set_file_times"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.NumberType{Width: 64, Signed: true},
			ast.NumberType{Width: 64, Signed: true},
			ast.NumberType{Width: 64, Signed: true},
			ast.NumberType{Width: 64, Signed: true},
			ast.NumberType{Width: 32, Signed: true},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["umask"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["priority"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["set_priority"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["remove_dir_all"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	procResult := ast.StructType{Name: "ProcessResult"}
	c.info.FuncSigs["subprocess"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.ArrayType{Elem: ast.StringType{}},
			ast.StringType{},
		},
		Result: procResult,
	}
	c.info.FuncSigs["stdin"] = &ast.FuncType{Params: []ast.Type{}, Result: readerType}
	c.info.FuncSigs["stdout"] = &ast.FuncType{Params: []ast.Type{}, Result: writerType}
	c.info.FuncSigs["stderr"] = &ast.FuncType{Params: []ast.Type{}, Result: writerType}
	c.info.FuncSigs["isatty"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.BoolType{},
	}
	c.info.FuncSigs["window_size"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StructType{Name: "WinSize"},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["set_window_size"] = &ast.FuncType{
		Params: []ast.Type{
			ast.NumberType{}, ast.NumberType{}, ast.NumberType{},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{}, ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["set_extproc"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.BoolType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{}, ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["termios_get"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["termios_set"] = &ast.FuncType{
		Params: []ast.Type{
			ast.NumberType{},
			ast.NumberType{},
			ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}},
		},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.VoidType{}, ast.EnumType{Name: "IoError"},
		}},
	}
	c.info.FuncSigs["target_os"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.StringType{},
	}
	c.info.FuncSigs["target_arch"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.StringType{},
	}
	registerStructMethod := func(structName, methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_" + structName + "_" + methodName
		c.info.Methods[structName+"."+methodName] = mangled
		fullParams := append([]ast.Type{ast.StructType{Name: structName}}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	registerStructMethod("Reader", "read_chunk", []ast.Type{ast.NumberType{}},
		ast.EnumType{Name: "Result", Args: []ast.Type{ast.StringType{}, ioErrType}})
	registerStructMethod("Reader", "read_chunk_bytes", []ast.Type{ast.NumberType{}},
		ast.EnumType{Name: "Result", Args: []ast.Type{u8s, ioErrType}})
	registerStructMethod("Reader", "close", nil, voidIoResult)
	fileStatResult := ast.EnumType{Name: "Result", Args: []ast.Type{
		ast.StructType{Name: "FileStat"}, ioErrType}}
	registerStructMethod("Reader", "stat", nil, fileStatResult)
	dirType := ast.StructType{Name: "Dir"}
	dirResult := ast.EnumType{Name: "Result", Args: []ast.Type{dirType, ioErrType}}
	c.info.FuncSigs["open_dir"] = &ast.FuncType{Params: []ast.Type{ast.StringType{}}, Result: dirResult}
	registerStructMethod("Dir", "open_dir", []ast.Type{ast.StringType{}}, dirResult)
	registerStructMethod("Dir", "entries", nil,
		ast.EnumType{Name: "Result", Args: []ast.Type{ast.ArrayType{Elem: ast.StringType{}}, ioErrType}})
	registerStructMethod("Dir", "stat", []ast.Type{ast.StringType{}}, fileStatResult)
	registerStructMethod("Dir", "lstat", []ast.Type{ast.StringType{}}, fileStatResult)
	registerStructMethod("Dir", "access", []ast.Type{ast.StringType{}, ast.NumberType{}}, voidIoResult)
	registerStructMethod("Dir", "remove_file", []ast.Type{ast.StringType{}}, voidIoResult)
	registerStructMethod("Dir", "remove_dir", []ast.Type{ast.StringType{}}, voidIoResult)
	registerStructMethod("Dir", "chmod", []ast.Type{ast.StringType{}, ast.NumberType{}, ast.BoolType{}}, voidIoResult)
	registerStructMethod("Dir", "chown", []ast.Type{ast.StringType{}, ast.NumberType{}, ast.NumberType{}, ast.BoolType{}}, voidIoResult)
	registerStructMethod("Dir", "close", nil, voidIoResult)
	registerStructMethod("Writer", "stat", nil, fileStatResult)
	seekResult := ast.EnumType{Name: "Result", Args: []ast.Type{
		ast.NumberType{Width: 64, Signed: true}, ioErrType}}
	registerStructMethod("Reader", "seek",
		[]ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{}}, seekResult)
	registerStructMethod("Writer", "seek",
		[]ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{}}, seekResult)
	registerStructMethod("Reader", "splice_to",
		[]ast.Type{writerType, ast.NumberType{}}, seekResult)
	registerStructMethod("Reader", "copy_range_to",
		[]ast.Type{writerType, ast.NumberType{}}, seekResult)
	flagsResult := ast.EnumType{Name: "Result", Args: []ast.Type{
		ast.NumberType{Width: 64, Signed: true}, ioErrType}}
	registerStructMethod("Reader", "flags", nil, flagsResult)
	registerStructMethod("Writer", "flags", nil, flagsResult)
	registerStructMethod("Reader", "isatty", nil, ast.BoolType{})
	registerStructMethod("Writer", "isatty", nil, ast.BoolType{})
	registerStructMethod("Reader", "dup_onto",
		[]ast.Type{ast.NumberType{}}, voidIoResult)
	registerStructMethod("Writer", "dup_onto",
		[]ast.Type{ast.NumberType{}}, voidIoResult)
	registerStructMethod("Reader", "window_size", nil,
		ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StructType{Name: "WinSize"}, ioErrType}})
	registerStructMethod("Reader", "set_window_size",
		[]ast.Type{ast.NumberType{}, ast.NumberType{}}, voidIoResult)
	registerStructMethod("Reader", "termios_get", nil,
		ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}}, ioErrType}})
	registerStructMethod("Reader", "termios_set",
		[]ast.Type{ast.NumberType{},
			ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}}}, voidIoResult)
	registerStructMethod("Reader", "set_extproc", []ast.Type{ast.BoolType{}}, voidIoResult)
	registerStructMethod("Writer", "write", []ast.Type{ast.StringType{}}, voidIoResult)
	registerStructMethod("Writer", "splice_forever", []ast.Type{ast.StringType{}}, voidIoResult)
	writeSomeResult := ast.EnumType{Name: "Result", Args: []ast.Type{
		ast.NumberType{Width: 64, Signed: true}, ioErrType}}
	registerStructMethod("Writer", "write_some", []ast.Type{ast.StringType{}}, writeSomeResult)
	bytes := ast.SliceType{Elem: ast.NumberType{Width: 8, Signed: false}}
	registerStructMethod("Writer", "write_bytes", []ast.Type{bytes}, voidIoResult)
	registerStructMethod("Writer", "write_some_bytes", []ast.Type{bytes}, writeSomeResult)
	registerStructMethod("Writer", "close", nil, voidIoResult)
	registerStructMethod("Writer", "truncate",
		[]ast.Type{ast.NumberType{Width: 64, Signed: true}}, voidIoResult)
	registerStructMethod("Reader", "fsync", nil, voidIoResult)
	registerStructMethod("Writer", "fsync", nil, voidIoResult)
	registerStructMethod("Reader", "fdatasync", nil, voidIoResult)
	registerStructMethod("Writer", "fdatasync", nil, voidIoResult)
	registerStructMethod("Reader", "syncfs", nil, voidIoResult)
	registerStructMethod("Writer", "syncfs", nil, voidIoResult)
	dropCacheArgs := []ast.Type{ast.NumberType{Width: 64, Signed: true}, ast.NumberType{Width: 64, Signed: true}}
	registerStructMethod("Reader", "drop_cache", dropCacheArgs, voidIoResult)
	registerStructMethod("Writer", "drop_cache", dropCacheArgs, voidIoResult)
	c.info.FuncSigs["sync"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.VoidType{},
	}

	keyParam := ast.ParamType{Name: "K"}
	valueParam := ast.ParamType{Name: "V"}
	mapKV := ast.StructType{Name: "Map", Args: []ast.Type{keyParam, valueParam}}
	optionV := ast.EnumType{Name: "Option", Args: []ast.Type{valueParam}}
	c.info.FuncSigs["map_new"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.StructType{Name: "Map"},
	}
	registerMapMethod := func(methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_Map_" + methodName
		c.info.Methods["Map."+methodName] = mangled
		fullParams := append([]ast.Type{mapKV}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	registerMapMethod("len", nil, ast.NumberType{})
	registerMapMethod("contains", []ast.Type{keyParam}, ast.BoolType{})
	registerMapMethod("get", []ast.Type{keyParam}, optionV)
	registerMapMethod("set", []ast.Type{keyParam, valueParam}, mapKV)
	registerMapMethod("keys", nil, ast.ArrayType{Elem: keyParam})
	registerMapMethod("values", nil, ast.ArrayType{Elem: valueParam})
	registerMapMethod("remove", []ast.Type{keyParam}, mapKV)
	registerMapMethod("clear", nil, mapKV)
	registerMapMethod("get_or", []ast.Type{keyParam, valueParam}, valueParam)
	mapIterKV := ast.StructType{Name: "MapIter", Args: []ast.Type{keyParam, valueParam}}
	registerMapMethod("iter", nil, mapIterKV)
	c.info.Methods["Map.insert"] = "__method_Map_set"
	c.info.Methods["Map.cleared"] = "__method_Map_clear"

	arrayElemParam := ast.ParamType{Name: "T"}
	c.info.Methods["Array.push"] = "__method_Array_push"
	c.info.FuncSigs["__method_Array_push"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}
	c.intrinsicSigs = map[*ast.FuncType]IntrinsicKind{
		c.info.FuncSigs["__method_Array_push"]: IntrinsicArrayAppend,
	}
	c.info.Methods["Array.set"] = "__method_Array_set"
	c.info.FuncSigs["__method_Array_set"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			ast.NumberType{},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}
	c.info.Methods["Array.append"] = "__method_Array_push"
	c.info.Methods["Array.with"] = "__method_Array_set"
	delete(c.info.Methods, "Map.set")
	delete(c.info.Methods, "Map.clear")
	delete(c.info.Methods, "Array.set")
	delete(c.info.Methods, "Array.push")
	c.info.Methods["Array.len"] = "__method_Array_len"
	c.info.FuncSigs["__method_Array_len"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: arrayElemParam}},
		Result: ast.NumberType{},
	}
	sliceElemParam := ast.ParamType{Name: "T"}
	c.info.Methods["slice.len"] = "__method_slice_len"
	c.info.FuncSigs["__method_slice_len"] = &ast.FuncType{
		Params: []ast.Type{ast.SliceType{Elem: sliceElemParam}},
		Result: ast.NumberType{},
	}

	cellParam := ast.ParamType{Name: "T"}
	cellT := ast.StructType{Name: "Cell", Args: []ast.Type{cellParam}}
	c.info.FuncSigs["cell_new"] = &ast.FuncType{
		Params: []ast.Type{cellParam},
		Result: ast.StructType{Name: "Cell"},
	}
	c.info.Methods["Cell.get"] = "__method_Cell_get"
	c.info.FuncSigs["__method_Cell_get"] = &ast.FuncType{
		Params: []ast.Type{cellT},
		Result: cellParam,
	}
	c.info.Methods["Cell.set"] = "__method_Cell_set"
	c.info.FuncSigs["__method_Cell_set"] = &ast.FuncType{
		Params: []ast.Type{cellT, cellParam},
		Result: ast.VoidType{},
	}

	registerMapIterMethod := func(methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_MapIter_" + methodName
		c.info.Methods["MapIter."+methodName] = mangled
		fullParams := append([]ast.Type{mapIterKV}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	registerMapIterMethod("has_next", nil, ast.BoolType{})
	registerMapIterMethod("key", nil, keyParam)
	registerMapIterMethod("value", nil, valueParam)
	registerMapIterMethod("advance", nil, ast.VoidType{})

	registerStringMethod := func(methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_string_" + methodName
		c.info.Methods["string."+methodName] = mangled
		fullParams := append([]ast.Type{ast.StringType{}}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	registerStringMethod("as_bytes", nil, ast.SliceType{Elem: ast.NumberType{Width: 8, Signed: false}})
	registerStringMethod("len", nil, ast.NumberType{})
	c.info.FuncSigs["__method_boolean_to_string"] = &ast.FuncType{Params: []ast.Type{ast.BoolType{}}, Result: ast.StringType{}}

	c.info.FuncSigs["string_from_bytes_unchecked"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.StringType{},
	}
	c.info.FuncSigs["string_from_bytes_range_unchecked"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.StringType{},
	}

	c.info.FuncSigs["slice_unchecked"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.StrType{},
	}

	usizeT := ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}
	c.info.FuncSigs["__memcpy"] = &ast.FuncType{
		Params: []ast.Type{usizeT, usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__memset"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__alloc_u8"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
	}
	c.info.FuncSigs["__alloc_i32"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}},
	}
	c.info.FuncSigs["__alloc_i64"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}},
	}
	c.info.FuncSigs["__alloc_bool"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.BoolType{}},
	}
	c.info.FuncSigs["__alloc"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__fern_rc_inc"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: usizeT,
	}
	c.info.FuncSigs["__fern_arr_dec"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__fern_drop_arr_ptr"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__fern_str_dec"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__free"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__fern_rc_dec"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: usizeT,
	}
	c.info.FuncSigs["__fern_box_free"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__alloc_reuse"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}, ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__load_i32"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__store_i32"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__load_u8"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__store_u8"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__str_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, usizeT},
		Result: usizeT,
	}
	c.info.FuncSigs["__arr_set_len"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	wordT := ast.NumberType{Width: 64, Signed: true}
	for n := 3; n <= 6; n++ {
		params := []ast.Type{ast.NumberType{}}
		for i := 0; i < n; i++ {
			params = append(params, wordT)
		}
		c.info.FuncSigs[fmt.Sprintf("__syscall%d", n)] = &ast.FuncType{Params: params, Result: wordT}
	}
	c.info.FuncSigs["__load_ptr"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: usizeT,
	}
	c.info.FuncSigs["__store_ptr"] = &ast.FuncType{
		Params: []ast.Type{usizeT, usizeT},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__ptr_width"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	for n, params := range map[string]int{"__c_call0": 1, "__c_call1": 2, "__c_call2": 3, "__c_call3": 4, "__c_call4": 5} {
		ps := make([]ast.Type, params)
		for i := range ps {
			ps[i] = usizeT
		}
		c.info.FuncSigs[n] = &ast.FuncType{Params: ps, Result: usizeT}
		ps32 := make([]ast.Type, params)
		ps64 := make([]ast.Type, params)
		for i := range ps32 {
			ps32[i] = usizeT
			ps64[i] = usizeT
		}
		c.info.FuncSigs[n+"_f32"] = &ast.FuncType{Params: ps32, Result: ast.FloatType{Width: 32}}
		c.info.FuncSigs[n+"_f64"] = &ast.FuncType{Params: ps64, Result: ast.FloatType{Width: 64}}
	}
	c.info.FuncSigs["__load_i64"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["__store_i64"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}

	for _, td := range prog.Traits {
		if _, dup := c.info.Traits[td.Name]; dup {
			c.errfCode(td.P, "E006", "trait %q redeclared", td.Name)
			continue
		}
		seenMethod := map[string]bool{}
		for _, m := range td.Methods {
			if seenMethod[m.Name] {
				c.errfCode(m.P, "E006", "trait method %q redeclared in trait %q", m.Name, td.Name)
				continue
			}
			seenMethod[m.Name] = true
		}
		c.info.Traits[td.Name] = td
	}

	c.synthesizeDerives(prog)

	c.synthesizeTraitDefaults(prog)

	for _, fn := range prog.Funcs {
		if fn.ImportIface != "" && fn.Async {
			if st, ok := fn.ReturnType.(ast.StreamType); ok && st.Elem != nil {
				fn.ReturnType = ast.ArrayType{Elem: st.Elem}
			}
			for i := range fn.Params {
				if st, ok := fn.Params[i].Type.(ast.StreamType); ok && st.Elem != nil {
					fn.Params[i].Type = ast.ArrayType{Elem: st.Elem}
				}
			}
		}
	}

	for _, fn := range prog.Funcs {
		if fn.Name == "main" && fn.Receiver == nil && fn.AssocType == "" && fn.Body != nil {
			c.checkMainSignature(fn)
		}
		if fn.AssocType != "" {
			typeName := fn.AssocType
			switch kind, other := c.methodDeclConflict(typeName, fn.Name, fn.ImplTrait); kind {
			case declDup:
				c.errfCode(fn.P, "E006", "associated function %q on %s redeclared", fn.Name, typeName)
				continue
			case declAmbiguous:
				c.errAmbiguousDecl(typeName, fn.Name,
					[]methodCand{{Trait: fn.ImplTrait, Pos: fn.P, Module: fn.BodyModule()}, other})
				continue
			}
			simpleName := fn.Name
			fn.MethodRecv = typeName
			fn.MethodSimpleName = simpleName
			fn.Name = c.mangleMethodName("__assoc_", typeName, simpleName, fn.ImplTrait)
			fn.AssocType = ""
			c.registerMethod(typeName, simpleName, fn.ImplTrait, fn.Name, fn.SourceModule, fn.P)
		} else if fn.Receiver != nil {
			var typeName string
			switch rt := fn.Receiver.Type.(type) {
			case ast.StructType:
				if _, ok := c.info.Structs[rt.Name]; !ok {
					c.errfCode(fn.P, "E021", "method receiver references unknown struct %q", rt.Name)
					hoistReceiver(fn)
					continue
				}
				typeName = rt.Name
			case ast.EnumType:
				if _, ok := c.info.Enums[rt.Name]; !ok {
					c.errfCode(fn.P, "E021", "method receiver references unknown enum %q", rt.Name)
					hoistReceiver(fn)
					continue
				}
				typeName = rt.Name
			case ast.ArrayType:
				if !elemDispatchable(rt.Elem) {
					c.errfCode(fn.P, "E021", "array-receiver method cannot take a nested array/slice element (%s); make the receiver element-polymorphic (e.g. `(xs: T[])`) or take the array as a plain parameter", fn.Receiver.Type)
					hoistReceiver(fn)
					continue
				}
				typeName = "Array"
			case ast.SliceType:
				if !elemDispatchable(rt.Elem) {
					c.errfCode(fn.P, "E021", "slice-receiver method cannot take a nested array/slice element (%s); make the receiver element-polymorphic (e.g. `(xs: [T])`) or take the slice as a plain parameter", fn.Receiver.Type)
					hoistReceiver(fn)
					continue
				}
				typeName = "slice"
			default:
				n, ok := methodTypeName(fn.Receiver.Type)
				if !ok {
					c.errfCode(fn.P, "E021", "method receiver type must be a struct, enum, array, slice, or built-in type, got %s", fn.Receiver.Type)
					hoistReceiver(fn)
					continue
				}
				typeName = n
			}
			if kind, other := c.methodDeclConflict(typeName, fn.Name, fn.ImplTrait); kind != declOK {
				if kind == declDup {
					c.errfCode(fn.P, "E006", "method %q on %s redeclared", fn.Name, typeName)
				} else {
					c.errAmbiguousDecl(typeName, fn.Name,
						[]methodCand{{Trait: fn.ImplTrait, Pos: fn.P, Module: fn.BodyModule()}, other})
				}
				hoistReceiver(fn)
				continue
			}
			simpleName := fn.Name
			mangled := c.mangleMethodName("__method_", typeName, simpleName, fn.ImplTrait)
			fn.Name = mangled
			fn.MethodRecv = typeName
			fn.MethodSimpleName = simpleName
			fn.Params = append([]ast.Param{*fn.Receiver}, fn.Params...)
			fn.Receiver = nil
			c.registerMethod(typeName, simpleName, fn.ImplTrait, mangled, fn.SourceModule, fn.P)
		} else if fn.MethodRecv != "" {
			c.registerMethod(fn.MethodRecv, fn.MethodSimpleName, fn.ImplTrait, fn.Name, fn.SourceModule, fn.P)
		}
		if _, dup := c.info.FuncSigs[fn.Name]; dup && !isScalarBuiltinMethod(fn.Name) {
			c.errfCode(fn.P, "E006", "function %q redeclared", fn.Name)
			continue
		}
		params := make([]ast.Type, len(fn.Params))
		owns := make([]bool, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
			owns[i] = p.Own
		}
		c.info.FuncSigs[fn.Name] = &ast.FuncType{Params: params, ParamOwn: ast.OwnFlags(owns, len(params)), Result: fn.ReturnType}
		if fn.AssocConst {
			c.info.AssocConsts[fn.Name] = true
		}
		if fn.MethodRecv == "" {
			c.funcSources[fn.Name] = fn.SourceModule
		}
		if len(fn.TypeParams) > 0 {
			c.info.GenericFuncs[fn.Name] = fn
			c.info.Generics[fn.Name] = fn
		}
	}

	methodOwns := map[string][]bool{}
	for _, fn := range prog.Funcs {
		flags := make([]bool, len(fn.Params))
		any := false
		for i, p := range fn.Params {
			flags[i] = p.Own
			any = any || p.Own
		}
		if any {
			methodOwns[fn.Name] = flags
		}
	}
	for _, impl := range prog.Impls {
		typeName, ok := methodTypeName(impl.Type)
		if !ok {
			c.errfCode(impl.TypePos, "E021", "`impl … for %s`: type must be a struct, enum, or built-in type", impl.Type)
			continue
		}
		td, ok := c.info.Traits[impl.Trait]
		if !ok {
			c.errfCode(impl.TraitPos, "E021", "unknown trait %q in impl", demangle(impl.Trait))
			continue
		}
		if len(impl.TraitArgs) != len(td.TypeParams) {
			c.errfCode(impl.TraitPos, "E021", "trait %s takes %d type argument(s), %d supplied",
				demangle(impl.Trait), len(td.TypeParams), len(impl.TraitArgs))
			continue
		}
		if impl.SourceModule != "" {
			traitLocal := td.SourceModule == impl.SourceModule
			typeLocal := false
			if sd, ok := c.info.Structs[typeName]; ok && sd.SourceModule == impl.SourceModule {
				typeLocal = true
			}
			if ed, ok := c.info.Enums[typeName]; ok && ed.SourceModule == impl.SourceModule {
				typeLocal = true
			}
			if !traitLocal && !typeLocal {
				c.report(impl.SourceModule, impl.P, "E021", fmt.Sprintf(
					"orphan impl: `impl %s for %s` must be declared in the module that defines the trait or the type",
					demangle(impl.Trait), demangle(typeName)))
				continue
			}
		}
		if c.info.Impls[impl.Trait] == nil {
			c.info.Impls[impl.Trait] = map[string]bool{}
		}
		if c.info.Impls[impl.Trait][typeName] {
			c.errfCode(impl.P, "E006", "duplicate impl: %s is already implemented for %s", demangle(impl.Trait), demangle(typeName))
			continue
		}
		traitMethods := map[string]bool{}
		for _, m := range td.Methods {
			traitMethods[m.Name] = true
		}
		for _, mn := range impl.MethodNames {
			if !traitMethods[mn] {
				c.errfCode(impl.P, "E021", "method %q is not a member of trait %s", mn, demangle(impl.Trait))
			}
		}
		paramSub := map[string]ast.Type{}
		for _, tp := range impl.TypeParams {
			paramSub[tp] = ast.ParamType{Name: tp}
		}
		conforms := true
		for _, m := range td.Methods {
			prefix := "__method_"
			if m.Assoc {
				prefix = "__assoc_"
			}
			mangled := prefix + typeName + "_" + m.Name
			if got, _, found := c.resolveMethod(typeName, m.Name, []string{impl.Trait}); found && strings.HasPrefix(got, prefix) {
				mangled = got
			}
			sig, ok := c.info.FuncSigs[mangled]
			if !ok && m.Body != nil && (len(m.TypeParams) > 0 || wrapsSelf(m.Result)) {
				continue
			}
			if ok && len(m.TypeParams) > 0 && !slices.ContainsFunc(append(append([]ast.Type(nil), sig.Params...), sig.Result), containsParamType) {
				continue
			}
			if !ok {
				if m.Const {
					c.errfCode(impl.P, "E021", "%s does not implement %s: missing associated constant %q", demangle(typeName), demangle(impl.Trait), m.Name)
				} else {
					c.errfCode(impl.P, "E021", "%s does not implement %s: missing method %q", demangle(typeName), demangle(impl.Trait), m.Name)
				}
				conforms = false
				continue
			}
			if m.Const != c.info.AssocConsts[mangled] {
				if m.Const {
					c.errfCode(impl.P, "E021", "%s.%s is an associated constant of trait %s; bind it with `const %s: %s = …;`, not a function",
						demangle(typeName), m.Name, demangle(impl.Trait), m.Name, c.resolveProjWith(ast.SubstSelf(m.Result, impl.Type), impl.AssocTypeBindings))
				} else {
					c.errfCode(impl.P, "E021", "%s.%s is an associated function of trait %s; provide it with `function`, not a const",
						demangle(typeName), m.Name, demangle(impl.Trait))
				}
				conforms = false
				continue
			}
			traitSub := map[string]ast.Type{}
			if len(td.TypeParams) == len(impl.TraitArgs) {
				for i, tp := range td.TypeParams {
					traitSub[tp] = impl.TraitArgs[i]
				}
			}
			ownSub := map[string]ast.Type{}
			if _, renamed := renameApart(m.TypeParams, impl.TypeParams, impl.TraitArgs); len(renamed) > 0 {
				for from, to := range renamed {
					ownSub[from] = ast.ParamType{Name: to}
				}
			}
			subTrait := func(t ast.Type) ast.Type {
				if len(ownSub) > 0 {
					t = substByName(t, ownSub)
				}
				if len(traitSub) > 0 {
					t = substByName(t, traitSub)
				}
				return c.resolveProjWith(c.normalizeEnumKinds(ast.SubstSelf(t, impl.Type)), impl.AssocTypeBindings)
			}
			want := make([]ast.Type, len(m.Params))
			for i, p := range m.Params {
				want[i] = subTrait(p.Type)
			}
			wantRet := subTrait(m.Result)
			gotSig := &ast.FuncType{Params: make([]ast.Type, len(sig.Params)), Result: c.resolveProjWith(c.normalizeEnumKinds(sig.Result), impl.AssocTypeBindings)}
			for i, pt := range sig.Params {
				gotSig.Params[i] = c.resolveProjWith(c.normalizeEnumKinds(pt), impl.AssocTypeBindings)
			}
			if len(paramSub) > 0 {
				for i := range want {
					want[i] = substByName(want[i], paramSub)
				}
				wantRet = substByName(wantRet, paramSub)
				for i := range gotSig.Params {
					gotSig.Params[i] = substByName(gotSig.Params[i], paramSub)
				}
				gotSig.Result = substByName(gotSig.Result, paramSub)
			}
			for i := range want {
				want[i] = c.cloneSpelling(want[i])
			}
			wantRet = c.cloneSpelling(wantRet)
			if !sigMatches(gotSig, want, wantRet) {
				c.errfCode(impl.P, "E021",
					"%s.%s has the wrong signature for trait %s: expected %s, got %s",
					demangle(typeName), m.Name, demangle(impl.Trait),
					(&ast.FuncType{Params: want, Result: wantRet}).String(), gotSig.String())
				conforms = false
			}
			implOwns := methodOwns[mangled]
			for i, p := range m.Params {
				implOwn := i < len(implOwns) && implOwns[i]
				if p.Own != implOwn {
					verb := "must take `own` on"
					if !p.Own {
						verb = "must not take `own` on"
					}
					c.errfCode(impl.P, "E021",
						"%s.%s %s parameter %q to match trait %s",
						demangle(typeName), m.Name, verb, p.Name, demangle(impl.Trait))
					conforms = false
					break
				}
			}
		}
		if td2, ok := c.info.Traits[impl.Trait]; ok {
			declared := map[string]bool{}
			for _, at := range td2.AssocTypes {
				declared[at] = true
			}
			for name := range impl.AssocTypeBindings {
				if !declared[name] {
					c.errfCode(impl.P, "E021", "impl of %s for %s binds associated type %q which the trait does not declare",
						demangle(impl.Trait), demangle(typeName), name)
					conforms = false
				}
			}
			for _, at := range td2.AssocTypes {
				if _, ok := impl.AssocTypeBindings[at]; !ok {
					c.errfCode(impl.P, "E021", "impl of %s for %s must bind associated type %q (`type %s = …;`)",
						demangle(impl.Trait), demangle(typeName), at, at)
					conforms = false
				}
			}
			if conforms && len(impl.AssocTypeBindings) > 0 {
				if c.info.AssocBindings[typeName] == nil {
					c.info.AssocBindings[typeName] = map[string]ast.Type{}
				}
				for name, bt := range impl.AssocTypeBindings {
					c.info.AssocBindings[typeName][name] = bt
					if len(impl.TypeParams) > 0 {
						if c.info.AssocBindingPattern[typeName] == nil {
							c.info.AssocBindingPattern[typeName] = map[string]ast.Type{}
						}
						c.info.AssocBindingPattern[typeName][name] = impl.Type
					}
				}
			}
		}
		if conforms {
			c.info.Impls[impl.Trait][typeName] = true
			if len(impl.TraitArgs) > 0 {
				if c.info.ImplTraitArgs[impl.Trait] == nil {
					c.info.ImplTraitArgs[impl.Trait] = map[string][]ast.Type{}
				}
				c.info.ImplTraitArgs[impl.Trait][typeName] = impl.TraitArgs
				if len(paramSub) > 0 {
					if c.info.ImplForPattern[impl.Trait] == nil {
						c.info.ImplForPattern[impl.Trait] = map[string]ast.Type{}
					}
					c.info.ImplForPattern[impl.Trait][typeName] = substByName(impl.Type, paramSub)
				}
			}
		}
	}

	for _, td := range prog.Traits {
		for _, sup := range td.Supertraits {
			if _, ok := c.info.Traits[sup]; !ok {
				c.errfCode(td.P, "E021", "unknown supertrait %q in trait %s", demangle(sup), demangle(td.Name))
			}
		}
		if c.traitInItsOwnSupers(td.Name) {
			c.errfCode(td.P, "E021", "cyclic supertrait: %s is (transitively) its own supertrait", demangle(td.Name))
		}
	}

	for _, impl := range prog.Impls {
		typeName, ok := methodTypeName(impl.Type)
		if !ok {
			continue
		}
		if !c.info.Impls[impl.Trait][typeName] {
			continue
		}
		td, ok := c.info.Traits[impl.Trait]
		if !ok {
			continue
		}
		for _, sup := range c.expandTraits(td.Supertraits) {
			if !c.info.Impls[sup][typeName] {
				c.errfCode(impl.P, "E021",
					"impl %s for %s also requires `impl %s for %s` (supertrait of %s)",
					demangle(impl.Trait), demangle(typeName), demangle(sup), demangle(typeName), demangle(impl.Trait))
			}
		}
	}

	c.resolveProjections(prog)

	c.validateMapKeyTypes(prog)

	for _, fn := range prog.Funcs {
		if len(fn.TypeParams) > 0 && fn.BoundArgs != nil {
			tpSet := make(map[string]bool, len(fn.TypeParams))
			for _, tp := range fn.TypeParams {
				tpSet[tp] = true
			}
			for _, argLists := range fn.BoundArgs {
				for i := range argLists {
					for k := range argLists[i] {
						argLists[i][k] = normalizeParamRefs(argLists[i][k], tpSet)
					}
				}
			}
		}
		for tp, traits := range fn.Bounds {
			for i, traitName := range traits {
				td, ok := c.info.Traits[traitName]
				if !ok {
					c.errfCode(fn.P, "E021", "unknown trait %q in bound on type parameter %s", demangle(traitName), tp)
					continue
				}
				var nargs int
				if ba := fn.BoundArgs[tp]; i < len(ba) {
					nargs = len(ba[i])
				}
				if nargs != len(td.TypeParams) {
					c.errfCode(fn.P, "E021", "trait %s takes %d type argument(s), %d supplied in bound on type parameter %s",
						demangle(traitName), len(td.TypeParams), nargs, tp)
				}
			}
		}
	}

	if h := findDecl(prog, "handle"); h != nil && h.Receiver == nil && len(h.Params) == 2 &&
		findDecl(prog, WasiHandleName) == nil && findDecl(prog, "platform__host") != nil {
		prog.Funcs = append(prog.Funcs, synthesiseWasiHandle())
	}
	h := findDecl(prog, "handle")
	if h != nil && h.Receiver == nil && len(h.Params) == 3 {
		c.info.StatefulHandler = true
	}
	wasiRefused := wasiHTTP && h != nil && h.Receiver == nil && len(h.Params) != 2
	if wasiRefused {
		c.errfCode(h.P, "E075", "%s", wasiHTTPHandlerShape(len(h.Params)))
	}
	if hasHandleDecl(prog) && !hasMainDecl(prog) && !wasiRefused {
		if !wasiHTTP && !handlerImportsServe(prog) {
			c.errfCode(h.P, "E001", "%s", HandlerNeedsServe)
		} else if c.checkHandlerStatePairing(prog) && !wasiHTTP {
			prog.Funcs = append(prog.Funcs, synthesiseHandleMain(prog, supervised))
		}
	}

	c.validateDynTraitTypes(prog)
	c.validateResourceHandles(prog)
	c.validateExports(prog)

	c.ownFuncs = map[string][]bool{}
	for _, fn := range prog.Funcs {
		hasOwn := false
		flags := make([]bool, len(fn.Params))
		for i, p := range fn.Params {
			flags[i] = p.Own
			hasOwn = hasOwn || p.Own
		}
		if hasOwn {
			c.ownFuncs[fn.Name] = flags
		}
	}
	c.info.OwnFuncs = c.ownFuncs
	c.computeResultBorrows(prog)

	c.rewriteAssocConstReads(prog)
	for _, fn := range prog.Funcs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.checkFunction(fn)
	}

	c.checkEscapes(prog)
	c.checkFipFunctions(prog)
	c.checkUnsettledWideLiterals()

	if len(c.errors) > 0 {
		return c.info, diag.Errors(c.errors)
	}

	ast.RewriteProgramExprs(prog, func(e ast.Expr) ast.Expr {
		if t, ok := e.(*ast.TryOp); ok && t.Lowered != nil {
			return t.Lowered
		}
		if d, ok := e.(*ast.DowncastExpr); ok && d.Lowered != nil {
			return d.Lowered
		}
		if u, ok := e.(*ast.Unary); ok {
			if u.NegCall != nil {
				return u.NegCall
			}
			return e
		}
		b, ok := e.(*ast.Binary)
		if !ok {
			return e
		}
		if b.CheckedLowered != nil {
			return b.CheckedLowered
		}
		if b.EqCall != nil {
			if b.EqNegate {
				return &ast.Unary{P: b.P, Op: "!", Operand: b.EqCall}
			}
			return b.EqCall
		}
		if b.CmpCall != nil {
			return &ast.Binary{
				P:        b.P,
				Op:       b.Op,
				Left:     b.CmpCall,
				Right:    &ast.NumberLit{P: b.P, Value: 0, Width: 32},
				IntWidth: 32,
			}
		}
		if b.ArithCall != nil {
			return b.ArithCall
		}
		if b.IntWidth == 0 && !b.IsFloat && !b.IsLanes && !b.IsStringConcat {
			switch b.Op {
			case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>", "+|", "-|", "*|", "<<|":
				b.IntWidth = 32
			}
		}
		return e
	})

	return c.info, nil
}

func validScalarValue(v int64) bool {
	return v >= 0 && v <= 0x10FFFF && (v < 0xD800 || v > 0xDFFF)
}

func surrogateHint(v int64) string {
	if v >= 0xD800 && v <= 0xDFFF {
		return " (this is a surrogate)"
	}
	return ""
}

func methodTypeName(t ast.Type) (string, bool) { return ast.ReceiverTypeName(t) }

func definitelyScalar(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.CharType, ast.VoidType:
		return true
	}
	return false
}

func paramSig(params []ast.Param, result ast.Type) *ast.FuncType {
	types := make([]ast.Type, len(params))
	owns := make([]bool, len(params))
	for i, p := range params {
		types[i] = p.Type
		owns[i] = p.Own
	}
	return &ast.FuncType{Params: types, ParamOwn: ast.OwnFlags(owns, len(types)), Result: result}
}

func (c *checker) mangleMethodName(prefix, typeName, name, trait string) string {
	key := typeName + "." + name
	if trait != "" {
		if _, taken := c.info.Methods[key]; taken && !slices.Contains(c.info.MethodOwners[key], trait) {
			return prefix + typeName + "__" + trait + "__" + name
		}
	}
	return prefix + typeName + "_" + name
}

func (c *checker) registerMethod(typeName, name, trait, mangled, srcModule string, pos ast.Position) {
	key := typeName + "." + name
	site := declSiteKey(typeName, name, trait)
	if _, exists := c.info.MethodDeclSites[site]; !exists {
		c.info.MethodDeclSites[site] = pos
	}
	if !slices.Contains(c.info.MethodInsts[site], mangled) {
		c.info.MethodInsts[site] = append(c.info.MethodInsts[site], mangled)
	}
	if _, exists := c.info.Methods[key]; !exists {
		c.info.Methods[key] = mangled
		c.info.MethodSources[mangled] = srcModule
	}
	if trait == "" {
		return
	}
	traitKey := trait + "." + key
	if _, exists := c.info.TraitMethods[traitKey]; exists {
		return
	}
	c.info.TraitMethods[traitKey] = mangled
	c.info.MethodSources[mangled] = srcModule
	owners := append(c.info.MethodOwners[key], trait)
	slices.Sort(owners)
	c.info.MethodOwners[key] = owners
}

func (c *checker) instForReceiver(typeName, name, trait, mangled string, recv ast.Type) string {
	et, ok := recv.(ast.EnumType)
	if !ok || len(et.Args) == 0 {
		return mangled
	}
	for _, inst := range c.info.MethodInsts[declSiteKey(typeName, name, trait)] {
		if sig := c.info.FuncSigs[inst]; sig != nil && len(sig.Params) > 0 && ast.Equal(sig.Params[0], recv) {
			return inst
		}
	}
	return mangled
}

func declSiteKey(typeName, name, trait string) string {
	if trait == "" {
		return typeName + "." + name
	}
	return trait + "." + typeName + "." + name
}

type methodCand struct {
	Trait  string
	Pos    ast.Position
	Module string
}

const (
	declOK = iota
	declDup
	declAmbiguous
)

func (c *checker) methodDeclConflict(typeName, name, trait string) (int, methodCand) {
	key := typeName + "." + name
	if pos, dup := c.info.MethodDeclSites[declSiteKey(typeName, name, trait)]; dup {
		return declDup, methodCand{Trait: trait, Pos: pos}
	}
	if trait == "" {
		if owners := c.info.MethodOwners[key]; len(owners) > 0 {
			return declAmbiguous, c.info.methodCands(typeName, name, owners[:1])[0]
		}
	} else if _, inherent := c.info.MethodDeclSites[key]; inherent {
		return declAmbiguous, c.info.methodCands(typeName, name, []string{""})[0]
	}
	if _, taken := c.info.Methods[key]; taken && len(c.info.MethodOwners[key]) == 0 {
		return declDup, methodCand{}
	}
	return declOK, methodCand{}
}

func (c *checker) errAmbiguousMethod(pos ast.Position, typeName, name string, cands []methodCand) {
	c.errfCode(pos, "E074", "%s", ambiguousMethodMessage(typeName, name, cands))
}

func ambiguousMethodMessage(typeName, name string, cands []methodCand) string {
	parts := make([]string, len(cands))
	for i, cand := range cands {
		what := "an inherent method"
		if cand.Trait != "" {
			what = "trait " + demangle(cand.Trait)
		}
		if cand.Pos == (ast.Position{}) {
			parts[i] = what
			continue
		}
		at := cand.Pos.String()
		if cand.Module != "" {
			at = moduleFileLabel(cand.Module) + ":" + at
		}
		parts[i] = what + " (declared at " + at + ")"
	}
	return fmt.Sprintf("ambiguous method %q on %s: provided by %s", name, demangle(typeName), strings.Join(parts, " and "))
}

func (c *checker) errAmbiguousDecl(typeName, name string, cands []methodCand) {
	at := cands[0]
	for _, cand := range cands {
		if !strings.HasPrefix(cand.Module, stdlibModulePrefix) {
			at = cand
			break
		}
	}
	c.report(at.Module, at.Pos, "E074", ambiguousMethodMessage(typeName, name, cands))
}

const stdlibModulePrefix = "stdlib://"

func moduleFileLabel(module string) string {
	if rest, ok := strings.CutPrefix(module, stdlibModulePrefix); ok {
		return rest
	}
	return filepath.Base(module)
}

func SimpleTraitName(name string) string {
	if i := strings.LastIndex(name, "__"); i >= 0 {
		return name[i+2:]
	}
	return name
}

func (in *Info) ResolveMethod(typeName, name string, prefer []string) (mangled, owner string, ok bool) {
	if in == nil {
		return "", "", false
	}
	key := typeName + "." + name
	owners := in.MethodOwners[key]
	if m, o, found := in.preferredMethod(key, owners, prefer); found {
		return m, o, true
	}
	mangled, ok = in.Methods[key]
	if !ok {
		return "", "", false
	}
	for _, o := range owners {
		if in.TraitMethods[o+"."+key] == mangled {
			return mangled, o, true
		}
	}
	return mangled, "", true
}

func (in *Info) preferredMethod(key string, owners, prefer []string) (mangled, owner string, ok bool) {
	for _, want := range prefer {
		if want == "" {
			continue
		}
		want = SimpleTraitName(want)
		for _, o := range owners {
			if SimpleTraitName(o) != want {
				continue
			}
			if m, found := in.TraitMethods[o+"."+key]; found {
				return m, o, true
			}
		}
	}
	return "", "", false
}

func (in *Info) traitInScope(fromModule, trait string) bool {
	td := in.Traits[trait]
	if td == nil || fromModule == "" || td.SourceModule == "" || td.SourceModule == fromModule {
		return true
	}
	if strings.HasPrefix(fromModule, "stdlib://") && strings.HasPrefix(td.SourceModule, "stdlib://") {
		return true
	}
	return in.DirectImports[fromModule][td.SourceModule]
}

func (in *Info) ResolveMethodFrom(fromModule, typeName, name string, prefer []string) (mangled, owner string, tied []string, ok bool) {
	if in == nil {
		return "", "", nil, false
	}
	key := typeName + "." + name
	owners := in.MethodOwners[key]
	if len(owners) < 2 {
		mangled, owner, ok = in.ResolveMethod(typeName, name, prefer)
		return mangled, owner, nil, ok
	}
	if m, o, found := in.preferredMethod(key, owners, prefer); found {
		return m, o, nil, true
	}
	best := make([]string, 0, len(owners))
	for _, o := range owners {
		if in.traitInScope(fromModule, o) {
			best = append(best, o)
		}
	}
	if len(best) == 0 {
		best = owners
	}
	m, found := in.TraitMethods[best[0]+"."+key]
	if !found {
		mangled, owner, ok = in.ResolveMethod(typeName, name, prefer)
		return mangled, owner, nil, ok
	}
	if len(best) > 1 {
		return m, best[0], best, true
	}
	return m, best[0], nil, true
}

func (in *Info) methodCands(typeName, name string, traits []string) []methodCand {
	out := make([]methodCand, len(traits))
	for i, tr := range traits {
		site := declSiteKey(typeName, name, tr)
		out[i] = methodCand{Trait: tr, Pos: in.MethodDeclSites[site]}
		if insts := in.MethodInsts[site]; len(insts) > 0 {
			out[i].Module = in.MethodSources[insts[0]]
		}
	}
	return out
}

func (c *checker) resolveMethod(typeName, name string, prefer []string) (mangled, owner string, ok bool) {
	return c.info.ResolveMethod(typeName, name, prefer)
}

func (c *checker) currentModule() string {
	if c.current == nil {
		return ""
	}
	return c.current.BodyModule()
}

func (c *checker) setElemHintFor(e ast.Expr, dst ast.Type) {
	c.elemHint = nil
	if _, ok := e.(*ast.ArrayLit); !ok {
		return
	}
	switch at := dst.(type) {
	case ast.ArrayType:
		c.elemHint = at.Elem
	case ast.SliceType:
		c.elemHint = at.Elem
	}
}

func forEachDynTrait(t ast.Type, fn func(string)) {
	switch x := t.(type) {
	case ast.DynTraitType:
		for _, tr := range x.Traits {
			fn(tr)
		}
	case ast.ArrayType:
		forEachDynTrait(x.Elem, fn)
	case ast.SliceType:
		forEachDynTrait(x.Elem, fn)
	case ast.TupleType:
		for _, e := range x.Elems {
			forEachDynTrait(e, fn)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			forEachDynTrait(p, fn)
		}
		forEachDynTrait(x.Result, fn)
	case ast.StructType:
		for _, a := range x.Args {
			forEachDynTrait(a, fn)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			forEachDynTrait(a, fn)
		}
	}
}

func forEachDynTraitObj(t ast.Type, fn func(ast.DynTraitType)) {
	switch x := t.(type) {
	case ast.DynTraitType:
		fn(x)
		for _, args := range x.Args {
			for _, a := range args {
				forEachDynTraitObj(a, fn)
			}
		}
	case ast.ArrayType:
		forEachDynTraitObj(x.Elem, fn)
	case ast.SliceType:
		forEachDynTraitObj(x.Elem, fn)
	case ast.TupleType:
		for _, e := range x.Elems {
			forEachDynTraitObj(e, fn)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			forEachDynTraitObj(p, fn)
		}
		forEachDynTraitObj(x.Result, fn)
	case ast.StructType:
		for _, a := range x.Args {
			forEachDynTraitObj(a, fn)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			forEachDynTraitObj(a, fn)
		}
	}
}

func forEachHandle(t ast.Type, fn func(ast.HandleType)) {
	switch x := t.(type) {
	case ast.HandleType:
		fn(x)
	case ast.ArrayType:
		forEachHandle(x.Elem, fn)
	case ast.SliceType:
		forEachHandle(x.Elem, fn)
	case ast.TupleType:
		for _, e := range x.Elems {
			forEachHandle(e, fn)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			forEachHandle(p, fn)
		}
		forEachHandle(x.Result, fn)
	case ast.StructType:
		for _, a := range x.Args {
			forEachHandle(a, fn)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			forEachHandle(a, fn)
		}
	}
}

func (c *checker) validateResourceHandles(prog *ast.Program) {
	visit := func(t ast.Type, pos ast.Position) {
		forEachHandle(t, func(h ast.HandleType) {
			if _, ok := c.info.Resources[h.Resource]; !ok {
				c.errfCode(pos, "E021", "%s", handleNotResource(h))
			}
		})
	}
	for _, fn := range prog.Funcs {
		for _, p := range fn.Params {
			visit(p.Type, fn.P)
		}
		visit(fn.ReturnType, fn.P)
		if fn.Body != nil {
			c.walkDeclaredTypes(fn.Body, visit)
		}
	}
	for _, sd := range prog.Structs {
		for _, f := range sd.Fields {
			visit(f.Type, sd.P)
		}
	}
	for _, ed := range prog.Enums {
		for _, v := range ed.Variants {
			for _, t := range v.Payloads {
				visit(t, ed.P)
			}
		}
	}
}

func handleNotResource(h ast.HandleType) string {
	name := demangle(h.Resource)
	if h.Borrowed {
		return fmt.Sprintf("`%s` is not a resource, so `borrow %s` is not a handle type; a parameter borrows by default, written `name: %s`", name, name, name)
	}
	return fmt.Sprintf("`%s` is not a resource, so `own %s` is not a handle type; a consuming parameter is written `own name: %s`", name, name, name)
}

func (c *checker) validateExports(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		if fn.ExportIface == "" {
			continue
		}
		if len(fn.TypeParams) > 0 {
			c.errfCode(fn.P, "E054", "@export function %q cannot be generic (a world export has a single concrete ABI)", fn.Name)
		}
		if fn.Receiver != nil || fn.MethodRecv != "" {
			c.errfCode(fn.P, "E054", "@export cannot be applied to a method (%q); use a top-level function", fn.Name)
		}
	}
}

func (c *checker) validateDynTraitTypes(prog *ast.Program) {
	reported := map[string]bool{}
	visit := func(t ast.Type, pos ast.Position) {
		forEachDynTrait(t, func(trait string) {
			if reported[trait] {
				return
			}
			if _, ok := c.info.Traits[trait]; !ok {
				reported[trait] = true
				c.errfCode(pos, "E021", "unknown trait %q in `dyn` type", demangle(trait))
			}
		})
		forEachDynTraitObj(t, func(dt ast.DynTraitType) {
			for i, trait := range dt.Traits {
				td, ok := c.info.Traits[trait]
				if !ok {
					continue
				}
				pinned := map[string]bool{}
				for _, b := range dt.AssocFor(i) {
					pinned[b.Name] = true
				}
				if safe, reason := c.objectSafe(trait, pinned); !safe {
					if !reported[trait] {
						reported[trait] = true
						c.errfCode(pos, "E021", "trait %s is not object-safe: %s, so it cannot be used as `dyn %s`",
							demangle(trait), reason, demangle(trait))
					}
					continue
				}
				if len(td.TypeParams) > 0 {
					if got := len(dt.ArgsFor(i)); got != len(td.TypeParams) {
						key := trait + "!arity"
						if reported[key] {
							continue
						}
						reported[key] = true
						c.errfCode(pos, "E021", "generic trait %s used as `dyn` must pin its type parameter(s): expected %d argument(s) (e.g. `dyn %s[...]`), got %d",
							demangle(trait), len(td.TypeParams), demangle(trait), got)
					}
				}
			}
		})
	}
	for _, fn := range prog.Funcs {
		for _, p := range fn.Params {
			visit(p.Type, fn.P)
		}
		visit(fn.ReturnType, fn.P)
		if fn.Body != nil {
			c.walkDeclaredTypes(fn.Body, visit)
		}
	}
}

func (c *checker) walkDeclaredTypes(b *ast.Block, visit func(ast.Type, ast.Position)) {
	if b == nil {
		return
	}
	ast.Walk(b, func(n ast.Node) bool {
		forEachDeclaredType(n, func(t *ast.Type, pos ast.Position) {
			if *t != nil {
				visit(*t, pos)
			}
		})
		return true
	})
}

func forEachDeclaredType(n ast.Node, visit func(t *ast.Type, pos ast.Position)) {
	switch x := n.(type) {
	case *ast.Var:
		visit(&x.Type, x.P)
	case *ast.FuncDecl:
		for i := range x.Params {
			visit(&x.Params[i].Type, paramPos(x.Params[i], x.P))
		}
		visit(&x.ReturnType, x.P)
	case *ast.Lambda:
		for i := range x.Params {
			visit(&x.Params[i].Type, paramPos(x.Params[i], x.P))
		}
		visit(&x.ReturnType, x.P)
	case *ast.Call:
		if x.TypeArgsWritten {
			for i := range x.TypeArgs {
				visit(&x.TypeArgs[i], x.P)
			}
		}
	case *ast.StructLit:
		if x.TypeArgsWritten {
			for i := range x.TypeArgs {
				visit(&x.TypeArgs[i], x.P)
			}
		}
	}
}

func mentionsSelf(t ast.Type) bool {
	switch x := t.(type) {
	case ast.SelfType:
		return true
	case ast.ArrayType:
		return mentionsSelf(x.Elem)
	case ast.SliceType:
		return mentionsSelf(x.Elem)
	case ast.TupleType:
		for _, e := range x.Elems {
			if mentionsSelf(e) {
				return true
			}
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			if mentionsSelf(p) {
				return true
			}
		}
		return mentionsSelf(x.Result)
	case ast.StructType:
		for _, a := range x.Args {
			if mentionsSelf(a) {
				return true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if mentionsSelf(a) {
				return true
			}
		}
	}
	return false
}

func (c *checker) objectSafe(traitName string, pinned map[string]bool) (bool, string) {
	td, ok := c.info.Traits[traitName]
	if !ok {
		return false, ""
	}
	for _, at := range td.AssocTypes {
		if !pinned[at] {
			return false, fmt.Sprintf("associated type %q is not pinned — write `dyn %s[%s = ...]`", at, demangle(traitName), at)
		}
	}
	for _, m := range td.Methods {
		for i := 1; i < len(m.Params); i++ {
			if mentionsSelf(m.Params[i].Type) {
				return false, fmt.Sprintf("method %q takes Self as a non-receiver parameter", m.Name)
			}
		}
		if mentionsSelf(m.Result) {
			return false, fmt.Sprintf("method %q returns Self", m.Name)
		}
	}
	return true, ""
}

func (c *checker) checkDynMethodCall(n *ast.Call, fa *ast.FieldAccess, dt ast.DynTraitType, s *scope) ast.Type {
	for i, tr := range dt.Traits {
		if _, ok := c.info.Traits[tr]; !ok {
			return nil
		}
		pinned := map[string]bool{}
		for _, b := range dt.AssocFor(i) {
			pinned[b.Name] = true
		}
		if safe, _ := c.objectSafe(tr, pinned); !safe {
			return nil
		}
	}
	var tm *ast.TraitMethod
	var ownerTrait string
	var collidingTraits []string
	for _, tr := range dt.Traits {
		td := c.info.Traits[tr]
		for i := range td.Methods {
			if td.Methods[i].Name == fa.Field {
				if tm != nil && ownerTrait != tr {
					collidingTraits = append(collidingTraits, tr)
				} else {
					tm = &td.Methods[i]
					ownerTrait = tr
				}
				break
			}
		}
	}
	if len(collidingTraits) > 0 {
		all := append([]string{ownerTrait}, collidingTraits...)
		for i := range all {
			all[i] = demangle(all[i])
		}
		c.errfCode(fa.FieldPos, "E062", "ambiguous method %q on `%s`: declared by traits %s",
			fa.Field, dt.String(), strings.Join(all, ", "))
		return nil
	}
	if tm == nil {
		c.errfCode(fa.FieldPos, "E021", "no method %q on `%s`", fa.Field, dt.String())
		return nil
	}
	if tm.Assoc {
		c.errfCode(fa.FieldPos, "E021",
			"%q is an associated function of trait %s, not a method; it has no `self` receiver, so it cannot be called through a `%s` value — call it on a concrete type",
			fa.Field, demangle(ownerTrait), dt.String())
		return nil
	}
	if td := c.info.Traits[ownerTrait]; td != nil && len(td.TypeParams) > 0 {
		var args []ast.Type
		for i, tr := range dt.Traits {
			if tr == ownerTrait {
				args = dt.ArgsFor(i)
				break
			}
		}
		if len(args) == len(td.TypeParams) {
			sub := make(map[string]ast.Type, len(args))
			for i, tp := range td.TypeParams {
				sub[tp] = args[i]
			}
			pinned := substTraitMethodTypeParams(*tm, sub)
			tm = &pinned
		}
	}
	if td := c.info.Traits[ownerTrait]; td != nil && len(td.AssocTypes) > 0 {
		var binds []ast.AssocBinding
		for i, tr := range dt.Traits {
			if tr == ownerTrait {
				binds = dt.AssocFor(i)
				break
			}
		}
		if len(binds) > 0 {
			bindings := make(map[string]ast.Type, len(binds))
			for _, b := range binds {
				bindings[b.Name] = b.Type
			}
			resolved := *tm
			resolved.Params = make([]ast.Param, len(tm.Params))
			for i, p := range tm.Params {
				p.Type = c.resolveProjWith(p.Type, bindings)
				resolved.Params[i] = p
			}
			resolved.Result = c.resolveProjWith(tm.Result, bindings)
			tm = &resolved
		}
	}
	wantParams := tm.Params
	if len(wantParams) > 0 {
		if _, isSelf := wantParams[0].Type.(ast.SelfType); isSelf || wantParams[0].Name == "self" {
			wantParams = wantParams[1:]
		}
	}
	if len(n.Args) != len(wantParams) {
		c.errfCode(n.P, "E004", "%s", arityMsg(fmt.Sprintf("method %q", fa.Field), len(wantParams), len(n.Args), "argument"))
		return ast.SubstSelf(tm.Result, dt)
	}
	for i, arg := range n.Args {
		at := c.checkExpr(arg, s)
		want := ast.SubstSelf(wantParams[i].Type, dt)
		if at != nil && !c.argOK(&n.Args[i], want, at, wantParams[i].Own) {
			c.errfCode(arg.Pos(), "E038", "argument %d to %q: expected %s, got %s", i+1, fa.Field, want, at)
		}
	}
	n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: dt, OwnerTrait: ownerTrait}
	n.DynTrait = ownerTrait
	return ast.SubstSelf(tm.Result, dt)
}

func (c *checker) normalizeEnumKinds(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = c.normalizeEnumKinds(a)
		}
		if _, isEnum := c.info.Enums[x.Name]; isEnum {
			return ast.EnumType{Name: x.Name, Args: args}
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = c.normalizeEnumKinds(a)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.normalizeEnumKinds(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.normalizeEnumKinds(x.Elem)}
	case ast.TupleType:
		els := make([]ast.Type, len(x.Elems))
		for i, e := range x.Elems {
			els[i] = c.normalizeEnumKinds(e)
		}
		return ast.TupleType{Elems: els}
	}
	return t
}

func sigMatches(sig *ast.FuncType, want []ast.Type, wantRet ast.Type) bool {
	if len(sig.Params) != len(want) {
		return false
	}
	for i := range want {
		if !ast.Equal(sig.Params[i], want[i]) {
			return false
		}
	}
	return ast.Equal(sig.Result, wantRet)
}

const cmpImport = "`import \"core/cmp\";`"

func deriveHint(tn, derive, trait string) string {
	mod, simple, foreign := strings.Cut(tn, "__")
	if mod == "" {
		foreign = false
	}
	if !foreign {
		simple = tn
	}
	routes := fmt.Sprintf("`@derive(%s)`", derive)
	if trait != "" {
		routes += fmt.Sprintf(" (or `impl %s for %s`)", trait, simple)
	}
	if !foreign {
		return fmt.Sprintf("add %s, which requires %s", routes, cmpImport)
	}
	orphan := ""
	if trait != "" {
		orphan = " (an impl written here instead would be an orphan impl)"
	}
	return fmt.Sprintf("add %s in module `%s`, which declares it and needs %s there%s", routes, mod, cmpImport, orphan)
}

func typeLabel(t ast.Type) string { return demangleAll(t.String()) }

func demangleAll(s string) string {
	identPart := func(b byte) bool {
		return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if !identPart(s[i]) || s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && identPart(s[j]) {
			j++
		}
		if word := s[i:j]; word[0] == '_' {
			b.WriteString(word)
		} else {
			b.WriteString(demangle(word))
		}
		i = j
	}
	return b.String()
}

func deriveKind(name string) string {
	switch simple := SimpleTraitName(name); simple {
	case "Eq", "Display", "Ord", "Hash", "Json", "FromJson", "Default", "Debug":
		return simple
	}
	return ""
}

func hasStdJson(prog *ast.Program) bool {
	for _, pf := range prog.Funcs {
		if pf.Name == "json__json_parse" {
			return true
		}
	}
	return false
}

func fromJsonFieldTypes(types []ast.Type) ([]ast.Type, int, bool) {
	out := make([]ast.Type, len(types))
	for i, t := range types {
		e := fromJsonElem(t)
		if _, nested := fromJsonContainer(e); nested {
			return nil, i, false
		}
		switch e.(type) {
		case ast.SliceType, ast.TupleType, *ast.FuncType:
			return nil, i, false
		}
		out[i] = e
	}
	return out, -1, true
}

func fromJsonContainer(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.ArrayType:
		return "array", true
	case ast.EnumType:
		if x.Name == "Option" && len(x.Args) == 1 {
			return "option", true
		}
	}
	return "", false
}

func fromJsonElem(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ArrayType:
		return x.Elem
	case ast.EnumType:
		if x.Name == "Option" && len(x.Args) == 1 {
			return x.Args[0]
		}
	}
	return t
}

func (c *checker) typeImplsEqAndHash(typeName string) bool {
	hasEq, hasHash := false, false
	for trait, types := range c.info.Impls {
		if !types[typeName] {
			continue
		}
		switch deriveKind(trait) {
		case "Eq":
			hasEq = true
		case "Hash":
			hasHash = true
		}
	}
	return hasEq && hasHash
}

const MapVoidValue = "map value type void is not supported — a set of keys is std/set's `Set`"

func (c *checker) mapKeyTypeError(k ast.Type) string {
	switch kt := k.(type) {
	case ast.NumberType, ast.CharType, ast.StringType, ast.StrType, ast.BoolType, ast.ParamType:
		return ""
	case ast.StructType:
		if c.typeImplsEqAndHash(kt.Name) {
			return ""
		}
		return fmt.Sprintf("map key type %s is not supported — a struct used as a key must derive Eq and Hash: %s", typeLabel(k), deriveHint(kt.Name, "cmp.Eq, cmp.Hash", ""))
	case ast.EnumType:
		if c.typeImplsEqAndHash(kt.Name) {
			return ""
		}
		return fmt.Sprintf("map key type %s is not supported — an enum used as a key must derive Eq and Hash: %s", typeLabel(k), deriveHint(kt.Name, "cmp.Eq, cmp.Hash", ""))
	case ast.TupleType, ast.ArrayType:
		if ast.StructuralMapKey(k) {
			return ""
		}
		return fmt.Sprintf("map key type %s is not supported — a tuple or array used as a key may hold only integers, booleans, chars, strings, and such tuples and arrays", typeLabel(k))
	}
	return fmt.Sprintf("map key type %s is not yet supported — use i32, string, or a struct/enum with `@derive(cmp.Eq, cmp.Hash)`, which requires %s", typeLabel(k), cmpImport)
}

type deriveConf struct {
	impls   map[string]map[string]bool
	methods map[string]map[string]bool
}

func newDeriveConf(prog *ast.Program) *deriveConf {
	dc := &deriveConf{impls: map[string]map[string]bool{}, methods: map[string]map[string]bool{}}
	add := func(m map[string]map[string]bool, k1, k2 string) {
		if m[k1] == nil {
			m[k1] = map[string]bool{}
		}
		m[k1][k2] = true
	}
	for _, im := range prog.Impls {
		if tn, ok := methodTypeName(im.Type); ok {
			add(dc.impls, im.Trait, tn)
		}
	}
	for _, sd := range prog.Structs {
		for _, dn := range sd.Derives {
			add(dc.impls, dn, sd.Name)
		}
	}
	for _, ed := range prog.Enums {
		for _, dn := range ed.Derives {
			add(dc.impls, dn, ed.Name)
		}
	}
	for _, fn := range prog.Funcs {
		if fn.Receiver == nil {
			continue
		}
		if tn, ok := methodTypeName(fn.Receiver.Type); ok {
			add(dc.methods, tn, fn.Name)
		}
	}
	return dc
}

func (dc *deriveConf) conforms(td *ast.TraitDecl, dn, tn string) bool {
	if dc.impls[dn][tn] {
		return true
	}
	required := false
	for _, m := range td.Methods {
		if m.Body != nil {
			continue
		}
		required = true
		if !dc.methods[tn][m.Name] {
			return false
		}
	}
	return required
}

func (dc *deriveConf) deriveFieldGap(td *ast.TraitDecl, dn string, labels []string, types []ast.Type, typeParams []string) (string, string, bool) {
	for i, t := range types {
		tn, ok := methodTypeName(t)
		if !ok || slices.Contains(typeParams, tn) {
			continue
		}
		if !dc.conforms(td, dn, tn) {
			return labels[i], tn, true
		}
	}
	return "", "", false
}

func (c *checker) preCheckDeriveFields(dc *deriveConf, td *ast.TraitDecl, dn, kind, what string, p ast.Position, labels []string, types []ast.Type, typeParams []string) bool {
	if kind == "" {
		return false
	}
	if kind == "Default" {
		labels, types = nominalFields(labels, types)
	}
	label, tn, bad := dc.deriveFieldGap(td, dn, labels, types, typeParams)
	if !bad {
		return false
	}
	c.errfCode(p, "E021", "cannot @derive(%s) for %s: %s of type %s does not implement %s — add `impl %s for %s` (or remove the derive)",
		demangle(dn), demangle(what), label, demangle(tn), demangle(dn), demangle(dn), demangle(tn))
	return true
}

func nominalFields(labels []string, types []ast.Type) ([]string, []ast.Type) {
	var ls []string
	var ts []ast.Type
	for i, t := range types {
		switch t.(type) {
		case ast.StructType, ast.EnumType:
			ls = append(ls, labels[i])
			ts = append(ts, t)
		}
	}
	return ls, ts
}

func (c *checker) synthesizeDerives(prog *ast.Program) {
	var dc *deriveConf
	for _, sd := range prog.Structs {
		if len(sd.Derives) > 0 {
			dc = newDeriveConf(prog)
			break
		}
	}
	if dc == nil {
		for _, ed := range prog.Enums {
			if len(ed.Derives) > 0 {
				dc = newDeriveConf(prog)
				break
			}
		}
	}
	for _, sd := range prog.Structs {
		if len(sd.Derives) == 0 {
			continue
		}
		derives := sd.Derives
		sd.Derives = nil
		recvType, implTypeParams := deriveRecvStruct(sd)
		for _, dn := range derives {
			td, ok := c.info.Traits[dn]
			if !ok {
				c.errfCode(sd.P, "E021", "@derive(%s): unknown trait", demangle(dn))
				continue
			}
			kind := deriveKind(dn)
			if kind == "" {
				c.errfCode(sd.P, "E021", "cannot @derive(%s): only Eq, Display, Debug, Ord, Hash, Json, FromJson, and Default are derivable", demangle(dn))
				continue
			}
			labels := make([]string, len(sd.Fields))
			types := make([]ast.Type, len(sd.Fields))
			for i, f := range sd.Fields {
				labels[i] = "field " + f.Name
				types[i] = f.Type
			}
			if kind == "FromJson" {
				elems, ok := c.fromJsonDeriveTypes(prog, sd.P, dn, sd.Name, labels, types)
				if !ok {
					continue
				}
				types = elems
			}
			if c.preCheckDeriveFields(dc, td, dn, kind, sd.Name, sd.P, labels, types, sd.TypeParams) {
				continue
			}
			var method *ast.FuncDecl
			switch kind {
			case "Eq":
				method = synthEq(sd, recvType)
			case "Display":
				method = synthDisplay(sd, recvType)
			case "Debug":
				method = synthDebug(sd, recvType)
			case "Ord":
				method = synthOrd(sd, recvType)
			case "Hash":
				method = synthHash(sd, recvType)
			case "Json":
				method = synthJson(sd, recvType)
			case "FromJson":
				method = synthFromJsonValue(sd, recvType)
				text := synthFromJsonText(recvType.Name, recvType)
				text.SourceModule = sd.SourceModule
				text.ImplTrait = dn
				bindDeriveTypeParams(text, implTypeParams, dn)
				prog.Funcs = append(prog.Funcs, text)
			case "Default":
				m, badField, badType := synthDefault(sd, recvType)
				if m == nil {
					c.errfCode(sd.P, "E021", "cannot @derive(%s) for %s: field %q has type %s, which has no default; implement %s by hand", demangle(dn), demangle(sd.Name), badField, badType, demangle(dn))
					continue
				}
				method = m
			}
			method.SourceModule = sd.SourceModule
			method.ImplTrait = dn
			bindDeriveTypeParams(method, implTypeParams, dn)
			prog.Funcs = append(prog.Funcs, method)
			prog.Impls = append(prog.Impls, &ast.ImplDecl{
				P: sd.P, Trait: dn, TraitPos: sd.P, Type: recvType, TypePos: sd.P,
				MethodNames: []string{method.Name}, SourceModule: sd.SourceModule,
				TypeParams: implTypeParams,
			})
		}
	}
	for _, ed := range prog.Enums {
		if len(ed.Derives) == 0 {
			continue
		}
		derives := ed.Derives
		ed.Derives = nil
		recvType, implTypeParams := deriveRecvEnum(ed)
		for _, dn := range derives {
			td, ok := c.info.Traits[dn]
			if !ok {
				c.errfCode(ed.P, "E021", "@derive(%s): unknown trait", demangle(dn))
				continue
			}
			var labels []string
			var types []ast.Type
			for _, v := range ed.Variants {
				for _, pt := range v.Payloads {
					labels = append(labels, "variant "+v.Name+" payload")
					types = append(types, pt)
				}
			}
			if deriveKind(dn) == "FromJson" {
				elems, ok := c.fromJsonDeriveTypes(prog, ed.P, dn, ed.Name, labels, types)
				if !ok {
					continue
				}
				types = elems
			}
			if c.preCheckDeriveFields(dc, td, dn, deriveKind(dn), ed.Name, ed.P, labels, types, ed.TypeParams) {
				continue
			}
			var method *ast.FuncDecl
			switch deriveKind(dn) {
			case "Eq":
				method = synthEnumEq(ed, recvType)
			case "Display":
				method = synthEnumDisplay(ed, recvType)
			case "Debug":
				method = synthEnumDebug(ed, recvType)
			case "Ord":
				method = synthEnumOrd(ed, recvType)
			case "Hash":
				method = synthEnumHash(ed, recvType)
			case "Json":
				method = synthEnumJson(ed, recvType)
			case "FromJson":
				method = synthEnumFromJsonValue(ed, recvType)
				text := synthFromJsonText(recvType.Name, recvType)
				text.SourceModule = ed.SourceModule
				text.ImplTrait = dn
				bindDeriveTypeParams(text, implTypeParams, dn)
				prog.Funcs = append(prog.Funcs, text)
			case "Default":
				m, badType := synthEnumDefault(ed, recvType)
				if m == nil {
					c.errfCode(ed.P, "E021", "cannot @derive(%s) for enum %s: first variant has a payload of type %s, which has no default; implement %s by hand", demangle(dn), demangle(ed.Name), badType, demangle(dn))
					continue
				}
				method = m
			default:
				c.errfCode(ed.P, "E021", "cannot @derive(%s): only Eq, Display, Debug, Ord, Hash, Json, FromJson, and Default are derivable", demangle(dn))
				continue
			}
			method.SourceModule = ed.SourceModule
			method.ImplTrait = dn
			bindDeriveTypeParams(method, implTypeParams, dn)
			prog.Funcs = append(prog.Funcs, method)
			prog.Impls = append(prog.Impls, &ast.ImplDecl{
				P: ed.P, Trait: dn, TraitPos: ed.P, Type: recvType, TypePos: ed.P,
				MethodNames: []string{method.Name}, SourceModule: ed.SourceModule,
				TypeParams: implTypeParams,
			})
		}
	}
}

func (c *checker) synthesizeTraitDefaults(prog *ast.Program) {
	for _, impl := range prog.Impls {
		td, ok := c.info.Traits[impl.Trait]
		if !ok {
			continue
		}
		provided := map[string]bool{}
		for _, mn := range impl.MethodNames {
			provided[mn] = true
		}
		for _, m := range td.Methods {
			if m.Body == nil || provided[m.Name] {
				continue
			}
			method := &ast.FuncDecl{
				P:              m.P,
				NamePos:        m.P,
				Name:           m.Name,
				ReturnType:     m.Result,
				Body:           ast.CloneBlock(m.Body),
				SourceModule:   impl.SourceModule,
				SourceFile:     td.SourceModule,
				DeclName:       m.Name,
				DefiningModule: td.SourceModule,
				ImplTrait:      impl.Trait,
				TraitDefault:   impl.Trait,
			}
			if m.Assoc {
				method.Params = append([]ast.Param(nil), m.Params...)
				method.AssocConst = m.Const
				if tn, ok := methodTypeName(impl.Type); ok {
					method.AssocType = tn
				} else {
					method.AssocType = impl.Type.String()
				}
			} else {
				recv := m.Params[0]
				method.Receiver = &ast.Param{Name: recv.Name, Type: impl.Type, Own: recv.Own}
				method.Params = append([]ast.Param(nil), m.Params[1:]...)
			}
			own, renamed := renameApart(m.TypeParams, impl.TypeParams, impl.TraitArgs)
			ownTo := make([]ast.Type, len(own))
			for i, v := range own {
				ownTo[i] = ast.ParamType{Name: v}
			}
			substTraitArgs(method, m.TypeParams, ownTo)
			substTraitArgs(method, td.TypeParams, implTraitArgs(impl))
			method.ReturnType = ast.SubstSelf(method.ReturnType, impl.Type)
			method.Params = substSelfParams(method.Params, impl.Type)
			ast.Walk(method.Body, func(n ast.Node) bool {
				forEachDeclaredType(n, func(t *ast.Type, _ ast.Position) {
					*t = ast.SubstSelf(*t, impl.Type)
				})
				return true
			})
			if len(impl.TypeParams) > 0 || len(own) > 0 {
				method.TypeParams = append(append([]string(nil), impl.TypeParams...), own...)
				method.Bounds = mergeBounds(impl.Bounds, m.Bounds, renamed)
				method.BoundArgs = mergeBounds(impl.BoundArgs, m.BoundArgs, renamed)
			}
			prog.Funcs = append(prog.Funcs, method)
			impl.MethodNames = append(impl.MethodNames, m.Name)
		}
	}
}

func (c *checker) cloneSpelling(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = c.cloneSpelling(a)
		}
		if name := ast.MangleInst(x.Name, args); c.info.Structs[name] != nil {
			return ast.StructType{Name: name}
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = c.cloneSpelling(a)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i, e := range x.Elems {
			out.Elems[i] = c.cloneSpelling(e)
		}
		return out
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.cloneSpelling(x.Elem)}
	}
	return t
}

func wrapsSelf(t ast.Type) bool {
	switch x := t.(type) {
	case ast.StructType:
		for _, a := range x.Args {
			if mentionsSelf(a) || wrapsSelf(a) {
				return true
			}
		}
	case ast.EnumType:
		return slices.ContainsFunc(x.Args, wrapsSelf)
	case ast.TupleType:
		return slices.ContainsFunc(x.Elems, wrapsSelf)
	case ast.ArrayType:
		return wrapsSelf(x.Elem)
	}
	return false
}

func renameApart(own, impl []string, args []ast.Type) ([]string, map[string]string) {
	taken := map[string]bool{}
	for _, n := range impl {
		taken[n] = true
	}
	for _, a := range args {
		taken[a.String()] = true
	}
	for _, n := range own {
		taken[n] = true
	}
	var out []string
	var renamed map[string]string
	for _, n := range own {
		fresh := n
		if slices.Contains(impl, n) || slices.ContainsFunc(args, func(a ast.Type) bool { return a.String() == n }) {
			for k := 1; taken[fresh]; k++ {
				fresh = fmt.Sprintf("%s%d", n, k)
			}
			taken[fresh] = true
			if renamed == nil {
				renamed = map[string]string{}
			}
			renamed[n] = fresh
		}
		out = append(out, fresh)
	}
	return out, renamed
}

func mergeBounds[V any](impl, own map[string]V, renamed map[string]string) map[string]V {
	if len(own) == 0 {
		return impl
	}
	out := make(map[string]V, len(impl)+len(own))
	for k, v := range impl {
		out[k] = v
	}
	for k, v := range own {
		if r, ok := renamed[k]; ok {
			k = r
		}
		out[k] = v
	}
	return out
}

func implTraitArgs(impl *ast.ImplDecl) []ast.Type {
	if len(impl.TypeParams) == 0 {
		return impl.TraitArgs
	}
	tps := make(map[string]bool, len(impl.TypeParams))
	for _, tp := range impl.TypeParams {
		tps[tp] = true
	}
	out := make([]ast.Type, len(impl.TraitArgs))
	for i, a := range impl.TraitArgs {
		out[i] = normalizeParamRefs(a, tps)
	}
	return out
}

func substTraitArgs(method *ast.FuncDecl, params []string, args []ast.Type) {
	if len(params) == 0 || len(params) != len(args) {
		return
	}
	sub := make(map[string]ast.Type, len(params))
	for i, p := range params {
		sub[p] = args[i]
	}
	method.ReturnType = substByName(method.ReturnType, sub)
	for i := range method.Params {
		method.Params[i].Type = substByName(method.Params[i].Type, sub)
	}
	if method.Body != nil {
		ast.Walk(method.Body, func(n ast.Node) bool {
			forEachDeclaredType(n, func(t *ast.Type, _ ast.Position) {
				*t = substByName(*t, sub)
			})
			return true
		})
	}
}

func substSelfParams(params []ast.Param, self ast.Type) []ast.Param {
	if len(params) == 0 {
		return nil
	}
	out := make([]ast.Param, len(params))
	for i, p := range params {
		out[i] = p
		out[i].Type = ast.SubstSelf(p.Type, self)
	}
	return out
}

func deriveRecvStruct(sd *ast.StructDecl) (ast.StructType, []string) {
	if len(sd.TypeParams) == 0 {
		return ast.StructType{Name: sd.Name}, nil
	}
	args := make([]ast.Type, len(sd.TypeParams))
	for i, tp := range sd.TypeParams {
		args[i] = ast.ParamType{Name: tp}
	}
	return ast.StructType{Name: sd.Name, Args: args}, sd.TypeParams
}

func deriveRecvEnum(ed *ast.EnumDecl) (ast.EnumType, []string) {
	if len(ed.TypeParams) == 0 {
		return ast.EnumType{Name: ed.Name}, nil
	}
	args := make([]ast.Type, len(ed.TypeParams))
	for i, tp := range ed.TypeParams {
		args[i] = ast.ParamType{Name: tp}
	}
	return ast.EnumType{Name: ed.Name, Args: args}, ed.TypeParams
}

func (c *checker) collectFreeTypeVars(t ast.Type, out *[]string, seen map[string]bool, src string) {
	named := func(name string, args []ast.Type) {
		if !c.nominalVisibleFrom(name, src) {
			if !seen[name] {
				seen[name] = true
				*out = append(*out, name)
			}
			return
		}
		for i := range args {
			c.collectFreeTypeVars(args[i], out, seen, src)
		}
	}
	switch x := t.(type) {
	case ast.StructType:
		named(x.Name, x.Args)
	case ast.EnumType:
		named(x.Name, x.Args)
	case ast.ArrayType:
		c.collectFreeTypeVars(x.Elem, out, seen, src)
	case ast.SliceType:
		c.collectFreeTypeVars(x.Elem, out, seen, src)
	case ast.TupleType:
		for i := range x.Elems {
			c.collectFreeTypeVars(x.Elems[i], out, seen, src)
		}
	}
}

func (c *checker) nominalVisibleFrom(name, src string) bool {
	if sd, ok := c.info.Structs[name]; ok {
		return c.moduleSees(src, sd.SourceModule)
	}
	if ed, ok := c.info.Enums[name]; ok {
		return c.moduleSees(src, ed.SourceModule)
	}
	return false
}

func (c *checker) moduleSees(from, decl string) bool {
	if from == "" || decl == "" || from == decl {
		return true
	}
	if strings.HasPrefix(from, "stdlib://") && strings.HasPrefix(decl, "stdlib://") {
		return true
	}
	if c.info.ModuleImports == nil {
		return true
	}
	return c.info.ModuleImports[from][decl]
}

func (c *checker) moduleSeesVariant(from, decl string) bool {
	if from == "" || decl == "" || from == decl {
		return true
	}
	if c.info.ModuleImports == nil {
		return true
	}
	return c.info.ModuleImports[from][decl]
}

func bindDeriveTypeParams(method *ast.FuncDecl, typeParams []string, trait string) {
	if len(typeParams) == 0 {
		return
	}
	method.TypeParams = typeParams
	method.Bounds = make(map[string][]string, len(typeParams))
	for _, tp := range typeParams {
		method.Bounds[tp] = []string{trait}
	}
}

func synthEnumEq(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		sBind := make([]string, len(v.Payloads))
		oBind := make([]string, len(v.Payloads))
		for i := range v.Payloads {
			sBind[i] = fmt.Sprintf("__s%d", i)
			oBind[i] = fmt.Sprintf("__o%d", i)
		}
		var eqExpr ast.Expr = &ast.BoolLit{Value: true}
		for i := range v.Payloads {
			cmp := methodCall(&ast.Ident{Name: sBind[i]}, "eq", &ast.Ident{Name: oBind[i]})
			if i == 0 {
				eqExpr = cmp
			} else {
				eqExpr = &ast.Binary{Op: "&&", Left: eqExpr, Right: cmp}
			}
		}
		inner := &ast.Match{Tag: &ast.Ident{Name: "other"}, Arms: []*ast.MatchArm{
			{VariantName: v.Name, Bindings: oBind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: eqExpr}}}},
			{IsWildcard: true, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: &ast.BoolLit{Value: false}}}}},
		}}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: sBind, Body: &ast.Block{Stmts: []ast.Stmt{inner}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.BoolLit{Value: false}},
	}}
	return &ast.FuncDecl{
		Name: "eq", Receiver: &ast.Param{Name: "self", Type: recv},
		Params: []ast.Param{{Name: "other", Type: recv}}, ReturnType: ast.BoolType{}, Body: body,
	}
}

func synthEnumDisplay(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for i := range v.Payloads {
			bind[i] = fmt.Sprintf("__p%d", i)
		}
		var expr ast.Expr = &ast.StringLit{Value: v.Name}
		if len(v.FieldNames) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: " { "})
			for i, fn := range v.FieldNames {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(&ast.StringLit{Value: fn + ": "})
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_string"))
			}
			add(&ast.StringLit{Value: " }"})
		} else if len(v.Payloads) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: "("})
			for i := range v.Payloads {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_string"))
			}
			add(&ast.StringLit{Value: ")"})
		}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.StringLit{Value: ""}},
	}}
	return &ast.FuncDecl{
		Name: "to_string", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{}, Body: body,
	}
}

func synthEnumDebug(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for i := range v.Payloads {
			bind[i] = fmt.Sprintf("__p%d", i)
		}
		var expr ast.Expr = &ast.StringLit{Value: v.Name}
		if len(v.FieldNames) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: " { "})
			for i, fn := range v.FieldNames {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(&ast.StringLit{Value: fn + ": "})
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_debug"))
			}
			add(&ast.StringLit{Value: " }"})
		} else if len(v.Payloads) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: "("})
			for i := range v.Payloads {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_debug"))
			}
			add(&ast.StringLit{Value: ")"})
		}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.StringLit{Value: ""}},
	}}
	return &ast.FuncDecl{
		Name: "to_debug", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{}, Body: body,
	}
}

func synthEnumOrd(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	negOne := func() ast.Expr {
		return &ast.Binary{Op: "-", Left: &ast.NumberLit{Value: 0}, Right: &ast.NumberLit{Value: 1}}
	}
	retBlock := func(e ast.Expr) *ast.Block {
		return &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: e}}}
	}
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for i, vi := range ed.Variants {
		sBind := make([]string, len(vi.Payloads))
		for k := range vi.Payloads {
			sBind[k] = fmt.Sprintf("__s%d", k)
		}
		innerArms := make([]*ast.MatchArm, 0, len(ed.Variants))
		for j, vj := range ed.Variants {
			oBind := make([]string, len(vj.Payloads))
			for k := range vj.Payloads {
				oBind[k] = fmt.Sprintf("__o%d", k)
			}
			var body *ast.Block
			if j < i {
				body = retBlock(&ast.NumberLit{Value: 1})
			} else if j > i {
				body = retBlock(negOne())
			} else {
				var stmts []ast.Stmt
				for k := range vi.Payloads {
					cn := fmt.Sprintf("__c%d", k)
					stmts = append(stmts, &ast.Var{
						Name: cn, Type: ast.NumberType{},
						Init: methodCall(&ast.Ident{Name: sBind[k]}, "cmp", &ast.Ident{Name: oBind[k]}),
					})
					stmts = append(stmts, &ast.If{
						Cond: &ast.Binary{Op: "!=", Left: &ast.Ident{Name: cn}, Right: &ast.NumberLit{Value: 0}},
						Then: retBlock(&ast.Ident{Name: cn}),
					})
				}
				stmts = append(stmts, &ast.Return{Value: &ast.NumberLit{Value: 0}})
				body = &ast.Block{Stmts: stmts}
			}
			innerArms = append(innerArms, &ast.MatchArm{VariantName: vj.Name, Bindings: oBind, Body: body})
		}
		inner := &ast.Match{Tag: &ast.Ident{Name: "other"}, Arms: innerArms}
		arms = append(arms, &ast.MatchArm{VariantName: vi.Name, Bindings: sBind, Body: &ast.Block{Stmts: []ast.Stmt{inner}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.NumberLit{Value: 0}},
	}}
	return &ast.FuncDecl{
		Name: "cmp", Receiver: &ast.Param{Name: "self", Type: recv},
		Params: []ast.Param{{Name: "other", Type: recv}}, ReturnType: ast.NumberType{}, Body: body,
	}
}

func selfField(name string) ast.Expr {
	return &ast.FieldAccess{Target: &ast.Ident{Name: "self"}, Field: name}
}
func otherField(name string) ast.Expr {
	return &ast.FieldAccess{Target: &ast.Ident{Name: "other"}, Field: name}
}

func methodCall(recv ast.Expr, m string, args ...ast.Expr) ast.Expr {
	return &ast.Call{Callee: &ast.FieldAccess{Target: recv, Field: m}, Args: args}
}

func synthEq(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.BoolLit{Value: true}
	for i, f := range sd.Fields {
		cmp := methodCall(selfField(f.Name), "eq", otherField(f.Name))
		if i == 0 {
			expr = cmp
		} else {
			expr = &ast.Binary{Op: "&&", Left: expr, Right: cmp}
		}
	}
	return &ast.FuncDecl{
		Name:       "eq",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		Params:     []ast.Param{{Name: "other", Type: recv}},
		ReturnType: ast.BoolType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

func synthDisplay(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.StringLit{Value: demangle(sd.Name) + " {"}
	add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
	for i, f := range sd.Fields {
		sep := " "
		if i > 0 {
			sep = ", "
		}
		add(&ast.StringLit{Value: sep + f.Name + ": "})
		add(methodCall(selfField(f.Name), "to_string"))
	}
	if len(sd.Fields) == 0 {
		add(&ast.StringLit{Value: "}"})
	} else {
		add(&ast.StringLit{Value: " }"})
	}
	return &ast.FuncDecl{
		Name:       "to_string",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

func synthDebug(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.StringLit{Value: demangle(sd.Name) + " {"}
	add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
	for i, f := range sd.Fields {
		sep := " "
		if i > 0 {
			sep = ", "
		}
		add(&ast.StringLit{Value: sep + f.Name + ": "})
		add(methodCall(selfField(f.Name), "to_debug"))
	}
	if len(sd.Fields) == 0 {
		add(&ast.StringLit{Value: "}"})
	} else {
		add(&ast.StringLit{Value: " }"})
	}
	return &ast.FuncDecl{
		Name:       "to_debug",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

func synthOrd(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var stmts []ast.Stmt
	for i, f := range sd.Fields {
		vn := fmt.Sprintf("__c%d", i)
		stmts = append(stmts, &ast.Var{
			Name: vn, Type: ast.NumberType{},
			Init: methodCall(selfField(f.Name), "cmp", otherField(f.Name)),
		})
		stmts = append(stmts, &ast.If{
			Cond: &ast.Binary{Op: "!=", Left: &ast.Ident{Name: vn}, Right: &ast.NumberLit{Value: 0}},
			Then: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: &ast.Ident{Name: vn}}}},
		})
	}
	stmts = append(stmts, &ast.Return{Value: &ast.NumberLit{Value: 0}})
	return &ast.FuncDecl{
		Name:       "cmp",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		Params:     []ast.Param{{Name: "other", Type: recv}},
		ReturnType: ast.NumberType{},
		Body:       &ast.Block{Stmts: stmts},
	}
}

const (
	hashSeed = 17
	hashMul  = 31
)

func hashFold(stmts []ast.Stmt, e ast.Expr) []ast.Stmt {
	return append(stmts, &ast.ExprStmt{Expr: &ast.Assign{
		Target: &ast.Ident{Name: "__h"},
		Value: &ast.Binary{
			Op:    "+",
			Left:  &ast.Binary{Op: "*", Left: &ast.Ident{Name: "__h"}, Right: &ast.NumberLit{Value: hashMul}},
			Right: methodCall(e, "hash"),
		},
	}})
}

func synthHash(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	stmts := []ast.Stmt{
		&ast.Var{Name: "__h", Type: ast.NumberType{}, Init: &ast.NumberLit{Value: hashSeed}},
	}
	for _, f := range sd.Fields {
		stmts = hashFold(stmts, selfField(f.Name))
	}
	stmts = append(stmts, &ast.Return{Value: &ast.Ident{Name: "__h"}})
	return &ast.FuncDecl{
		Name:       "hash",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.NumberType{},
		Body:       &ast.Block{Stmts: stmts},
	}
}

func synthEnumHash(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for i, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for k := range v.Payloads {
			bind[k] = fmt.Sprintf("__p%d", k)
		}
		stmts := []ast.Stmt{
			&ast.Var{Name: "__h", Type: ast.NumberType{}, Init: &ast.NumberLit{Value: int64(hashSeed + i)}},
		}
		for k := range v.Payloads {
			stmts = hashFold(stmts, &ast.Ident{Name: bind[k]})
		}
		stmts = append(stmts, &ast.Return{Value: &ast.Ident{Name: "__h"}})
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: stmts}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.NumberLit{Value: 0}},
	}}
	return &ast.FuncDecl{
		Name: "hash", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.NumberType{}, Body: body,
	}
}

func defaultDeriveExpr(t ast.Type) (ast.Expr, bool) {
	switch ft := t.(type) {
	case ast.NumberType:
		return &ast.NumberLit{}, true
	case ast.FloatType:
		return &ast.FloatLit{}, true
	case ast.BoolType:
		return &ast.BoolLit{Value: false}, true
	case ast.StringType:
		return &ast.StringLit{Value: ""}, true
	case ast.StructType:
		return defaultAssocCall(ft.Name), true
	case ast.EnumType:
		return defaultAssocCall(ft.Name), true
	case ast.ParamType:
		return defaultAssocCall(ft.Name), true
	}
	return nil, false
}

func defaultAssocCall(typeName string) ast.Expr {
	return &ast.Call{Callee: &ast.FieldAccess{Target: &ast.Ident{Name: typeName}, Field: "default"}}
}

func synthDefault(sd *ast.StructDecl, recv ast.StructType) (*ast.FuncDecl, string, ast.Type) {
	fields := make([]ast.FieldInit, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		dv, ok := defaultDeriveExpr(f.Type)
		if !ok {
			return nil, f.Name, f.Type
		}
		fields = append(fields, ast.FieldInit{Name: f.Name, Value: dv})
	}
	lit := &ast.StructLit{TypeName: recv.Name, Fields: fields, TypeArgs: recv.Args}
	return &ast.FuncDecl{
		Name:       "default",
		AssocType:  recv.Name,
		ReturnType: recv,
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: lit}}},
	}, "", nil
}

func synthEnumDefault(ed *ast.EnumDecl, recv ast.EnumType) (*ast.FuncDecl, ast.Type) {
	if len(ed.Variants) == 0 {
		return nil, nil
	}
	v0 := ed.Variants[0]
	var value ast.Expr
	if len(v0.Payloads) == 0 {
		value = &ast.Ident{Name: v0.Name, EnumName: recv.Name}
	} else {
		args := make([]ast.Expr, len(v0.Payloads))
		for i, pt := range v0.Payloads {
			dv, ok := defaultDeriveExpr(pt)
			if !ok {
				return nil, pt
			}
			args[i] = dv
		}
		value = &ast.Call{Callee: &ast.Ident{Name: v0.Name, EnumName: recv.Name}, Args: args}
	}
	return &ast.FuncDecl{
		Name:       "default",
		AssocType:  recv.Name,
		ReturnType: recv,
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: value}}},
	}, nil
}

func synthJson(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.StringLit{Value: "{"}
	add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
	for i, f := range sd.Fields {
		sep := ""
		if i > 0 {
			sep = ","
		}
		add(&ast.StringLit{Value: sep + "\"" + f.Name + "\":"})
		add(methodCall(selfField(f.Name), "to_json"))
	}
	add(&ast.StringLit{Value: "}"})
	return &ast.FuncDecl{
		Name:       "to_json",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

func synthFromJsonValue(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	jcall, variant, ret := fjCall, fjVariant, fjReturn
	fis := make([]ast.FieldInit, len(sd.Fields))
	for i, f := range sd.Fields {
		fis[i] = ast.FieldInit{Name: f.Name, Value: &ast.Ident{Name: "__fj_" + f.Name}}
	}
	body := ret(variant("Ok", &ast.StructLit{TypeName: recv.Name, Fields: fis, TypeArgs: recv.Args}))
	for i := len(sd.Fields) - 1; i >= 0; i-- {
		f := sd.Fields[i]
		value := "__fj_v_" + f.Name
		errBind := "__fj_e_" + f.Name
		getter := "from_json_field"
		if container, _ := fromJsonContainer(f.Type); container == "option" {
			getter = "from_json_optional_field"
		}
		decode := fromJsonDecode(f.Type, value)
		inner := &ast.Match{
			Tag: decode,
			Arms: []*ast.MatchArm{
				{VariantName: "Ok", Bindings: []string{"__fj_" + f.Name}, Body: body},
				{VariantName: "Err", Bindings: []string{errBind}, Body: ret(variant("Err",
					jcall("from_json_at", &ast.StringLit{Value: f.Name}, &ast.Ident{Name: errBind})))},
			},
		}
		outer := &ast.Match{
			Tag: jcall(getter, &ast.Ident{Name: "v"}, &ast.StringLit{Value: f.Name}),
			Arms: []*ast.MatchArm{
				{VariantName: "Ok", Bindings: []string{value}, Body: &ast.Block{Stmts: []ast.Stmt{inner}}},
				{VariantName: "Err", Bindings: []string{errBind}, Body: ret(variant("Err", &ast.Ident{Name: errBind}))},
			},
		}
		body = &ast.Block{Stmts: []ast.Stmt{outer}}
	}
	return &ast.FuncDecl{
		Name:       "from_json_value",
		AssocType:  recv.Name,
		Params:     []ast.Param{{Name: "v", Type: ast.EnumType{Name: "json__JsonValue"}}},
		ReturnType: ast.EnumType{Name: "Result", Args: []ast.Type{recv, ast.StringType{}}},
		Body:       body,
	}
}

func (c *checker) fromJsonDeriveTypes(prog *ast.Program, p ast.Position, dn, name string, labels []string, types []ast.Type) ([]ast.Type, bool) {
	if !hasStdJson(prog) {
		c.errfCode(p, "E021", "cannot @derive(%s): the decoder is std/json's; import \"std/json\"", demangle(dn))
		return nil, false
	}
	elems, bad, ok := fromJsonFieldTypes(types)
	if !ok {
		c.errfCode(p, "E021", "cannot @derive(%s) for %s: %s has type %s; FromJson decodes a field of a FromJson type, an array of one or an Option of one", demangle(dn), demangle(name), labels[bad], types[bad])
		return nil, false
	}
	return elems, true
}

func synthEnumFromJsonValue(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	var stmts []ast.Stmt
	for _, v := range ed.Variants {
		stmts = append(stmts, &ast.If{
			Cond: &ast.Binary{Op: "==", Left: &ast.Ident{Name: "__fj_tag"}, Right: &ast.StringLit{Value: v.Name}},
			Then: enumFromJsonVariant(v, recv),
		})
	}
	stmts = append(stmts, fjReturn(fjVariant("Err", fjCall("from_json_unknown_variant", &ast.Ident{Name: "__fj_tag"}))))
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{
			Tag: fjCall("from_json_variant", &ast.Ident{Name: "v"}),
			Arms: []*ast.MatchArm{
				{VariantName: "Ok", Bindings: []string{"__fj_tag"}, Body: &ast.Block{Stmts: stmts}},
				{VariantName: "Err", Bindings: []string{"__fj_e"}, Body: fjReturn(fjVariant("Err", &ast.Ident{Name: "__fj_e"}))},
			},
		},
		&ast.Return{Value: fjVariant("Err", &ast.StringLit{Value: ""})},
	}}
	return &ast.FuncDecl{
		Name:       "from_json_value",
		AssocType:  recv.Name,
		Params:     []ast.Param{{Name: "v", Type: ast.EnumType{Name: "json__JsonValue"}}},
		ReturnType: ast.EnumType{Name: "Result", Args: []ast.Type{recv, ast.StringType{}}},
		Body:       body,
	}
}

func enumFromJsonVariant(v ast.EnumVariant, recv ast.EnumType) *ast.Block {
	n := len(v.Payloads)
	args := make([]ast.Expr, n)
	for k := range v.Payloads {
		args[k] = &ast.Ident{Name: fmt.Sprintf("__fj_%s_%d", v.Name, k)}
	}
	var value ast.Expr = &ast.Ident{Name: v.Name}
	if n > 0 {
		value = &ast.Call{Callee: &ast.Ident{Name: v.Name}, Args: args}
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Var{Name: "__fj_r", Type: recv, Init: value, WasAnnotated: true},
		&ast.Return{Value: fjVariant("Ok", &ast.Ident{Name: "__fj_r"})},
	}}
	for k := n - 1; k >= 0; k-- {
		body = enumFromJsonPayload(v, k, body)
	}
	return body
}

func enumFromJsonPayload(v ast.EnumVariant, k int, body *ast.Block) *ast.Block {
	n := len(v.Payloads)
	pt := v.Payloads[k]
	value := fmt.Sprintf("__fj_v_%s_%d", v.Name, k)
	errBind := fmt.Sprintf("__fj_e_%s_%d", v.Name, k)
	container, _ := fromJsonContainer(pt)
	payload := fjCall("from_json_payload", &ast.Ident{Name: "v"})
	var get ast.Expr
	var why ast.Expr = &ast.Ident{Name: errBind}
	switch {
	case len(v.FieldNames) > 0:
		getter := "from_json_field"
		if container == "option" {
			getter = "from_json_optional_field"
		}
		get = fjCall(getter, payload, &ast.StringLit{Value: v.FieldNames[k]})
		why = fjCall("from_json_at", &ast.StringLit{Value: v.FieldNames[k]}, why)
	default:
		getter := "from_json_item"
		if container == "option" {
			getter = "from_json_optional_item"
		}
		get = fjCall(getter, payload, &ast.NumberLit{Value: int64(k)}, &ast.NumberLit{Value: int64(n)})
		if n > 1 {
			why = fjCall("from_json_index", &ast.NumberLit{Value: int64(k)}, why)
		}
	}
	inVariant := func(e ast.Expr) *ast.Block {
		return fjReturn(fjVariant("Err", fjCall("from_json_in", &ast.StringLit{Value: v.Name}, e)))
	}
	inner := &ast.Match{
		Tag: fromJsonDecode(pt, value),
		Arms: []*ast.MatchArm{
			{VariantName: "Ok", Bindings: []string{fmt.Sprintf("__fj_%s_%d", v.Name, k)}, Body: body},
			{VariantName: "Err", Bindings: []string{errBind}, Body: inVariant(why)},
		},
	}
	return &ast.Block{Stmts: []ast.Stmt{&ast.Match{
		Tag: get,
		Arms: []*ast.MatchArm{
			{VariantName: "Ok", Bindings: []string{value}, Body: &ast.Block{Stmts: []ast.Stmt{inner}}},
			{VariantName: "Err", Bindings: []string{errBind}, Body: inVariant(&ast.Ident{Name: errBind})},
		},
	}}}
}

func fjCall(fn string, args ...ast.Expr) ast.Expr {
	return &ast.Call{Callee: &ast.Ident{Name: "json__" + fn}, Args: args}
}

func fjVariant(name string, arg ast.Expr) ast.Expr {
	return &ast.Call{Callee: &ast.Ident{Name: name}, Args: []ast.Expr{arg}}
}

func fjReturn(e ast.Expr) *ast.Block {
	return &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: e}}}
}

func fromJsonDecode(t ast.Type, value string) ast.Expr {
	arg := []ast.Expr{&ast.Ident{Name: value}}
	switch container, _ := fromJsonContainer(t); container {
	case "array":
		return &ast.Call{Callee: &ast.Ident{Name: "json__from_json_array"}, Args: arg,
			TypeArgs: []ast.Type{fromJsonElem(t)}, TypeArgsWritten: true}
	case "option":
		return &ast.Call{Callee: &ast.Ident{Name: "json__from_json_option"}, Args: arg,
			TypeArgs: []ast.Type{fromJsonElem(t)}, TypeArgsWritten: true}
	}
	return &ast.Call{Callee: &ast.FieldAccess{Target: &ast.Ident{Name: fromJsonTypeName(t)}, Field: "from_json_value"}, Args: arg}
}

func fromJsonTypeName(t ast.Type) string {
	if tn, ok := ast.ReceiverTypeName(t); ok {
		return tn
	}
	if pt, ok := t.(ast.ParamType); ok {
		return pt.Name
	}
	return fmt.Sprint(t)
}

func synthFromJsonText(name string, recv ast.Type) *ast.FuncDecl {
	call := &ast.Call{Callee: &ast.Ident{Name: "json__decode"}, Args: []ast.Expr{&ast.Ident{Name: "s"}},
		TypeArgs: []ast.Type{recv}, TypeArgsWritten: true}
	return &ast.FuncDecl{
		Name:       "from_json",
		AssocType:  name,
		Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
		ReturnType: ast.EnumType{Name: "Result", Args: []ast.Type{recv, ast.StringType{}}},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: call}}},
	}
}

func synthEnumJson(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for k := range v.Payloads {
			bind[k] = fmt.Sprintf("__p%d", k)
		}
		var expr ast.Expr
		add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
		if len(v.FieldNames) > 0 {
			expr = &ast.StringLit{Value: "{\"" + v.Name + "\":{"}
			for k, fn := range v.FieldNames {
				if k > 0 {
					add(&ast.StringLit{Value: ","})
				}
				add(&ast.StringLit{Value: "\"" + fn + "\":"})
				add(methodCall(&ast.Ident{Name: bind[k]}, "to_json"))
			}
			add(&ast.StringLit{Value: "}}"})
		} else {
			switch len(v.Payloads) {
			case 0:
				expr = &ast.StringLit{Value: "\"" + v.Name + "\""}
			case 1:
				expr = &ast.StringLit{Value: "{\"" + v.Name + "\":"}
				add(methodCall(&ast.Ident{Name: bind[0]}, "to_json"))
				add(&ast.StringLit{Value: "}"})
			default:
				expr = &ast.StringLit{Value: "{\"" + v.Name + "\":["}
				for k := range v.Payloads {
					if k > 0 {
						add(&ast.StringLit{Value: ","})
					}
					add(methodCall(&ast.Ident{Name: bind[k]}, "to_json"))
				}
				add(&ast.StringLit{Value: "]}"})
			}
		}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.StringLit{Value: ""}},
	}}
	return &ast.FuncDecl{
		Name: "to_json", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{}, Body: body,
	}
}

func demangle(s string) string {
	if len(s) < 2 {
		return s
	}
	if cut := strings.Index(s[1:], "__"); cut >= 0 {
		cut++
		return s[:cut] + "." + s[cut+2:]
	}
	return s
}

func (c *checker) checkOpaqueAccess(sd *ast.StructDecl, pos ast.Position, what string) {
	if sd == nil || !sd.Opaque || sd.SourceModule == "" {
		return
	}
	cur := ""
	if c.current != nil {
		cur = c.current.BodyModule()
	}
	if cur != sd.SourceModule {
		c.errfCode(pos, "E021", "cannot %s opaque type %s outside the module that defines it", what, demangle(sd.Name))
	}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (c *checker) rewriteAssocConstReads(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		tps := map[string]bool{}
		for _, tp := range fn.TypeParams {
			tps[tp] = true
		}
		w := ast.ScopeWalker{}
		w.Visit = func(slot *ast.Expr, w *ast.ScopeWalker) bool {
			if call, ok := (*slot).(*ast.Call); ok {
				if fa, ok := call.Callee.(*ast.FieldAccess); ok {
					w.WalkExpr(&fa.Target)
					for i := range call.Args {
						w.WalkExpr(&call.Args[i])
					}
					return false
				}
				return true
			}
			fa, ok := (*slot).(*ast.FieldAccess)
			if !ok {
				return true
			}
			tid, ok := fa.Target.(*ast.Ident)
			if !ok || w.IsBound(tid.Name) || !c.assocConstBase(tid.Name, fa.Field, tps, fn.MethodRecv != "") {
				return true
			}
			*slot = &ast.Call{P: fa.P, Callee: fa, AssocConst: true}
			return false
		}
		w.WalkFunc(fn)
	}
}

func (c *checker) assocConstBase(name, field string, tps map[string]bool, inMethod bool) bool {
	if name == "Self" {
		return inMethod
	}
	if tps[name] || isPrimitiveTypeName(name) {
		return true
	}
	if _, ok := c.info.Structs[name]; ok {
		return true
	}
	if c.info.Enums[name] != nil {
		got, _, found := c.resolveMethod(name, field, nil)
		return found && strings.HasPrefix(got, "__assoc_")
	}
	return false
}

func (c *checker) assocConstReadOK(n *ast.Call, base, field string, isConst bool) bool {
	if n.AssocConst == isConst {
		return true
	}
	if n.AssocConst {
		c.errfCode(n.P, "E043", "%s.%s is an associated function, not a constant; call it as %s.%s(...)", demangle(base), field, demangle(base), field)
	} else {
		c.errfCode(n.P, "E043", "%s.%s is an associated constant; read it as %s.%s without an argument list", demangle(base), field, demangle(base), field)
	}
	return false
}

func (c *checker) resolveTraitMethodForParam(paramName, field string) (ast.TraitMethod, string, bool) {
	decl := c.typeParamDecl(paramName)
	if decl == nil {
		return ast.TraitMethod{}, "", false
	}
	direct := decl.Bounds[paramName]
	argsFor := map[string][]ast.Type{}
	if ba := decl.BoundArgs[paramName]; len(ba) == len(direct) {
		for i, tn := range direct {
			if len(ba[i]) > 0 {
				argsFor[tn] = ba[i]
			}
		}
	}
	for _, traitName := range c.expandTraits(direct) {
		td, ok := c.info.Traits[traitName]
		if !ok {
			continue
		}
		for _, m := range td.Methods {
			if m.Name == field {
				if args := argsFor[traitName]; len(args) > 0 && len(td.TypeParams) == len(args) {
					sub := make(map[string]ast.Type, len(args))
					for i, tp := range td.TypeParams {
						sub[tp] = args[i]
					}
					m = substTraitMethodTypeParams(m, sub)
				}
				return m, traitName, true
			}
		}
	}
	return ast.TraitMethod{}, "", false
}

func substTraitMethodTypeParams(m ast.TraitMethod, sub map[string]ast.Type) ast.TraitMethod {
	out := m
	out.Params = make([]ast.Param, len(m.Params))
	for i, p := range m.Params {
		p.Type = substByName(p.Type, sub)
		out.Params[i] = p
	}
	out.Result = substByName(m.Result, sub)
	return out
}

func (c *checker) collectTraitSupers(name string, seen map[string]bool, acc *[]string) {
	if seen[name] {
		return
	}
	seen[name] = true
	*acc = append(*acc, name)
	if td, ok := c.info.Traits[name]; ok {
		for _, sup := range td.Supertraits {
			c.collectTraitSupers(sup, seen, acc)
		}
	}
}

func (c *checker) traitInItsOwnSupers(name string) bool {
	seen := map[string]bool{}
	var stack []string
	if td, ok := c.info.Traits[name]; ok {
		stack = append(stack, td.Supertraits...)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == name {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if td, ok := c.info.Traits[cur]; ok {
			stack = append(stack, td.Supertraits...)
		}
	}
	return false
}

func (c *checker) expandTraits(traits []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range traits {
		c.collectTraitSupers(t, seen, &out)
	}
	return out
}

func (c *checker) methodVisibleHere(mangled string) bool {
	if c.current == nil {
		return true
	}
	return c.moduleSees(c.current.BodyModule(), c.info.MethodSources[mangled])
}

func (c *checker) methodImplementsTrait(typeName, methodName string) bool {
	for traitName, td := range c.info.Traits {
		if !c.info.Impls[traitName][typeName] {
			continue
		}
		for _, m := range td.Methods {
			if m.Name == methodName {
				return true
			}
		}
	}
	return false
}

func displayDispatchTypeName(t ast.Type) string {
	tn, _ := ast.ReceiverTypeName(t)
	return tn
}

func (c *checker) typeImplementsDisplay(t ast.Type) bool {
	switch x := t.(type) {
	case ast.ParamType:
		_, _, found := c.resolveTraitMethodForParam(x.Name, "to_string")
		return found
	case ast.DynTraitType:
		for _, tr := range x.Traits {
			td, ok := c.info.Traits[tr]
			if !ok {
				continue
			}
			for _, m := range td.Methods {
				if m.Name == "to_string" {
					return true
				}
			}
		}
		return false
	}
	tn := displayDispatchTypeName(t)
	if tn == "" {
		return false
	}
	if mangled, _, ok := c.resolveMethod(tn, "to_string", []string{"Display"}); ok && (c.methodVisibleHere(mangled) || c.methodImplementsTrait(tn, "to_string")) {
		return true
	}
	_, builtin := builtinScalarMethod(tn, "to_string")
	return builtin
}

func builtinScalarMethod(typeName, name string) (string, bool) {
	if typeName == "boolean" && name == "to_string" {
		return "__method_boolean_to_string", true
	}
	return "", false
}

func isScalarBuiltinMethod(mangled string) bool {
	return mangled == "__method_boolean_to_string"
}

type checker struct {
	entryModule       string
	callee            ast.Expr
	funcSources       map[string]string
	consts            map[string]*ast.ConstDecl
	constTypes        map[string]ast.Type
	constBusy         map[string]bool
	traitNames        map[string]bool
	info              *Info
	errors            []error
	intrinsicSigs     map[*ast.FuncType]IntrinsicKind
	seenDiags         map[diagKey]bool
	seededParams      map[string]bool
	wideLits          []wideLit
	wideSeen          map[*ast.NumberLit]bool
	negatedLits       map[*ast.NumberLit]bool
	litLocals         []*litLocal
	litLocalOf        map[*ast.Var]*litLocal
	litIdents         map[*ast.Ident]*litLocal
	litCompositeOf    map[*ast.Var][]*litLocal
	litFields         map[*ast.FieldAccess]*litLocal
	litCompositeReads map[*ast.Ident]*ast.Var
	current           *ast.FuncDecl
	loopDepth         int
	tryConvN          int
	checkedN          int
	inferReturns      *[]ast.Type
	loopLabels        []string

	ownFuncs       map[string][]bool
	resultBorrows  map[string]bool
	resultAnalysed map[string]bool
	callOwnFlags   map[*ast.Call][]bool
	scalarArgs     map[ast.Expr]bool

	shadowedGenericCalls map[*ast.Call]bool

	reservedShadows map[string]bool

	elemHint ast.Type

	typedRecv     ast.Expr
	typedRecvType ast.Type

	expectedType ast.Type

	requireMapImport bool
	mapErrReported   bool

	variantOf map[string][]variantRef

	captureSink  func(name string, t ast.Type)
	captureOuter *scope
	captureChain []captureEntry

	capturedVars map[*ast.Var]bool
	cellStores   []cellStore

	mutualRecSiblings map[string]bool
}

type captureEntry struct {
	sink  func(name string, t ast.Type)
	scope *scope
	outer *ast.FuncDecl
}

func (c *checker) typeParamDecl(name string) *ast.FuncDecl {
	if c.current != nil && containsString(c.current.TypeParams, name) {
		return c.current
	}
	for i := len(c.captureChain) - 1; i >= 0; i-- {
		if d := c.captureChain[i].outer; d != nil && containsString(d.TypeParams, name) {
			return d
		}
	}
	return nil
}

func (c *checker) resolveTypeNames(prog *ast.Program) {
	for _, td := range prog.Traits {
		var params map[string]bool
		if len(td.TypeParams) > 0 {
			params = make(map[string]bool, len(td.TypeParams))
			for _, n := range td.TypeParams {
				params[n] = true
			}
		}
		for i := range td.Methods {
			mparams := params
			if len(td.Methods[i].TypeParams) > 0 {
				mparams = make(map[string]bool, len(params)+len(td.Methods[i].TypeParams))
				for n := range params {
					mparams[n] = true
				}
				for _, n := range td.Methods[i].TypeParams {
					mparams[n] = true
				}
			}
			for j := range td.Methods[i].Params {
				c.resolveType(&td.Methods[i].Params[j].Type, mparams, td.Methods[i].P)
			}
			c.resolveType(&td.Methods[i].Result, mparams, td.Methods[i].P)
		}
	}
	for _, fn := range prog.Funcs {
		var params map[string]bool
		if len(fn.TypeParams) > 0 {
			params = make(map[string]bool, len(fn.TypeParams))
			for _, n := range fn.TypeParams {
				params[n] = true
			}
		}
		if fn.Receiver != nil {
			c.resolveType(&fn.Receiver.Type, params, orPos(fn.Receiver.NamePos, fn.P))
		}
		for i := range fn.Params {
			c.resolveType(&fn.Params[i].Type, params, orPos(fn.Params[i].NamePos, fn.P))
		}
		c.resolveType(&fn.ReturnType, params, fn.P)
		if fn.Body != nil {
			c.resolveTypesInBlock(fn.Body, params)
		}
	}
	for _, sd := range prog.Structs {
		var params map[string]bool
		if len(sd.TypeParams) > 0 {
			params = make(map[string]bool, len(sd.TypeParams))
			for _, n := range sd.TypeParams {
				params[n] = true
			}
		}
		for i := range sd.Fields {
			c.resolveType(&sd.Fields[i].Type, params, orPos(sd.Fields[i].NamePos, sd.P))
		}
	}
	for _, ed := range prog.Enums {
		var params map[string]bool
		if len(ed.TypeParams) > 0 {
			params = make(map[string]bool, len(ed.TypeParams))
			for _, n := range ed.TypeParams {
				params[n] = true
			}
		}
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				c.resolveType(&ed.Variants[i].Payloads[j], params, orPos(ed.Variants[i].P, ed.P))
			}
		}
	}
	for _, impl := range prog.Impls {
		if len(impl.TypeParams) == 0 {
			continue
		}
		params := make(map[string]bool, len(impl.TypeParams))
		for _, n := range impl.TypeParams {
			params[n] = true
		}
		c.resolveType(&impl.Type, params, impl.P)
		for name, bt := range impl.AssocTypeBindings {
			c.resolveType(&bt, params, impl.P)
			impl.AssocTypeBindings[name] = bt
		}
	}
}

func (c *checker) resolveTypesInBlock(b *ast.Block, params map[string]bool) {
	if b == nil {
		return
	}
	ast.Walk(b, func(n ast.Node) bool {
		forEachDeclaredType(n, func(t *ast.Type, pos ast.Position) {
			c.resolveType(t, params, pos)
		})
		return true
	})
}

func blockDiverges(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return stmtDiverges(b.Stmts[len(b.Stmts)-1])
}

func letElseDivergentArm(m *ast.Match, i int) bool {
	return m.Origin == ast.OriginLetElse && i == len(m.Arms)-1 && m.Arms[i].IsWildcard
}

func loopCanBreak(body ast.Stmt, label string) bool {
	found := false
	var walk func(n ast.Node, nested bool)
	walk = func(n ast.Node, nested bool) {
		ast.Walk(n, func(n ast.Node) bool {
			if found {
				return false
			}
			switch x := n.(type) {
			case *ast.Break:
				if x.Label == "" {
					found = !nested
				} else {
					found = x.Label == label
				}
				return false
			case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
				return false
			case *ast.Loop:
				walk(x.Body, true)
				return false
			case *ast.While:
				walk(x.Cond, nested)
				walk(x.Body, true)
				return false
			case *ast.For:
				if x.Init != nil {
					walk(x.Init, nested)
				}
				if x.Cond != nil {
					walk(x.Cond, nested)
				}
				if x.Step != nil {
					walk(x.Step, nested)
				}
				walk(x.Body, true)
				return false
			case *ast.ForEach:
				walk(x.Iter, nested)
				walk(x.Body, true)
				return false
			}
			return true
		})
	}
	walk(body, false)
	return found
}

func stmtDiverges(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return true
	case *ast.Loop:
		return !loopCanBreak(x.Body, x.Label)
	case *ast.While:
		if lit, ok := x.Cond.(*ast.BoolLit); ok && lit.Value {
			return !loopCanBreak(x.Body, x.Label)
		}
		return false
	case *ast.Block:
		return blockDiverges(x)
	case *ast.If:
		if x.Else == nil {
			return false
		}
		return stmtDiverges(x.Then) && stmtDiverges(x.Else)
	case *ast.Match:
		for i, arm := range x.Arms {
			if letElseDivergentArm(x, i) {
				continue
			}
			if !blockDiverges(arm.Body) {
				return false
			}
		}
		return len(x.Arms) > 0
	}
	return false
}

func funcBodyExits(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return stmtExits(b.Stmts[len(b.Stmts)-1])
}

func stmtExits(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Return:
		return true
	case *ast.Block:
		return funcBodyExits(x)
	case *ast.If:
		if lit, ok := x.Cond.(*ast.BoolLit); ok {
			if lit.Value {
				return stmtExits(x.Then)
			}
			return x.Else != nil && stmtExits(x.Else)
		}
		return x.Else != nil && stmtExits(x.Then) && stmtExits(x.Else)
	case *ast.Match:
		if len(x.Arms) == 0 {
			return false
		}
		for i, arm := range x.Arms {
			if letElseDivergentArm(x, i) {
				continue
			}
			if !funcBodyExits(arm.Body) {
				return false
			}
		}
		return true
	case *ast.While:
		if lit, ok := x.Cond.(*ast.BoolLit); ok && lit.Value {
			return !loopCanBreak(x.Body, x.Label)
		}
		return false
	case *ast.Loop:
		return !loopCanBreak(x.Body, x.Label)
	}
	return false
}

func isVoidReturn(t ast.Type) bool {
	if t == nil {
		return true
	}
	_, ok := t.(ast.VoidType)
	return ok
}

func orPos(p, fallback ast.Position) ast.Position {
	if p.Line == 0 {
		return fallback
	}
	return p
}

func (c *checker) cellPayloadRisk(t ast.Type, own []string, seen map[string]bool) ast.Type {
	switch v := t.(type) {
	case ast.NumberType, ast.FloatType, ast.BoolType, ast.StringType, ast.CharType:
		return nil
	case ast.ArrayType:
		return c.cellPayloadRisk(v.Elem, own, seen)
	case ast.TupleType:
		return c.cellPayloadRisks(v.Elems, own, seen)
	case ast.StructType:
		return c.cellNominalRisk(t, v.Name, v.Args, own, seen)
	case ast.EnumType:
		return c.cellNominalRisk(t, v.Name, v.Args, own, seen)
	case ast.ParamType:
		if slices.Contains(own, v.Name) {
			return nil
		}
		return t
	case *ast.FuncType, ast.DynTraitType, ast.HandleType, ast.StrType, ast.SliceType,
		ast.StreamType, ast.SelfType, ast.ProjType:
		return t
	}
	return nil
}

func (c *checker) cellPayloadRisks(ts []ast.Type, own []string, seen map[string]bool) ast.Type {
	for _, t := range ts {
		if r := c.cellPayloadRisk(t, own, seen); r != nil {
			return r
		}
	}
	return nil
}

func (c *checker) cellNominalRisk(t ast.Type, name string, args []ast.Type, own []string, seen map[string]bool) ast.Type {
	if len(args) == 0 && slices.Contains(own, name) {
		return nil
	}
	if name == "Cell" || name == "MapIter" {
		return t
	}
	if _, ok := c.info.Resources[name]; ok {
		return t
	}
	if r := c.cellPayloadRisks(args, own, seen); r != nil || seen[name] {
		return r
	}
	seen[name] = true
	if sd, ok := c.info.Structs[name]; ok && name != "Map" {
		for _, f := range sd.Fields {
			if r := c.cellPayloadRisk(f.Type, sd.TypeParams, seen); r != nil {
				return r
			}
		}
	}
	if ed, ok := c.info.Enums[name]; ok {
		for _, variant := range ed.Variants {
			if r := c.cellPayloadRisks(variant.Payloads, ed.TypeParams, seen); r != nil {
				return r
			}
		}
	}
	return nil
}

func (c *checker) cellElemError(at ast.Position, elem ast.Type) {
	risk := c.cellPayloadRisk(elem, nil, map[string]bool{})
	if risk == nil {
		return
	}
	why := fmt.Sprintf(", and %s contains %s", elem, risk)
	if fmt.Sprint(risk) == fmt.Sprint(elem) {
		why = fmt.Sprintf(", which %s is not", elem)
	}
	c.errfCode(at, "E057", "Cell[%s] is not allowed: a cell's element must be built from scalars, strings, arrays, tuples, maps, structs and enums%s", elem, why)
}

func (c *checker) resolveType(slot *ast.Type, params map[string]bool, pos ast.Position) {
	if slot == nil || *slot == nil {
		return
	}
	switch t := (*slot).(type) {
	case ast.ProjType:
		base := t.Base
		c.resolveType(&base, params, pos)
		*slot = ast.ProjType{Base: base, Name: t.Name}
		return
	case ast.StructType:
		if params[t.Name] {
			*slot = ast.ParamType{Name: t.Name}
			return
		}
		if _, isEnum := c.info.Enums[t.Name]; isEnum {
			*slot = ast.EnumType{Name: t.Name}
			return
		}
		if _, isResource := c.info.Resources[t.Name]; isResource && len(t.Args) == 0 {
			*slot = ast.HandleType{Resource: t.Name}
			return
		}
		if len(t.Args) > 0 {
			args := make([]ast.Type, len(t.Args))
			copy(args, t.Args)
			for i := range args {
				c.resolveType(&args[i], params, pos)
			}
			*slot = ast.StructType{Name: t.Name, Args: args}
		}
	case ast.EnumType:
		args := make([]ast.Type, len(t.Args))
		copy(args, t.Args)
		for i := range args {
			c.resolveType(&args[i], params, pos)
		}
		if sd, ok := c.info.Structs[t.Name]; ok {
			if len(sd.TypeParams) != len(args) && !c.reservedShadows[t.Name] {
				c.errfCode(sd.P, "E019", "%s", arityMsg("struct "+t.Name, len(sd.TypeParams), len(args), "type argument"))
			}
			if t.Name == "Cell" && len(args) == 1 {
				at := pos
				if at.Line == 0 {
					at = sd.P
				}
				c.cellElemError(at, args[0])
			}
			*slot = ast.StructType{Name: t.Name, Args: args}
			return
		}
		if ed, ok := c.info.Enums[t.Name]; ok && len(args) > 0 {
			if len(ed.TypeParams) != len(args) && !c.reservedShadows[t.Name] {
				c.errfCode(ed.P, "E019", "%s", arityMsg("enum "+t.Name, len(ed.TypeParams), len(args), "type argument"))
			}
		}
		*slot = ast.EnumType{Name: t.Name, Args: args}
	case ast.ArrayType:
		elem := t.Elem
		c.resolveType(&elem, params, pos)
		*slot = ast.ArrayType{Elem: elem}
	case ast.SliceType:
		elem := t.Elem
		c.resolveType(&elem, params, pos)
		*slot = ast.SliceType{Elem: elem}
	case ast.TupleType:
		elems := make([]ast.Type, len(t.Elems))
		copy(elems, t.Elems)
		for i := range elems {
			c.resolveType(&elems[i], params, pos)
		}
		*slot = ast.TupleType{Elems: elems}
	case *ast.FuncType:
		for i := range t.Params {
			c.resolveType(&t.Params[i], params, pos)
		}
		c.resolveType(&t.Result, params, pos)
	}
}

func (c *checker) validateKnownTypes(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		params := typeParamSet(fn.TypeParams)
		if fn.Receiver != nil {
			c.checkTypeKnown(fn.Receiver.Type, params, fn.P)
		}
		for i := range fn.Params {
			c.checkTypeKnown(fn.Params[i].Type, params, paramPos(fn.Params[i], fn.P))
			c.lanesContained(fn.Params[i].Type, paramPos(fn.Params[i], fn.P))
		}
		c.checkTypeKnown(fn.ReturnType, params, fn.P)
		c.lanesContained(fn.ReturnType, fn.P)
		if fn.Body != nil {
			ast.Walk(fn.Body, func(n ast.Node) bool {
				forEachDeclaredType(n, func(t *ast.Type, pos ast.Position) {
					if *t != nil {
						c.checkTypeKnown(*t, params, pos)
						c.lanesContained(*t, pos)
					}
				})
				return true
			})
		}
	}
	for _, sd := range prog.Structs {
		params := typeParamSet(sd.TypeParams)
		for i := range sd.Fields {
			c.checkTypeKnown(sd.Fields[i].Type, params, paramPos(sd.Fields[i], sd.P))
			c.lanesHeld(sd.Fields[i].Type, paramPos(sd.Fields[i], sd.P))
		}
	}
	for _, ed := range prog.Enums {
		params := typeParamSet(ed.TypeParams)
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				c.checkTypeKnown(ed.Variants[i].Payloads[j], params, ed.Variants[i].P)
				c.lanesHeld(ed.Variants[i].Payloads[j], ed.Variants[i].P)
			}
		}
	}
	for _, impl := range prog.Impls {
		params := typeParamSet(impl.TypeParams)
		c.checkTypeKnown(impl.Type, params, impl.P)
	}
}

func (c *checker) validateMapKeyTypes(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		mod := fn.BodyModule()
		if fn.Receiver != nil {
			c.checkMapKeyTypes(fn.Receiver.Type, mod, fn.P)
		}
		for i := range fn.Params {
			c.checkMapKeyTypes(fn.Params[i].Type, mod, paramPos(fn.Params[i], fn.P))
		}
		c.checkMapKeyTypes(fn.ReturnType, mod, fn.P)
		if fn.Body == nil {
			continue
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			if v, ok := n.(*ast.Var); ok {
				if lit, isLit := v.Init.(*ast.MapLit); isLit && len(lit.Entries) > 0 {
					return true
				}
			}
			forEachDeclaredType(n, func(t *ast.Type, pos ast.Position) {
				if *t != nil {
					c.checkMapKeyTypes(*t, mod, pos)
				}
			})
			return true
		})
	}
	for _, sd := range prog.Structs {
		for i := range sd.Fields {
			c.checkMapKeyTypes(sd.Fields[i].Type, sd.SourceModule, paramPos(sd.Fields[i], sd.P))
		}
	}
	for _, ed := range prog.Enums {
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				c.checkMapKeyTypes(ed.Variants[i].Payloads[j], ed.SourceModule, ed.Variants[i].P)
			}
		}
	}
	for _, impl := range prog.Impls {
		c.checkMapKeyTypes(impl.Type, impl.SourceModule, impl.P)
	}
}

func (c *checker) checkMapKeyTypes(t ast.Type, mod string, pos ast.Position) {
	switch x := t.(type) {
	case ast.StructType:
		if x.Name == "Map" && len(x.Args) == 2 {
			if msg := c.mapKeyTypeError(x.Args[0]); msg != "" {
				c.report(mod, pos, "E045", msg)
			}
			if _, void := x.Args[1].(ast.VoidType); void {
				c.report(mod, pos, "E045", MapVoidValue)
			}
		}
		for _, a := range x.Args {
			c.checkMapKeyTypes(a, mod, pos)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			c.checkMapKeyTypes(a, mod, pos)
		}
	case ast.ArrayType:
		c.checkMapKeyTypes(x.Elem, mod, pos)
	case ast.SliceType:
		c.checkMapKeyTypes(x.Elem, mod, pos)
	case ast.TupleType:
		for _, e := range x.Elems {
			c.checkMapKeyTypes(e, mod, pos)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			c.checkMapKeyTypes(p, mod, pos)
		}
		c.checkMapKeyTypes(x.Result, mod, pos)
	}
}

func (c *checker) checkTypeKnown(t ast.Type, params map[string]bool, pos ast.Position) {
	switch x := t.(type) {
	case ast.StructType:
		if !c.knownTypeName(x.Name, params) {
			c.errfCode(pos, "E064", "cannot find type %q in this scope%s", demangle(x.Name), c.unknownTypeHint(x.Name))
			return
		}
		for _, a := range x.Args {
			c.checkTypeKnown(a, params, pos)
		}
	case ast.EnumType:
		if !c.knownTypeName(x.Name, params) {
			c.errfCode(pos, "E064", "cannot find type %q in this scope%s", demangle(x.Name), c.unknownTypeHint(x.Name))
			return
		}
		for _, a := range x.Args {
			c.checkTypeKnown(a, params, pos)
		}
	case ast.ArrayType:
		c.checkTypeKnown(x.Elem, params, pos)
	case ast.SliceType:
		c.checkTypeKnown(x.Elem, params, pos)
	case ast.TupleType:
		for _, e := range x.Elems {
			c.checkTypeKnown(e, params, pos)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			c.checkTypeKnown(p, params, pos)
		}
		c.checkTypeKnown(x.Result, params, pos)
	}
}

func (c *checker) knownTypeName(name string, params map[string]bool) bool {
	if params[name] {
		return true
	}
	if _, ok := c.info.Structs[name]; ok {
		return true
	}
	if _, ok := c.info.Enums[name]; ok {
		return true
	}
	if _, ok := c.info.Traits[name]; ok {
		return true
	}
	if _, ok := c.info.Resources[name]; ok {
		return true
	}
	return false
}

func (c *checker) unknownTypeHint(name string) string {
	if c.traitNames[name] {
		return fmt.Sprintf(" (`%s` is a trait: a parameter of trait type makes the function generic over it; anywhere else write `dyn %s`)", demangle(name), demangle(name))
	}
	switch name {
	case "bool":
		return " (did you mean `boolean`?)"
	case "int", "long":
		return " (did you mean `i32`?)"
	case "uint":
		return " (did you mean `u32`?)"
	case "double":
		return " (did you mean `f64`?)"
	case "String":
		return " (did you mean `string`?)"
	}
	return ""
}

func typeParamSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func paramPos(p ast.Param, fallback ast.Position) ast.Position {
	if p.NamePos.Line > 0 {
		return p.NamePos
	}
	return fallback
}

type variantRef struct {
	enumName  string
	index     int
	payloads  []ast.Type
	srcModule string
}

func (c *checker) visibleVariants(name string) []variantRef {
	all := c.variantOf[name]
	from := c.currentModule()
	var out []variantRef
	for _, vr := range all {
		if c.moduleSeesVariant(from, vr.srcModule) {
			out = append(out, vr)
		}
	}
	return out
}

func (c *checker) resolveVariant(name, enumName string) (variantRef, bool, bool) {
	cands := c.visibleVariants(name)
	if enumName != "" {
		for _, vr := range cands {
			if vr.enumName == enumName {
				return vr, true, len(cands) > 1
			}
		}
		return variantRef{}, false, false
	}
	if len(cands) == 1 {
		return cands[0], true, false
	}
	return variantRef{}, false, len(cands) > 1
}

func (c *checker) monomorphCloneEnumName(dest ast.Type) string {
	et, ok := dest.(ast.EnumType)
	if !ok {
		return ""
	}
	if ed, ok := c.info.Enums[et.Name]; ok && ed.Monomorphized {
		return et.Name
	}
	return ""
}

func (c *checker) enumHintName(name string) string {
	ed, ok := c.info.Enums[name]
	if !ok || ed.Monomorphized || ed.SourceModule == "" || ed.SourceModule == c.entryModule {
		return name
	}
	if ed.SourceModule == c.currentModule() {
		if i := strings.Index(name, "__"); i >= 0 {
			return name[i+2:]
		}
		return name
	}
	return demangle(name)
}

func (c *checker) variantHintName(ed *ast.EnumDecl, name string) string {
	if ed == nil || ed.SourceModule == "" || ed.SourceModule == c.entryModule {
		return name
	}
	if i := strings.Index(ed.Name, "__"); i > 0 && strings.HasPrefix(name, ed.Name[:i+2]) {
		return name[i+2:]
	}
	return name
}

func (c *checker) variantEnumList(name string) string {
	cands := c.visibleVariants(name)
	if len(cands) == 0 {
		return ""
	}
	if len(cands) == 1 {
		return c.enumHintName(cands[0].enumName)
	}
	var b strings.Builder
	for i, vr := range cands {
		if i > 0 {
			if i == len(cands)-1 {
				b.WriteString(" and ")
			} else {
				b.WriteString(", ")
			}
		}
		b.WriteString(c.enumHintName(vr.enumName))
	}
	return b.String()
}

func (c *checker) variantQualifierHint(name, suffix string) string {
	cands := c.visibleVariants(name)
	var own, rest []string
	for _, vr := range cands {
		ed, ok := c.info.Enums[vr.enumName]
		spelled := c.enumHintName(vr.enumName) + "." + name
		switch {
		case ok && ed.SourceModule != "" && ed.SourceModule == c.currentModule():
			own = append(own, "`"+spelled+suffix+"`")
			continue
		case ok && ed.SourceModule != "" && !ed.Monomorphized:
			if i := strings.Index(vr.enumName, "__"); i > 0 {
				spelled = vr.enumName[:i] + "." + name
			}
		}
		rest = append(rest, "`"+spelled+suffix+"`")
	}
	return strings.Join(append(own, rest...), " or ")
}

var arithOpMethod = map[string]string{
	"+": "add", "-": "sub", "*": "mul", "/": "div", "%": "rem",
	"&": "bitand", "|": "bitor", "^": "bitxor", "<<": "shl", ">>": "shr",
}

var arithOpTrait = map[string]string{
	"add": "Add", "sub": "Sub", "mul": "Mul", "div": "Div", "neg": "Neg",
}

func (c *checker) paramComparable(lt, rt ast.Type, method string) bool {
	pt, ok := lt.(ast.ParamType)
	if !ok || !ast.Equal(lt, rt) {
		return false
	}
	_, _, found := c.resolveTraitMethodForParam(pt.Name, method)
	return found
}

func (c *checker) buildCheckedLowered(n *ast.Binary, t ast.NumberType, s *scope) ast.Expr {
	c.checkedN++
	p := n.P
	lN := fmt.Sprintf("__chk_l_%d", c.checkedN)
	rN := fmt.Sprintf("__chk_r_%d", c.checkedN)
	sN := fmt.Sprintf("__chk_s_%d", c.checkedN)
	id := func(name string) ast.Expr { return &ast.Ident{P: p, Name: name} }
	num := func(v int64) ast.Expr { return &ast.NumberLit{P: p, Value: v} }
	bin := func(op string, l, r ast.Expr) ast.Expr { return &ast.Binary{P: p, Op: op, Left: l, Right: r} }
	negOne := bin("-", num(0), num(1))
	baseOp := n.Op[:1]
	if len(n.Op) == 3 {
		baseOp = n.Op[:2]
	}
	unsigned := !t.IsSigned()

	var overflow ast.Expr
	switch {
	case n.Op == "<<?" || n.Op == ">>?":
		wLit := int64(t.NormalWidth())
		overflow = bin(">=", id(rN), num(wLit))
		if !unsigned {
			overflow = bin("||", bin("<", id(rN), num(0)), overflow)
		}
		if n.Op == "<<?" {
			overflow = bin("||", overflow, bin("!=", bin(">>", id(sN), id(rN)), id(lN)))
		}
	case baseOp == "/" || baseOp == "%":
		zero := bin("==", id(rN), num(0))
		if unsigned {
			overflow = zero
		} else {
			maxLit := int64(2147483647)
			if t.NormalWidth() == 64 {
				maxLit = 9223372036854775807
			}
			minExpr := bin("-", bin("-", num(0), num(maxLit)), num(1))
			overflow = bin("||", zero,
				bin("&&", bin("==", id(lN), minExpr), bin("==", id(rN), negOne)))
		}
	case unsigned && baseOp == "+":
		overflow = bin("<", id(sN), id(lN))
	case unsigned && baseOp == "-":
		overflow = bin("<", id(lN), id(rN))
	case unsigned:
		overflow = bin("&&", bin("!=", id(lN), num(0)),
			bin("!=", bin("/", id(sN), id(lN)), id(rN)))
	case baseOp == "+":
		overflow = bin("&&",
			bin(">=", bin("^", id(lN), id(rN)), num(0)),
			bin("<", bin("^", id(sN), id(lN)), num(0)))
	case baseOp == "-":
		overflow = bin("&&",
			bin("<", bin("^", id(lN), id(rN)), num(0)),
			bin("<", bin("^", id(sN), id(lN)), num(0)))
	default:
		maxLit := int64(2147483647)
		if t.NormalWidth() == 64 {
			maxLit = 9223372036854775807
		}
		minExpr := bin("-", bin("-", num(0), num(maxLit)), num(1))
		overflow = bin("&&", bin("!=", id(lN), num(0)),
			bin("||",
				bin("!=", bin("/", id(sN), id(lN)), id(rN)),
				bin("&&", bin("==", id(lN), negOne), bin("==", id(rN), minExpr))))
	}

	block := &ast.BlockExpr{P: p, Stmts: []ast.Stmt{
		&ast.Var{P: p, Name: lN, Type: t, Init: n.Left},
		&ast.Var{P: p, Name: rN, Type: t, Init: n.Right},
		&ast.Var{P: p, Name: sN, Type: t, Init: bin(baseOp, id(lN), id(rN))},
	}, Tail: &ast.IfExpr{P: p, Cond: overflow,
		Then: id("None"),
		Else: &ast.Call{P: p, Callee: id("Some"), Args: []ast.Expr{id(sN)}},
	}}
	if c.checkExpr(block, s) == nil {
		return nil
	}
	return block
}

func (c *checker) buildCheckedCast(n *ast.DowncastExpr, src, dst ast.NumberType, s *scope) ast.Expr {
	c.checkedN++
	p := n.P
	vN := fmt.Sprintf("__cvt_v_%d", c.checkedN)
	tN := fmt.Sprintf("__cvt_t_%d", c.checkedN)
	id := func(name string) ast.Expr { return &ast.Ident{P: p, Name: name} }
	bin := func(op string, l, r ast.Expr) ast.Expr { return &ast.Binary{P: p, Op: op, Left: l, Right: r} }
	zero := func() ast.Expr { return &ast.NumberLit{P: p, Value: 0} }
	fits := bin("==", &ast.CastExpr{P: p, Inner: id(tN), Target: src}, id(vN))
	if src.IsSigned() && !dst.IsSigned() {
		fits = bin("&&", bin(">=", id(vN), zero()), fits)
	}
	if !src.IsSigned() && dst.IsSigned() {
		fits = bin("&&", bin(">=", id(tN), zero()), fits)
	}
	block := &ast.BlockExpr{P: p, Stmts: []ast.Stmt{
		&ast.Var{P: p, Name: vN, Type: src, Init: n.Inner},
		&ast.Var{P: p, Name: tN, Type: dst, Init: &ast.CastExpr{P: p, Inner: id(vN), Target: dst}},
	}, Tail: &ast.IfExpr{P: p, Cond: fits,
		Then: &ast.Call{P: p, Callee: id("Some"), Args: []ast.Expr{id(tN)}},
		Else: id("None"),
	}}
	if c.checkExpr(block, s) == nil {
		return nil
	}
	return block
}

func (c *checker) tryConvertErrToDyn(n *ast.TryOp, srcEnum, retEnum ast.EnumType, s *scope) (ast.Expr, bool) {
	dt, ok := retEnum.Args[1].(ast.DynTraitType)
	if !ok {
		return nil, false
	}
	if !c.canBoxInto(dt, srcEnum.Args[1]) {
		return nil, false
	}
	c.tryConvN++
	tmp := fmt.Sprintf("__try_dyn_%d", c.tryConvN)
	okBind := fmt.Sprintf("__try_ok_%d", c.tryConvN)
	errBind := fmt.Sprintf("__try_err_%d", c.tryConvN)
	p := n.P
	resultDyn := ast.EnumType{Name: "Result", Args: []ast.Type{srcEnum.Args[0], dt}}
	mapMatch := &ast.MatchExpr{P: p, Tag: n.Inner, Arms: []*ast.MatchExprArm{
		{P: p, VariantName: "Ok", Bindings: []string{okBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Ok"}, Args: []ast.Expr{&ast.Ident{P: p, Name: okBind}}}},
		{P: p, VariantName: "Err", Bindings: []string{errBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Err"}, Args: []ast.Expr{
				&ast.CastExpr{P: p, Inner: &ast.Ident{P: p, Name: errBind}, Target: dt}}}},
	}}
	block := &ast.BlockExpr{P: p, Stmts: []ast.Stmt{
		&ast.Var{P: p, Name: tmp, Type: resultDyn, Init: mapMatch},
	}, Tail: &ast.TryOp{P: p, Inner: &ast.Ident{P: p, Name: tmp}}}
	if t := c.checkExpr(block, s); t == nil {
		return nil, false
	}
	return block, true
}

func (c *checker) tryResidualMsg(srcEnum ast.EnumType, srcRes, retRes ast.Type) string {
	sl, rl := typeLabel(srcRes), typeLabel(retRes)
	if dt, ok := retRes.(ast.DynTraitType); ok {
		return fmt.Sprintf("`?` cannot box %s into %s: %s does not implement %s", sl, rl, sl, c.missingDynTraits(dt, srcRes))
	}
	return fmt.Sprintf("`?` on %s carrying %s but the surrounding function carries %s, and %s has no `from(%s)` constructor to convert it (std/convert's `impl From[%s] for %s`)",
		demangle(srcEnum.Name), sl, rl, rl, sl, sl, rl)
}

func (c *checker) tryConvertErrViaFrom(n *ast.TryOp, srcEnum, retEnum ast.EnumType, s *scope) (ast.Expr, bool) {
	srcErr := srcEnum.Args[1]
	fnErr := retEnum.Args[1]
	tn, ok := methodTypeName(fnErr)
	if !ok {
		return nil, false
	}
	switch fnErr.(type) {
	case ast.StructType, ast.EnumType:
	default:
		return nil, false
	}
	sig, ok := c.info.FuncSigs["__assoc_"+tn+"_from"]
	if !ok || len(sig.Params) != 1 || !ast.Equal(sig.Params[0], srcErr) || !ast.Equal(sig.Result, fnErr) {
		return nil, false
	}
	c.tryConvN++
	tmp := fmt.Sprintf("__try_from_%d", c.tryConvN)
	okBind := fmt.Sprintf("__try_ok_%d", c.tryConvN)
	errBind := fmt.Sprintf("__try_err_%d", c.tryConvN)
	p := n.P
	resultConv := ast.EnumType{Name: "Result", Args: []ast.Type{srcEnum.Args[0], fnErr}}
	fromCall := &ast.Call{P: p,
		Callee: &ast.FieldAccess{P: p, Target: &ast.Ident{P: p, Name: tn}, Field: "from"},
		Args:   []ast.Expr{&ast.Ident{P: p, Name: errBind}}}
	mapMatch := &ast.MatchExpr{P: p, Tag: n.Inner, Arms: []*ast.MatchExprArm{
		{P: p, VariantName: "Ok", Bindings: []string{okBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Ok"}, Args: []ast.Expr{&ast.Ident{P: p, Name: okBind}}}},
		{P: p, VariantName: "Err", Bindings: []string{errBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Err"}, Args: []ast.Expr{fromCall}}},
	}}
	block := &ast.BlockExpr{P: p, Stmts: []ast.Stmt{
		&ast.Var{P: p, Name: tmp, Type: resultConv, Init: mapMatch},
	}, Tail: &ast.TryOp{P: p, Inner: &ast.Ident{P: p, Name: tmp}}}
	if t := c.checkExpr(block, s); t == nil {
		return nil, false
	}
	return block, true
}

func (c *checker) compositeOpOverload(n *ast.Binary, lt, rt ast.Type, s *scope) (ast.Type, bool) {
	if lt == nil || !ast.Equal(lt, rt) {
		return nil, false
	}
	opMethod := arithOpMethod[n.Op]
	if pt, ok := lt.(ast.ParamType); ok {
		if opMethod == "" {
			return nil, false
		}
		if _, _, found := c.resolveTraitMethodForParam(pt.Name, opMethod); !found {
			return nil, false
		}
		call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: opMethod}, Args: []ast.Expr{n.Right}}
		rtt := c.checkExpr(call, s)
		n.ArithCall = call
		return rtt, true
	}
	switch lt.(type) {
	case ast.StructType, ast.EnumType:
	default:
		return nil, false
	}
	tn, _ := methodTypeName(lt)
	if mangled, _, ok := c.resolveMethod(tn, opMethod, []string{arithOpTrait[opMethod]}); ok && c.methodVisibleHere(mangled) {
		call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: opMethod}, Args: []ast.Expr{n.Right}}
		rtt := c.checkExpr(call, s)
		n.ArithCall = call
		return rtt, true
	}
	c.errfCode(n.P, "E009", "operator %q is not defined for %s — implement `function (self: %s) %s(other: %s): %s` to overload it", n.Op, lt, tn, opMethod, tn, tn)
	return lt, true
}

func (c *checker) resolveProj(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ProjType:
		base := c.resolveProj(x.Base)
		if tn, ok := methodTypeName(base); ok {
			if m, ok := c.info.AssocBindings[tn]; ok {
				if bound, ok := m[x.Name]; ok {
					return c.resolveProj(c.substAssocBinding(tn, x.Name, bound, base))
				}
			}
		}
		return ast.ProjType{Base: base, Name: x.Name}
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.resolveProj(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.resolveProj(x.Elem)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = c.resolveProj(x.Elems[i])
		}
		return out
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProj(x.Args[i])
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProj(x.Args[i])
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case *ast.FuncType:
		out := &ast.FuncType{Result: c.resolveProj(x.Result), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, c.resolveProj(p))
		}
		return out
	}
	return t
}

func (c *checker) substAssocBinding(typeName, assoc string, bound, base ast.Type) ast.Type {
	pat, ok := c.info.AssocBindingPattern[typeName][assoc]
	if !ok {
		return bound
	}
	psub := map[string]ast.Type{}
	if !c.unifyType(pat, base, psub) || len(psub) == 0 {
		return bound
	}
	return substByName(bound, psub)
}

func (c *checker) resolveProjWithSub(t ast.Type, sub map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ProjType:
		base := c.resolveProjWithSub(x.Base, sub)
		if p, ok := base.(ast.ParamType); ok {
			if bound, isBound := sub[p.Name]; isBound {
				base = bound
			}
		}
		return c.resolveProj(ast.ProjType{Base: base, Name: x.Name})
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.resolveProjWithSub(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.resolveProjWithSub(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = c.resolveProjWithSub(x.Elems[i], sub)
		}
		return out
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProjWithSub(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProjWithSub(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case *ast.FuncType:
		out := &ast.FuncType{Result: c.resolveProjWithSub(x.Result, sub), ParamOwn: x.ParamOwn}
		for _, pp := range x.Params {
			out.Params = append(out.Params, c.resolveProjWithSub(pp, sub))
		}
		return out
	}
	return t
}

func (c *checker) resolveProjWith(t ast.Type, bindings map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ProjType:
		if b, ok := bindings[x.Name]; ok {
			rest := make(map[string]ast.Type, len(bindings))
			for k, v := range bindings {
				if k != x.Name {
					rest[k] = v
				}
			}
			return c.resolveProjWith(b, rest)
		}
		return ast.ProjType{Base: c.resolveProjWith(x.Base, bindings), Name: x.Name}
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.resolveProjWith(x.Elem, bindings)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.resolveProjWith(x.Elem, bindings)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = c.resolveProjWith(x.Elems[i], bindings)
		}
		return out
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProjWith(x.Args[i], bindings)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProjWith(x.Args[i], bindings)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case *ast.FuncType:
		out := &ast.FuncType{Result: c.resolveProjWith(x.Result, bindings), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, c.resolveProjWith(p, bindings))
		}
		return out
	}
	return t
}

func (c *checker) resolveProjections(prog *ast.Program) {
	if len(c.info.AssocBindings) == 0 {
		return
	}
	for name, sig := range c.info.FuncSigs {
		c.info.FuncSigs[name] = c.resolveProj(sig).(*ast.FuncType)
	}
	for _, fn := range prog.Funcs {
		fn.ReturnType = c.resolveProj(fn.ReturnType)
		for i := range fn.Params {
			fn.Params[i].Type = c.resolveProj(fn.Params[i].Type)
		}
		if fn.Receiver != nil {
			fn.Receiver.Type = c.resolveProj(fn.Receiver.Type)
		}
		if fn.Body != nil {
			c.resolveProjInBlock(fn.Body)
		}
	}
}

func (c *checker) resolveProjInBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		c.resolveProjInStmt(st)
	}
}

func (c *checker) resolveProjInStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Var:
		if x.Type != nil {
			x.Type = c.resolveProj(x.Type)
		}
		c.resolveProjInExpr(x.Init)
	case *ast.ExprStmt:
		c.resolveProjInExpr(x.Expr)
	case *ast.Return:
		c.resolveProjInExpr(x.Value)
	case *ast.Block:
		c.resolveProjInBlock(x)
	case *ast.If:
		c.resolveProjInExpr(x.Cond)
		c.resolveProjInStmt(x.Then)
		if x.Else != nil {
			c.resolveProjInStmt(x.Else)
		}
	case *ast.While:
		c.resolveProjInExpr(x.Cond)
		c.resolveProjInStmt(x.Body)
	case *ast.Loop:
		c.resolveProjInStmt(x.Body)
	case *ast.For:
		if x.Init != nil {
			c.resolveProjInStmt(x.Init)
		}
		c.resolveProjInExpr(x.Cond)
		if x.Step != nil {
			c.resolveProjInStmt(x.Step)
		}
		c.resolveProjInStmt(x.Body)
	case *ast.Match:
		c.resolveProjInExpr(x.Tag)
		for _, arm := range x.Arms {
			for i := range arm.BindingTypes {
				if arm.BindingTypes[i] != nil {
					arm.BindingTypes[i] = c.resolveProj(arm.BindingTypes[i])
				}
			}
			c.resolveProjInBlock(arm.Body)
		}
	}
}

func (c *checker) resolveProjInExpr(e ast.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ast.CastExpr:
		x.Target = c.resolveProj(x.Target)
		c.resolveProjInExpr(x.Inner)
	case *ast.Assign:
		c.resolveProjInExpr(x.Target)
		c.resolveProjInExpr(x.Value)
	case *ast.Lambda:
		for i := range x.Params {
			x.Params[i].Type = c.resolveProj(x.Params[i].Type)
		}
		x.ReturnType = c.resolveProj(x.ReturnType)
		c.resolveProjInBlock(x.Body)
	case *ast.Binary:
		c.resolveProjInExpr(x.Left)
		c.resolveProjInExpr(x.Right)
	case *ast.Unary:
		c.resolveProjInExpr(x.Operand)
	case *ast.Call:
		c.resolveProjInExpr(x.Callee)
		for _, a := range x.Args {
			c.resolveProjInExpr(a)
		}
	case *ast.Index:
		c.resolveProjInExpr(x.Array)
		c.resolveProjInExpr(x.Idx)
	case *ast.FieldAccess:
		c.resolveProjInExpr(x.Target)
	}
}

func substByName(t ast.Type, sub map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substByName(x.Args[i], sub)
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
			args[i] = substByName(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.ArrayType:
		return ast.ArrayType{Elem: substByName(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substByName(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substByName(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substByName(x.Result, sub), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, substByName(p, sub))
		}
		return out
	case ast.ProjType:
		return ast.ProjType{Base: substByName(x.Base, sub), Name: x.Name}
	}
	return t
}

func BoundErrors(info *Info, fn *ast.FuncDecl, args []ast.Type, pos ast.Position) []error {
	if info == nil || fn == nil || len(fn.Bounds) == 0 {
		return nil
	}
	sub := make(map[string]ast.Type, len(fn.TypeParams))
	tpSet := make(map[string]bool, len(fn.TypeParams))
	for i, name := range fn.TypeParams {
		tpSet[name] = true
		if i < len(args) {
			sub[name] = args[i]
		}
	}
	c := &checker{info: info}
	c.checkTypeArgBounds(fn, args, sub, tpSet, pos)
	return c.errors
}

func (c *checker) checkTypeArgBounds(genericFn *ast.FuncDecl, args []ast.Type, sub map[string]ast.Type, tpSet map[string]bool, pos ast.Position) {
	for i, tp := range genericFn.TypeParams {
		if _, isParam := args[i].(ast.ParamType); isParam {
			continue
		}
		for bi, traitName := range genericFn.Bounds[tp] {
			tn, ok := methodTypeName(args[i])
			site := demangle(genericFn.Name)
			if genericFn.MethodRecv != "" {
				site = demangle(genericFn.MethodRecv) + "." + genericFn.MethodSimpleName
			}
			dynAt := -1
			if d, isDyn := args[i].(ast.DynTraitType); isDyn {
				for k, name := range d.Traits {
					if name == traitName {
						dynAt = k
					}
				}
			}
			if dynAt < 0 && (!ok || !c.info.Impls[traitName][tn]) {
				c.errfCode(pos, "E021",
					"type argument %s = %s does not implement trait %s required by %s",
					tp, demangle(args[i].String()), demangle(traitName), site)
				continue
			}
			var boundArgs []ast.Type
			if ba := genericFn.BoundArgs[tp]; bi < len(ba) {
				boundArgs = ba[bi]
			}
			if len(boundArgs) > 0 {
				resolved := make([]ast.Type, len(boundArgs))
				for k, baT := range boundArgs {
					resolved[k] = substBoundArg(baT, tpSet, sub)
				}
				boundArgs = resolved
				var implArgs []ast.Type
				if d, isDyn := args[i].(ast.DynTraitType); isDyn && dynAt >= 0 {
					implArgs = d.ArgsFor(dynAt)
				} else {
					implArgs = c.implTraitArgsFor(traitName, tn, args[i])
				}
				if !typeArgsEqual(implArgs, boundArgs) {
					c.errfCode(pos, "E021",
						"type argument %s = %s implements %s%s but the bound requires %s%s (in %s)",
						tp, demangle(args[i].String()), demangle(traitName), traitArgsStr(implArgs),
						demangle(traitName), traitArgsStr(boundArgs), site)
				}
			}
		}
	}
}

func (c *checker) implTraitArgsFor(traitName, tn string, ct ast.Type) []ast.Type {
	implArgs := c.info.ImplTraitArgs[traitName][tn]
	if len(implArgs) == 0 {
		return implArgs
	}
	pat, ok := c.info.ImplForPattern[traitName][tn]
	if !ok {
		return implArgs
	}
	psub := map[string]ast.Type{}
	if !c.unifyType(pat, ct, psub) || len(psub) == 0 {
		return implArgs
	}
	out := make([]ast.Type, len(implArgs))
	for k := range implArgs {
		out[k] = substByName(implArgs[k], psub)
	}
	return out
}

func typeArgsEqual(a, b []ast.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !ast.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func normalizeParamRefs(t ast.Type, tpSet map[string]bool) ast.Type {
	switch b := t.(type) {
	case ast.StructType:
		if len(b.Args) == 0 {
			if tpSet[b.Name] {
				return ast.ParamType{Name: b.Name}
			}
			return t
		}
		na := make([]ast.Type, len(b.Args))
		for i := range b.Args {
			na[i] = normalizeParamRefs(b.Args[i], tpSet)
		}
		b.Args = na
		return b
	case ast.EnumType:
		na := make([]ast.Type, len(b.Args))
		for i := range b.Args {
			na[i] = normalizeParamRefs(b.Args[i], tpSet)
		}
		b.Args = na
		return b
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(b.Elems))}
		for i := range b.Elems {
			out.Elems[i] = normalizeParamRefs(b.Elems[i], tpSet)
		}
		return out
	case ast.ArrayType:
		return ast.ArrayType{Elem: normalizeParamRefs(b.Elem, tpSet)}
	case ast.SliceType:
		return ast.SliceType{Elem: normalizeParamRefs(b.Elem, tpSet)}
	case *ast.FuncType:
		out := &ast.FuncType{Result: normalizeParamRefs(b.Result, tpSet), ParamOwn: b.ParamOwn}
		for _, p := range b.Params {
			out.Params = append(out.Params, normalizeParamRefs(p, tpSet))
		}
		return out
	}
	return t
}

func bindBoundParam(boundArg, implArg ast.Type, tpSet map[string]bool, sub map[string]ast.Type) bool {
	bind := func(name string) bool {
		if !tpSet[name] {
			return false
		}
		existing, done := sub[name]
		if !done {
			sub[name] = implArg
			return true
		}
		if en, ok := existing.(ast.NumberType); ok && en.Polymorphic {
			if an, ok := implArg.(ast.NumberType); ok && !an.Polymorphic {
				sub[name] = implArg
				return true
			}
		}
		return false
	}
	switch b := boundArg.(type) {
	case ast.ParamType:
		return bind(b.Name)
	case ast.StructType:
		if len(b.Args) == 0 {
			return bind(b.Name)
		}
		if it, ok := implArg.(ast.StructType); ok && len(it.Args) == len(b.Args) {
			changed := false
			for i := range b.Args {
				if bindBoundParam(b.Args[i], it.Args[i], tpSet, sub) {
					changed = true
				}
			}
			return changed
		}
	}
	return false
}

func substBoundArg(t ast.Type, tpSet map[string]bool, sub map[string]ast.Type) ast.Type {
	switch b := t.(type) {
	case ast.ParamType:
		if tpSet[b.Name] {
			if v, ok := sub[b.Name]; ok {
				return v
			}
		}
	case ast.StructType:
		if len(b.Args) == 0 {
			if tpSet[b.Name] {
				if v, ok := sub[b.Name]; ok {
					return v
				}
			}
			return t
		}
		na := make([]ast.Type, len(b.Args))
		for i := range b.Args {
			na[i] = substBoundArg(b.Args[i], tpSet, sub)
		}
		b.Args = na
		return b
	}
	return t
}

func traitArgsStr(args []ast.Type) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = demangle(a.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
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
		out := &ast.FuncType{Result: substituteType(x.Result, sub), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, substituteType(p, sub))
		}
		return out
	case ast.ProjType:
		return ast.ProjType{Base: substituteType(x.Base, sub), Name: x.Name}
	}
	return t
}

func namedTypeArity(t ast.Type) (string, int, bool) {
	switch v := t.(type) {
	case ast.EnumType:
		return v.Name, len(v.Args), true
	case ast.StructType:
		return v.Name, len(v.Args), true
	}
	return "", 0, false
}

func (c *checker) unifyType(expected, actual ast.Type, sub map[string]ast.Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	if p, ok := expected.(ast.ParamType); ok {
		if existing, bound := sub[p.Name]; bound {
			if ast.Equal(existing, actual) {
				return true
			}
			if e, ok := existing.(ast.EnumType); ok {
				if a, ok := actual.(ast.EnumType); ok {
					if merged, ok := mergeEnumInference(e, a); ok {
						sub[p.Name] = merged
						return true
					}
				}
			}
			if en, ok := existing.(ast.NumberType); ok && en.Polymorphic && !c.seededParams[p.Name] {
				if an, ok := actual.(ast.NumberType); ok {
					if !an.Polymorphic {
						sub[p.Name] = actual
					}
					return true
				}
			}
			en, ea, eok := namedTypeArity(existing)
			an, aa, aok := namedTypeArity(actual)
			if eok && aok && en == an {
				if aa == 0 {
					return true
				}
				if ea == 0 {
					sub[p.Name] = actual
					return true
				}
			}
			return false
		}
		sub[p.Name] = actual
		return true
	}
	if e, ok := expected.(ast.EnumType); ok {
		a, ok := actual.(ast.EnumType)
		if !ok || a.Name != e.Name {
			return false
		}
		if len(a.Args) == 0 && len(e.Args) > 0 {
			return true
		}
		if len(a.Args) != len(e.Args) {
			return false
		}
		for i := range e.Args {
			if unboundEnumArg(a.Args[i]) {
				continue
			}
			if !c.unifyType(e.Args[i], a.Args[i], sub) {
				return false
			}
		}
		return true
	}
	if e, ok := expected.(ast.StructType); ok {
		a, ok := actual.(ast.StructType)
		if !ok || a.Name != e.Name || len(a.Args) != len(e.Args) {
			return false
		}
		for i := range e.Args {
			if !c.unifyType(e.Args[i], a.Args[i], sub) {
				return false
			}
		}
		return true
	}
	if e, ok := expected.(ast.ArrayType); ok {
		a, ok := actual.(ast.ArrayType)
		return ok && c.unifyType(e.Elem, a.Elem, sub)
	}
	if e, ok := expected.(ast.SliceType); ok {
		a, ok := actual.(ast.SliceType)
		return ok && c.unifyType(e.Elem, a.Elem, sub)
	}
	if e, ok := expected.(ast.TupleType); ok {
		a, ok := actual.(ast.TupleType)
		if !ok || len(e.Elems) != len(a.Elems) {
			return false
		}
		for i := range e.Elems {
			if !c.unifyType(e.Elems[i], a.Elems[i], sub) {
				return false
			}
		}
		return true
	}
	if e, ok := expected.(*ast.FuncType); ok {
		a, ok := actual.(*ast.FuncType)
		if !ok || len(e.Params) != len(a.Params) {
			return false
		}
		for i := range e.Params {
			if e.OwnAt(i) != a.OwnAt(i) && !definitelyScalar(a.Params[i]) {
				return false
			}
			if !c.unifyType(e.Params[i], a.Params[i], sub) {
				return false
			}
		}
		return c.unifyType(e.Result, a.Result, sub)
	}
	return ast.Equal(expected, actual)
}

func unifyIfArms(a, b ast.Type) ast.Type {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if _, ok := a.(ast.NeverType); ok {
		return b
	}
	if _, ok := b.(ast.NeverType); ok {
		return a
	}
	if an, aok := a.(ast.NumberType); aok && an.Polymorphic {
		switch b.(type) {
		case ast.NumberType, ast.FloatType:
			return b
		}
	}
	if bn, bok := b.(ast.NumberType); bok && bn.Polymorphic {
		switch a.(type) {
		case ast.NumberType, ast.FloatType:
			return a
		}
	}
	if af, aok := a.(ast.FloatType); aok && af.Polymorphic {
		if _, ok := b.(ast.FloatType); ok {
			return b
		}
	}
	if bf, bok := b.(ast.FloatType); bok && bf.Polymorphic {
		if _, ok := a.(ast.FloatType); ok {
			return a
		}
	}
	ae, aok := a.(ast.EnumType)
	be, bok := b.(ast.EnumType)
	if aok && bok && ae.Name == be.Name {
		if merged, ok := mergeEnumInference(ae, be); ok {
			return merged
		}
	}
	if as, aok := a.(ast.StructType); aok {
		if bs, bok := b.(ast.StructType); bok && as.Name == bs.Name && len(as.Args) == len(bs.Args) && len(as.Args) > 0 {
			args := make([]ast.Type, len(as.Args))
			for i := range as.Args {
				if ast.Equal(as.Args[i], bs.Args[i]) {
					args[i] = as.Args[i]
					continue
				}
				u := unifyIfArms(as.Args[i], bs.Args[i])
				if u == nil {
					return nil
				}
				args[i] = u
			}
			return ast.StructType{Name: as.Name, Args: args}
		}
	}
	if at, aok := a.(ast.TupleType); aok {
		if bt, bok := b.(ast.TupleType); bok && len(at.Elems) == len(bt.Elems) {
			out := make([]ast.Type, len(at.Elems))
			for i := range at.Elems {
				u := unifyIfArms(at.Elems[i], bt.Elems[i])
				if u == nil {
					return nil
				}
				out[i] = u
			}
			return ast.TupleType{Elems: out}
		}
	}
	if aa, aok := a.(ast.ArrayType); aok && aa.Elem != nil {
		if ba, bok := b.(ast.ArrayType); bok && ba.Elem != nil {
			if u := unifyIfArms(aa.Elem, ba.Elem); u != nil {
				return ast.ArrayType{Elem: u}
			}
			return nil
		}
	}
	if as_, aok := a.(ast.SliceType); aok && as_.Elem != nil {
		if bs, bok := b.(ast.SliceType); bok && bs.Elem != nil {
			if u := unifyIfArms(as_.Elem, bs.Elem); u != nil {
				return ast.SliceType{Elem: u}
			}
			return nil
		}
	}
	if ast.Equal(a, b) {
		return a
	}
	if aa, aok := a.(ast.ArrayType); aok {
		if ba, bok := b.(ast.ArrayType); bok {
			if aa.Elem == nil {
				return ba
			}
			if ba.Elem == nil {
				return aa
			}
		}
	}
	if as_, aok := a.(ast.SliceType); aok {
		if bs, bok := b.(ast.SliceType); bok {
			if as_.Elem == nil {
				return bs
			}
			if bs.Elem == nil {
				return as_
			}
		}
	}
	return nil
}

func (c *checker) maybeWrapForUnion(dst ast.Type, holder *ast.Expr, srcType ast.Type, s *scope) ast.Type {
	if holder == nil || *holder == nil {
		return srcType
	}
	if dt, ok := dst.(ast.DynTraitType); ok {
		if _, isDyn := srcType.(ast.DynTraitType); !isDyn {
			if tn, ok := methodTypeName(srcType); ok && c.canBoxInto(dt, srcType) {
				if c.info.DynCoercions == nil {
					c.info.DynCoercions = map[ast.Expr]DynCoercion{}
				}
				c.info.DynCoercions[*holder] = DynCoercion{
					Trait:    dt.Trait0(),
					Traits:   dt.Traits,
					Concrete: tn,
				}
			}
		}
		return srcType
	}
	if dtup, dok := dst.(ast.TupleType); dok {
		tl, isLit := (*holder).(*ast.TupleLit)
		stup, isTup := srcType.(ast.TupleType)
		if isLit && isTup && len(tl.Elems) == len(dtup.Elems) && len(stup.Elems) == len(dtup.Elems) {
			out := make([]ast.Type, len(stup.Elems))
			copy(out, stup.Elems)
			for i := range tl.Elems {
				out[i] = c.maybeWrapForUnion(dtup.Elems[i], &tl.Elems[i], stup.Elems[i], s)
			}
			return ast.TupleType{Elems: out}
		}
		return srcType
	}
	du, dok := dst.(ast.EnumType)
	if !dok {
		return srcType
	}
	if call, ok := (*holder).(*ast.Call); ok {
		if id, idOk := call.Callee.(*ast.Ident); idOk {
			if dts, found := c.variantDynPayloadTypes(du, id.Name); found && len(dts) == len(call.Args) {
				var srcPayloads []ast.Type
				if se, seOk := srcType.(ast.EnumType); seOk {
					srcPayloads, _ = c.variantDynPayloadTypes(se, id.Name)
				}
				changed := false
				for i := range call.Args {
					dt, isDyn := dts[i].(ast.DynTraitType)
					if !isDyn {
						continue
					}
					if i < len(srcPayloads) {
						if _, srcDyn := srcPayloads[i].(ast.DynTraitType); srcDyn {
							continue
						}
					}
					if _, already := call.Args[i].(*ast.CastExpr); already {
						continue
					}
					call.Args[i] = &ast.CastExpr{P: call.Args[i].Pos(), Inner: call.Args[i], Target: dt}
					changed = true
				}
				if changed {
					return c.checkExpr(*holder, s)
				}
			}
		}
	}
	if call, ok := (*holder).(*ast.Call); ok {
		if id, idOk := call.Callee.(*ast.Ident); idOk {
			slots, found := c.variantDynPayloadTypes(du, id.Name)
			se, seOk := srcType.(ast.EnumType)
			if found && seOk && len(slots) == len(call.Args) {
				srcPayloads, _ := c.variantDynPayloadTypes(se, id.Name)
				changed := false
				for i := range call.Args {
					if _, toUnion := slots[i].(ast.EnumType); !toUnion || i >= len(srcPayloads) {
						continue
					}
					if _, fromStruct := srcPayloads[i].(ast.StructType); !fromStruct {
						continue
					}
					arg := call.Args[i]
					c.maybeWrapForUnion(slots[i], &call.Args[i], srcPayloads[i], s)
					if call.Args[i] != arg {
						changed = true
					}
				}
				if changed {
					return c.checkExpr(*holder, s)
				}
			}
		}
	}
	ss, sok := srcType.(ast.StructType)
	if !sok {
		return srcType
	}
	ed, edOk := c.info.Enums[du.Name]
	if !edOk {
		return srcType
	}
	matched := false
	for _, v := range ed.Variants {
		if v.Name != ss.Name || len(v.Payloads) != 1 {
			continue
		}
		ps, ok := v.Payloads[0].(ast.StructType)
		if !ok || ps.Name != ss.Name {
			continue
		}
		matched = true
		break
	}
	if !matched {
		return srcType
	}
	src := *holder
	wrapped := &ast.Call{
		P:      src.Pos(),
		Callee: &ast.Ident{P: src.Pos(), Name: ss.Name},
		Args:   []ast.Expr{src},
	}
	*holder = wrapped
	return c.checkExpr(wrapped, s)
}

func (c *checker) variantDynPayloadTypes(du ast.EnumType, variantName string) ([]ast.Type, bool) {
	ed, ok := c.info.Enums[du.Name]
	if !ok {
		return nil, false
	}
	for _, v := range ed.Variants {
		if v.Name != variantName {
			continue
		}
		out := make([]ast.Type, len(v.Payloads))
		for i, p := range v.Payloads {
			out[i] = p
			if pt, isParam := p.(ast.ParamType); isParam {
				for idx, tp := range ed.TypeParams {
					if tp == pt.Name && idx < len(du.Args) {
						out[i] = du.Args[idx]
						break
					}
				}
			}
		}
		return out, true
	}
	return nil, false
}

func (c *checker) inStdlibContext() bool {
	return c.current != nil && strings.HasPrefix(c.current.BodyModule(), "stdlib://")
}

func (c *checker) implementsAllDynTraits(dt ast.DynTraitType, tn string) bool {
	for i, tr := range dt.Traits {
		if !c.info.Impls[tr][tn] {
			return false
		}
		if want := dt.ArgsFor(i); len(want) > 0 {
			if !typeArgsEqual(c.info.ImplTraitArgs[tr][tn], want) {
				return false
			}
		}
		for _, b := range dt.AssocFor(i) {
			got, ok := c.info.AssocBindings[tn][b.Name]
			if !ok || !ast.Equal(got, b.Type) {
				return false
			}
		}
	}
	return true
}

func (c *checker) canBoxInto(dt ast.DynTraitType, src ast.Type) bool {
	param, isParam := src.(ast.ParamType)
	if !isParam {
		tn, ok := methodTypeName(src)
		return ok && c.implementsAllDynTraits(dt, tn)
	}
	decl := c.typeParamDecl(param.Name)
	if decl == nil {
		return false
	}
	direct := decl.Bounds[param.Name]
	traits := c.expandTraits(direct)
	for i, trait := range dt.Traits {
		if !slices.Contains(traits, trait) {
			return false
		}
		if len(dt.AssocFor(i)) > 0 {
			return false
		}
		if want := dt.ArgsFor(i); len(want) > 0 {
			matched := false
			for j, bound := range direct {
				if args := decl.BoundArgs[param.Name]; bound == trait && j < len(args) && typeArgsEqual(args[j], want) {
					matched = true
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}

func (c *checker) missingDynTraits(dt ast.DynTraitType, src ast.Type) string {
	if _, ok := methodTypeName(src); !ok {
		if _, param := src.(ast.ParamType); !param {
			return demangle(dt.Trait0())
		}
	}
	var missing []string
	for i, tr := range dt.Traits {
		one := ast.NewDynTraitTypeFull([]string{tr}, [][]ast.Type{dt.ArgsFor(i)}, [][]ast.AssocBinding{dt.AssocFor(i)})
		if !c.canBoxInto(one, src) {
			missing = append(missing, demangle(tr))
		}
	}
	if len(missing) == 0 {
		return demangle(dt.Trait0())
	}
	return strings.Join(missing, " + ")
}

func (c *checker) argAssignable(want, got ast.Type, own bool) bool {
	if c.assignable(want, got) {
		return true
	}
	if own {
		return false
	}
	if isArrayViewLend(want, got) {
		return true
	}
	_, gotStr := got.(ast.StrType)
	_, wantString := want.(ast.StringType)
	return gotStr && wantString
}

func storesArgument(name string, i int) bool {
	switch name {
	case "__method_Array_push":
		return i == 1
	case "__method_Array_set":
		return i == 2
	case "__method_Map_set":
		return i == 1 || i == 2
	case "__method_Cell_set":
		return i == 1
	}
	return false
}

func (c *checker) argOK(arg *ast.Expr, want, got ast.Type, own bool) bool {
	if !c.argAssignable(want, got, own) {
		return false
	}
	c.lendArrayAsView(arg, want, got, own)
	return true
}

func isArrayViewLend(want, got ast.Type) bool {
	sl, isView := want.(ast.SliceType)
	arr, isArray := got.(ast.ArrayType)
	return isView && isArray && arr.Elem != nil && ast.Equal(sl.Elem, arr.Elem)
}

func (c *checker) lendArrayAsView(arg *ast.Expr, want, got ast.Type, own bool) {
	if own || arg == nil || *arg == nil || !isArrayViewLend(want, got) {
		return
	}
	*arg = &ast.SliceExpr{P: (*arg).Pos(), Source: *arg, ElemType: got.(ast.ArrayType).Elem, Lent: true}
}

func (c *checker) unifyArrayArg(arg *ast.Expr, want, got ast.Type, sub map[string]ast.Type, own bool) bool {
	if c.unifyType(want, got, sub) {
		return true
	}
	if own {
		return false
	}
	sl, isView := want.(ast.SliceType)
	arr, isArray := got.(ast.ArrayType)
	if !isView || !isArray || arr.Elem == nil || !c.unifyType(sl.Elem, arr.Elem, sub) {
		return false
	}
	*arg = &ast.SliceExpr{P: (*arg).Pos(), Source: *arg, ElemType: arr.Elem, Lent: true}
	return true
}

func assignHint(want, got ast.Type) string {
	if h := dynElemHint(want, got); h != "" {
		return h
	}
	if _, gotStr := got.(ast.StrType); !gotStr {
		return ""
	}
	if _, wantString := want.(ast.StringType); !wantString {
		return ""
	}
	return " — `str` is a borrowed view of a string; add `.to_owned()` to copy it into an owned string"
}

func dynElemHint(want, got ast.Type) string {
	if _, direct := want.(ast.DynTraitType); direct {
		return ""
	}
	pos, ok := dynElemMismatch(want, got)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" — %s is a trait object, and boxing one is a representation change that only happens at a direct coercion site, "+
		"never inside a container; rebuild it element by element (match / map the %s and coerce each payload)", pos, containerNoun(want))
}

func dynElemMismatch(want, got ast.Type) (string, bool) {
	switch w := want.(type) {
	case ast.DynTraitType:
		if _, gotDyn := got.(ast.DynTraitType); !gotDyn && got != nil {
			return w.String(), true
		}
	case ast.EnumType:
		g, ok := got.(ast.EnumType)
		if !ok || g.Name != w.Name || len(g.Args) != len(w.Args) {
			return "", false
		}
		for i := range w.Args {
			if p, found := dynElemMismatch(w.Args[i], g.Args[i]); found {
				return p, true
			}
		}
	case ast.TupleType:
		g, ok := got.(ast.TupleType)
		if !ok || len(g.Elems) != len(w.Elems) {
			return "", false
		}
		for i := range w.Elems {
			if p, found := dynElemMismatch(w.Elems[i], g.Elems[i]); found {
				return p, true
			}
		}
	}
	return "", false
}

func containerNoun(t ast.Type) string {
	if _, ok := t.(ast.TupleType); ok {
		return "tuple"
	}
	return "enum"
}

func (c *checker) assignable(dst, src ast.Type) bool {
	return c.assignableWith(dst, src, true)
}

func (c *checker) assignableWith(dst, src ast.Type, dynBox bool) bool {
	if ast.Equal(dst, src) {
		return true
	}
	if _, ok := src.(ast.NeverType); ok {
		return true
	}
	if _, ok := dst.(ast.StrType); ok {
		_, srcIsString := src.(ast.StringType)
		return srcIsString
	}
	if _, ok := src.(ast.StrType); ok {
		return false
	}
	if dt, ok := dst.(ast.DynTraitType); ok {
		if !dynBox {
			return false
		}
		if _, isDyn := src.(ast.DynTraitType); isDyn {
			return false
		}
		return c.canBoxInto(dt, src)
	}
	if dh, ok := dst.(ast.HandleType); ok {
		sh, isHandle := src.(ast.HandleType)
		return isHandle && dh.Borrowed && !sh.Borrowed && dh.Resource == sh.Resource
	}
	if _, ok := src.(ast.HandleType); ok {
		return false
	}
	if c.inStdlibContext() {
		if dn, ok := dst.(ast.NumberType); ok && dn.IsPointerWidth() {
			switch src.(type) {
			case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
				return true
			}
		}
		if sn, ok := src.(ast.NumberType); ok && sn.IsPointerWidth() {
			switch dst.(type) {
			case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
				return true
			}
		}
		if dn, ok := dst.(ast.NumberType); ok && dn.IsPointerWidth() {
			if _, sok := src.(ast.NumberType); sok {
				return true
			}
		}
		if sn, ok := src.(ast.NumberType); ok && sn.IsPointerWidth() {
			if _, dok := dst.(ast.NumberType); dok {
				return true
			}
		}
	}
	if de, dok := dst.(ast.EnumType); dok {
		if se, sok := src.(ast.EnumType); sok && de.Name == se.Name && len(de.Args) == len(se.Args) {
			allOk := true
			for i := range de.Args {
				if unboundEnumArg(se.Args[i]) {
					continue
				}
				if unboundEnumArg(de.Args[i]) {
					allOk = false
					break
				}
				if !c.assignableWith(de.Args[i], se.Args[i], false) {
					allOk = false
					break
				}
			}
			if allOk {
				return true
			}
		}
	}
	if da, dok := dst.(ast.ArrayType); dok {
		if sa, sok := src.(ast.ArrayType); sok && da.Elem != nil {
			if sa.Elem == nil {
				return true
			}
			return c.assignableWith(da.Elem, sa.Elem, false)
		}
	}
	d, dok := dst.(ast.EnumType)
	s, sok := src.(ast.EnumType)
	if dok && sok && d.Name == s.Name && len(s.Args) == 0 && len(d.Args) > 0 {
		return true
	}
	if dok && sok && d.Name == s.Name && len(d.Args) == len(s.Args) && len(d.Args) > 0 {
		for i := range d.Args {
			if !c.assignableWith(d.Args[i], s.Args[i], false) {
				return false
			}
		}
		return true
	}
	if dt, dok := dst.(ast.TupleType); dok {
		if st, sok := src.(ast.TupleType); sok && len(dt.Elems) == len(st.Elems) {
			for i := range dt.Elems {
				if !c.assignableWith(dt.Elems[i], st.Elems[i], false) {
					return false
				}
			}
			return true
		}
	}
	ds, dsok := dst.(ast.StructType)
	ss, ssok := src.(ast.StructType)
	if dsok && ssok && ds.Name == ss.Name && len(ss.Args) == 0 && len(ds.Args) > 0 {
		return true
	}
	if _, ok := dst.(ast.NumberType); ok {
		if sn, ok := src.(ast.NumberType); ok && sn.Polymorphic {
			return true
		}
	}
	if _, ok := dst.(ast.FloatType); ok {
		if sf, ok := src.(ast.FloatType); ok && sf.Polymorphic {
			return true
		}
	}
	return false
}

func payloadCountMsg(variant string, want, got int) string {
	values := "values"
	if want == 1 {
		values = "value"
	}
	return fmt.Sprintf("variant %s carries %d %s, but the pattern binds %d", variant, want, values, got)
}

func arityMsg(subject string, want, got int, unit string) string {
	if want != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%s takes %d %s but was given %d", subject, want, unit, got)
}

func (c *checker) errfCode(pos ast.Position, code, format string, args ...any) {
	c.report(c.currentModule(), pos, code, fmt.Sprintf(format, args...))
}

func firstTryOp(e ast.Expr) (ast.Position, bool) {
	var pos ast.Position
	found := false
	ast.Walk(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.Lambda, *ast.FuncDecl:
			return false
		case *ast.TryOp:
			pos, found = x.P, true
			return false
		}
		return true
	})
	return pos, found
}

func (c *checker) report(path string, pos ast.Position, code, msg string) *Error {
	key := diagKey{pos: pos, code: code, msg: msg, path: path}
	if c.seenDiags == nil {
		c.seenDiags = map[diagKey]bool{}
	}
	if c.seenDiags[key] {
		return nil
	}
	c.seenDiags[key] = true
	err := &Error{Pos: pos, Msg: msg, Path: path, ErrCode: code}
	c.errors = append(c.errors, err)
	return err
}

type diagKey struct {
	pos  ast.Position
	code string
	msg  string
	path string
}

func (c *checker) requireValue(e ast.Expr, t ast.Type) {
	if _, isVoid := t.(ast.VoidType); !isVoid {
		return
	}
	if _, isUnit := e.(*ast.UnitLit); isUnit {
		return
	}
	if _, isBinding := e.(*ast.Ident); isBinding {
		return
	}
	c.errfCode(e.Pos(), "E072",
		"a void expression is not a value here; call it as a statement, and write `()` if you meant the unit value")
}

func (c *checker) hasDropImpl(typeName string) bool {
	for trait, types := range c.info.Impls {
		if !types[typeName] {
			continue
		}
		simple := trait
		if i := strings.LastIndex(simple, "__"); i >= 0 {
			simple = simple[i+2:]
		}
		if simple == "Drop" {
			return true
		}
	}
	return false
}

var pureCollectionMutators = map[string]string{
	"__method_Map_set":    "insert",
	"__method_Map_remove": "remove",
	"__method_Map_clear":  "cleared",
	"__method_Array_set":  "with",
	"__method_Array_push": "append",
}

var retiredCollectionSpellings = map[string]string{
	"Array.push":  "append",
	"Array.set":   "with",
	"Map.set":     "insert",
	"Map.has":     "contains",
	"Map.delete":  "remove",
	"Map.without": "remove",
	"Map.clear":   "cleared",
}

func collectionNamespace(t ast.Type) (string, bool) {
	switch t.(type) {
	case ast.ArrayType:
		return "Array", true
	case ast.SliceType:
		return "slice", true
	}
	return methodTypeName(t)
}

func (c *checker) methodsOn(typeName string) []string {
	prefix := typeName + "."
	var names []string
	for k := range c.info.Methods {
		if strings.HasPrefix(k, prefix) {
			names = append(names, strings.TrimPrefix(k, prefix))
		}
	}
	sort.Strings(names)
	return names
}

func (c *checker) methodsFor(typeName string, recv ast.Type) []string {
	all := c.methodsOn(typeName)
	if recv == nil {
		return all
	}
	out := all[:0:0]
	for _, name := range all {
		mangled, ok := c.info.Methods[typeName+"."+name]
		if !ok {
			out = append(out, name)
			continue
		}
		if !c.methodVisibleHere(mangled) {
			continue
		}
		sig, ok := c.info.FuncSigs[mangled]
		if !ok || len(sig.Params) == 0 {
			out = append(out, name)
			continue
		}
		if containsParamType(sig.Params[0]) {
			if c.boundsAdmitReceiver(mangled, sig.Params[0], recv) {
				out = append(out, name)
			}
			continue
		}
		if c.argAssignable(sig.Params[0], recv, false) {
			out = append(out, name)
		}
	}
	return out
}

func (c *checker) boundsAdmitReceiver(mangled string, param, recv ast.Type) bool {
	fn, ok := c.info.GenericFuncs[mangled]
	if !ok || len(fn.Bounds) == 0 {
		return true
	}
	sub := map[string]ast.Type{}
	if !c.unifyType(param, recv, sub) {
		return true
	}
	for tp, traits := range fn.Bounds {
		bound, ok := sub[tp]
		if !ok {
			continue
		}
		tn, ok := methodTypeName(bound)
		if !ok {
			continue
		}
		for _, traitName := range traits {
			if !c.info.Impls[traitName][tn] {
				return false
			}
		}
	}
	return true
}

func (c *checker) stdlibReceiverMatches(typeName, method string, recv ast.Type) bool {
	pattern := stdlibMethodReceiverPatterns[typeName+"."+method]
	if pattern == nil {
		return true
	}
	sub := map[string]ast.Type{}
	return c.unifyType(pattern, recv, sub) && ast.Equal(substituteType(pattern, sub), recv)
}

func (c *checker) unknownMethodMessage(typeName, method string, recv ast.Type) string {
	msg := fmt.Sprintf("no method %q on %s", method, recv)
	if repl, ok := retiredCollectionSpellings[typeName+"."+method]; ok {
		if typeName == "Map" && (method == "has" || method == "without") {
			return fmt.Sprintf("%s — it was renamed; use %q", msg, repl)
		}
		return fmt.Sprintf("%s — the in-place spelling was removed; use %q, which returns the updated %s (assign the result back)",
			msg, repl, typeName)
	}
	declared := stdlibMethodIndex[typeName]
	if receiver := stdlibMethodReceivers[typeName][method]; receiver != "" && !c.stdlibReceiverMatches(typeName, method, recv) {
		return fmt.Sprintf("%s — %q is declared for %s in %q", msg, method, receiver, declared[method])
	}
	if mod := declared[method]; mod != "" && !c.moduleAlreadyImports(mod) {
		note := ""
		if method == "to_string" {
			note = " (an f-string like `f\"{x}\"` desugars to `x.to_string()`)"
		}
		return fmt.Sprintf("%s — add `import %q`%s", msg, mod, note)
	}
	names := c.methodsFor(typeName, recv)
	if s := diag.Suggest(method, names); s != "" {
		return fmt.Sprintf("%s — did you mean %q?", msg, s)
	}
	if s := diag.Suggest(method, slices.Sorted(maps.Keys(declared))); s != "" && c.stdlibReceiverMatches(typeName, s, recv) && !c.moduleAlreadyImports(declared[s]) {
		return fmt.Sprintf("%s — did you mean %q, which needs `import %q`?", msg, s, declared[s])
	}
	if len(declared) > 0 {
		msg += ", and no stdlib module declares one"
	}
	if len(names) > 0 {
		return fmt.Sprintf("%s (it has: %s)", msg, strings.Join(names, ", "))
	}
	return msg
}

func (c *checker) errUnknownMethod(n *ast.FieldAccess, namespace string, recv ast.Type) {
	e := &Error{Pos: n.P, Msg: c.unknownMethodMessage(namespace, n.Field, recv), Path: c.currentModule(), ErrCode: "E043"}
	_, retired := retiredCollectionSpellings[namespace+"."+n.Field]
	if suggestion := diag.Suggest(n.Field, c.methodsFor(namespace, recv)); suggestion != "" && !retired && n.FieldPos.Line > 0 {
		e.Fix = &diag.Suggestion{Pos: n.FieldPos, Length: len(n.Field), Replacement: suggestion,
			Title: fmt.Sprintf("replace `%s` with `%s`", n.Field, suggestion)}
	}
	c.errors = append(c.errors, e)
}

func hoistReceiver(fn *ast.FuncDecl) {
	if fn.Receiver == nil {
		return
	}
	fn.Params = append([]ast.Param{*fn.Receiver}, fn.Params...)
	fn.Receiver = nil
}

func (c *checker) receiverDeclares(name, tp string) bool {
	fn, ok := c.info.GenericFuncs[name]
	if !ok || fn.MethodRecv == "" || len(fn.Params) == 0 {
		return true
	}
	return typeMentionsParam(fn.Params[0].Type, tp)
}

func elemDispatchable(elem ast.Type) bool {
	switch elem.(type) {
	case ast.ArrayType, ast.SliceType:
		return false
	}
	return true
}

func (c *checker) errArgMismatch(n *ast.Call, i int, recvIsArg0 bool, expected, at ast.Type) {
	if recvIsArg0 && i == 0 && n.Method != nil {
		c.errfCode(n.Method.FieldPos, "E043", "no method %q on %s — %q is declared for %s",
			n.Method.Field, at, n.Method.Field, expected)
		return
	}
	num := i + 1
	if recvIsArg0 {
		num = i
	}
	c.errfCode(n.Args[i].Pos(), "E038", "%s: expected %s, got %s%s",
		argSubject(n, num), expected, at, assignHint(expected, at))
}

func argSubject(n *ast.Call, num int) string {
	name := ""
	if n.Method != nil {
		name = n.Method.Field
	} else if n.SourceCallee != "" {
		name = n.SourceCallee
	} else if id, ok := n.Callee.(*ast.Ident); ok && !strings.HasPrefix(id.Name, "__") {
		name = demangle(id.Name)
	}
	if name == "" {
		return fmt.Sprintf("argument %d", num)
	}
	return fmt.Sprintf("argument %d to %q", num, name)
}

func (c *checker) moduleAlreadyImports(mod string) bool {
	from := c.currentModule()
	if from == "" || c.info == nil {
		return false
	}
	key := stdlib.ModuleKey(mod)
	return key != "" && c.info.DirectImports[from][key]
}

func (c *checker) checkUnusedCollectionResult(e ast.Expr) {
	call, ok := e.(*ast.Call)
	if !ok || call.Method == nil {
		return
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	if _, isMutator := pureCollectionMutators[id.Name]; !isMutator {
		return
	}
	c.errfCode(call.Method.FieldPos, "E055",
		"result of `.%s(...)` is unused; assign it back (e.g. `x = x.%s(...)`) — collection operations return a new value, they do not mutate in place (use `let _ = …` to discard intentionally)",
		call.Method.Field, call.Method.Field)
}

var fipNonAllocMethods = map[string]bool{"len": true}

var fipNonAllocBuiltins = map[string]bool{
	"__memchr": true, "__mismatch_bytes": true, "__mismatch_bytes_str": true, "__count_byte_bytes": true, "__sum_bytes_array": true, "__bsd_sum_bytes": true, "__memchr_bytes": true, "__rmemchr_bytes": true, "__rmemchr": true, "__ascii_run": true, "__count_byte": true,
	"__sum_bytes": true, "__scan_set": true, "__scan_set_bytes": true, "__bsd_sum": true, "__str_hash": true, "__count_runs": true, "__count_runs_bytes": true,
	"__crc32_cksum": true, "__crc32_cksum_array": true,
	"__clz32": true, "__ctz32": true, "__popcount32": true,
	"__clz64": true, "__ctz64": true, "__popcount64": true, "__mulhi_u64": true,
	"__ptr_width": true, "__heap_bump_bytes": true, "__heap_alloc_count": true,
	"monotonic_ns": true,
	"__f64x2_of":   true, "__f64x2_splat": true, "__f64x2_lane": true,
	"__f64x2_lt": true, "__f64x2_ne": true, "__f64x2_bitmask": true, "__f32x4_bitmask": true,
}

func (c *checker) checkFipFunctions(prog *ast.Program) {
	fip := map[string]bool{}
	fbip := map[string]bool{}
	declared := map[string]bool{}
	for _, fn := range prog.Funcs {
		declared[fn.Name] = true
		if fn.Fip {
			fip[fn.Name] = true
		}
		if fn.Fbip {
			fbip[fn.Name] = true
		}
	}
	if len(fip)+len(fbip) == 0 {
		return
	}
	for _, fn := range prog.Funcs {
		if (!fn.Fip && !fn.Fbip) || fn.Body == nil {
			continue
		}
		kw := "fip"
		if fn.Fbip {
			kw = "fbip"
		}
		own := map[string]bool{}
		for _, p := range fn.Params {
			if p.Own {
				own[p.Name] = true
			}
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ArrayLit:
				if x.Const {
					return false
				}
				c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (array literal)", kw, fn.Name)
			case *ast.FString:
				c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (string interpolation)", kw, fn.Name)
			case *ast.Binary:
				if x.IsStringConcat {
					c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (string concatenation)", kw, fn.Name)
				}
			case *ast.Call:
				if x.IsVariantCall {
					return true
				}
				if x.Method != nil {
					if x.Method.Field == "with" && len(x.Args) > 0 && own[fipRootIdent(x.Args[0])] && !fipLinkReadsRoot(x) {
						return true
					}
					if x.Method.Field == "map" && len(x.Args) > 0 && own[fipRootIdent(x.Args[0])] {
						return true
					}
					if !fipNonAllocMethods[x.Method.Field] {
						c.errfCode(x.Pos(), "E053", "`%s` function %q may not call method %q (not proven allocation-free)", kw, fn.Name, x.Method.Field)
					}
					return true
				}
				if id, ok := x.Callee.(*ast.Ident); ok {
					if fipNonAllocBuiltins[id.Name] && !declared[id.Name] {
						return true
					}
					if fn.Fbip {
						if !fip[id.Name] && !fbip[id.Name] {
							c.errfCode(x.Pos(), "E053", "`fbip` function %q may only call `fip` or `fbip` functions, not %q", fn.Name, id.Name)
						}
					} else if !fip[id.Name] {
						c.errfCode(x.Pos(), "E053", "`fip` function %q may only call other `fip` functions, not %q", fn.Name, id.Name)
					}
					return true
				}
				c.errfCode(x.Pos(), "E053", "`%s` function %q may not make an indirect call (not proven allocation-free)", kw, fn.Name)
			case *ast.Assign:
				if fipWriteAllocates(x.Target, own) {
					c.errfCode(x.Pos(), "E053", "`%s` function %q may not write to a non-`own` heap value (triggers a copy-on-write)", kw, fn.Name)
				}
			}
			return true
		})
	}
}

func fipWriteAllocates(target ast.Expr, own map[string]bool) bool {
	switch t := target.(type) {
	case *ast.Ident:
		return false
	case *ast.Index:
		return !own[fipRootIdent(t.Array)]
	case *ast.FieldAccess:
		return !own[fipRootIdent(t.Target)]
	}
	return true
}

func fipLinkReadsRoot(c *ast.Call) bool {
	inner, ok := c.Args[0].(*ast.Call)
	if !ok || inner.Method == nil || inner.Method.Field != "with" {
		return false
	}
	root := fipRootIdent(c.Args[0])
	for _, a := range c.Args[1:] {
		mentioned := false
		ast.Walk(a, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == root {
				mentioned = true
			}
			return !mentioned
		})
		if mentioned {
			return true
		}
	}
	return false
}

func fipRootIdent(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.Index:
			e = x.Array
		case *ast.FieldAccess:
			e = x.Target
		case *ast.Call:
			if x.Method == nil || x.Method.Field != "with" || len(x.Args) == 0 {
				return ""
			}
			e = x.Args[0]
		default:
			return ""
		}
	}
}

func (c *checker) needCoreMap(pos ast.Position) {
	if !c.requireMapImport || c.mapErrReported {
		return
	}
	c.mapErrReported = true
	c.errfCode(pos, "E001", "Map operations require `import \"core/map\";`")
}

var retiredNames = map[string]string{
	"string_from_bytes": "string_from_bytes_unchecked",
}

var retiredHints = map[string]string{
	"string_from_bytes": " (or `std/utf8.from_bytes`, which validates and returns `Option[string]`)",
}

func (c *checker) errIdent(n *ast.Ident, s *scope, format string, args ...any) {
	cands := c.collectNames(s)
	suggestion := diag.Suggest(n.Name, cands)
	if r, ok := retiredNames[n.Name]; ok {
		suggestion = r
	}
	e := &Error{
		Pos:     n.P,
		Span:    len(n.Name),
		Msg:     fmt.Sprintf(format, args...),
		Path:    c.currentModule(),
		ErrCode: "E001",
	}
	if suggestion != "" {
		e.Fix = &diag.Suggestion{
			Pos:         n.P,
			Length:      len(n.Name),
			Replacement: suggestion,
			Title:       fmt.Sprintf("replace `%s` with `%s`%s", n.Name, suggestion, retiredHints[n.Name]),
		}
	}
	c.errors = append(c.errors, e)
}

func (c *checker) errTypeParamAssoc(tid *ast.Ident, field string) {
	why := fmt.Sprintf("%s has no bound; bound it by a trait that declares %s()", tid.Name, field)
	if bounds := c.typeParamDecl(tid.Name).Bounds[tid.Name]; len(bounds) > 0 {
		why = fmt.Sprintf("its bound %s declares none", demangle(strings.Join(bounds, " + ")))
	}
	c.errors = append(c.errors, &Error{
		Pos:     tid.P,
		Span:    len(tid.Name),
		Msg:     fmt.Sprintf("type parameter %s has no associated function %q: %s", tid.Name, field, why),
		Path:    c.currentModule(),
		ErrCode: "E001",
	})
}

func (c *checker) unknownVariantHint(name string, ed *ast.EnumDecl) string {
	if s := c.variantSpellingHint(name, ed); s != "" {
		return s
	}
	return " — a bare name in arm position is a VARIANT, not a binding;" +
		" for a catch-all use `_`, binding the scrutinee to a variable first if you need its value"
}

func (c *checker) variantSpellingHint(name string, ed *ast.EnumDecl) string {
	name = c.variantHintName(ed, name)
	best, bestD := "", 0
	max := len(name)/3 + 1
	for i := range ed.Variants {
		v := c.variantHintName(ed, ed.Variants[i].Name)
		d := editDistanceStr(name, v)
		if d <= max && (best == "" || d < bestD) {
			best, bestD = v, d
		}
	}
	if best != "" {
		return fmt.Sprintf(" (did you mean %q?)", best)
	}
	return ""
}

func editDistanceStr(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = prev[j] + 1
			if cur[j-1]+1 < cur[j] {
				cur[j] = cur[j-1] + 1
			}
			if prev[j-1]+cost < cur[j] {
				cur[j] = prev[j-1] + cost
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func (c *checker) errUnknownField(pos, namePos ast.Position, structName, field string, declared []string) {
	c.errUnknownMember(pos, namePos, structName, "field", field, declared)
}

func (c *checker) errUnknownMember(pos, namePos ast.Position, structName, noun, field string, declared []string) {
	if _, retired := retiredCollectionSpellings[structName+"."+field]; retired {
		c.errfCode(pos, "E043", "%s", c.unknownMethodMessage(structName, field, ast.StructType{Name: structName}))
		return
	}
	e := &Error{
		Pos:     pos,
		Msg:     fmt.Sprintf("struct %s has no %s %q", demangle(structName), noun, field),
		Path:    c.currentModule(),
		ErrCode: "E043",
	}
	if s := diag.Suggest(field, declared); s != "" && namePos.Line > 0 {
		e.Fix = &diag.Suggestion{
			Pos:         namePos,
			Length:      len(field),
			Replacement: s,
			Title:       fmt.Sprintf("replace `%s` with `%s`", field, s),
		}
	}
	c.errors = append(c.errors, e)
}

func (c *checker) collectNames(s *scope) []string {
	seen := map[string]bool{}
	var out []string
	for cur := s; cur != nil; cur = cur.parent {
		for i := len(cur.order) - 1; i >= 0; i-- {
			if name := cur.order[i]; !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	var funcs []string
	for name := range c.info.FuncSigs {
		if !seen[name] && c.funcVisibleHere(name) {
			funcs = append(funcs, name)
		}
	}
	slices.Sort(funcs)
	return append(out, funcs...)
}

func (c *checker) isUserFuncOrLocal(name string, s *scope) bool {
	if _, ok := s.lookup(name); ok {
		return true
	}
	if _, ok := c.visibleFuncSig(name); ok {
		return true
	}
	return false
}

func (c *checker) funcVisibleHere(name string) bool {
	return c.moduleSees(c.currentModule(), c.funcSources[name])
}

func (c *checker) visibleFuncSig(name string) (*ast.FuncType, bool) {
	sig, ok := c.info.FuncSigs[name]
	return sig, ok && c.funcVisibleHere(name)
}

type scope struct {
	parent *scope
	names  map[string]ast.Type
	order  []string
	vars   map[string]*ast.Var
	consts map[string]bool
}

func (s *scope) binder(name string) *scope {
	for cur := s; cur != nil; cur = cur.parent {
		if _, ok := cur.names[name]; ok {
			return cur
		}
	}
	return nil
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: map[string]ast.Type{}}
}

func (s *scope) set(name string, t ast.Type) {
	if _, bound := s.names[name]; !bound {
		s.order = append(s.order, name)
	}
	s.names[name] = t
}

func (s *scope) lookup(name string) (ast.Type, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.names[name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (s *scope) bindVar(name string, t ast.Type, decl *ast.Var) {
	s.set(name, t)
	if s.vars == nil {
		s.vars = map[string]*ast.Var{}
	}
	s.vars[name] = decl
}

func (s *scope) lookupVarDecl(name string) *ast.Var {
	for cur := s; cur != nil; cur = cur.parent {
		if _, ok := cur.names[name]; ok {
			return cur.vars[name]
		}
	}
	return nil
}

func (s *scope) poison(name string) {
	s.set(name, nil)
}

func (s *scope) poisonAll(names []string) {
	for _, n := range names {
		s.poison(n)
	}
}

func (c *checker) capturedType(name string, s *scope) (ast.Type, bool) {
	if _, ok := s.lookup(name); ok {
		return nil, false
	}
	for i := len(c.captureChain) - 1; i >= 0; i-- {
		ent := c.captureChain[i]
		if ent.scope == nil {
			continue
		}
		if t, ok := ent.scope.lookup(name); ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) identValueBinding(name string, s *scope) (ast.Type, bool) {
	if t, ok := s.lookup(name); ok {
		return t, true
	}
	return c.capturedType(name, s)
}

type cellStore struct {
	pos      ast.Position
	name     string
	decl     *ast.Var
	declType ast.Type
	valType  ast.Type
}

func (c *checker) checkFunction(fn *ast.FuncDecl) {
	c.current = fn
	defer func() { c.current = nil }()
	c.capturedVars = nil
	c.cellStores = nil
	defer func() {
		c.capturedVars = nil
		c.cellStores = nil
	}()

	if fn.Body == nil {
		return
	}

	if fn.ReturnUnannotated {
		c.errfCode(fn.P, "E081", "missing return type on function %q; declare it explicitly (use `: void` if it returns nothing)", fn.Name)
	}

	infer := fn.ReturnUnannotated && fn.Receiver == nil && fn.MethodRecv == "" && fn.AssocType == "" && len(fn.TypeParams) == 0
	prevInfer := c.inferReturns
	var rets []ast.Type
	if infer {
		c.inferReturns = &rets
	} else {
		c.inferReturns = nil
	}
	defer func() { c.inferReturns = prevInfer }()

	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errfCode(fn.P, "E018", "duplicate parameter %q", p.Name)
		}
		root.set(p.Name, p.Type)
	}
	for _, cp := range fn.ConstParams {
		if _, dup := root.names[cp]; dup {
			c.errfCode(fn.P, "E018", "duplicate parameter %q", cp)
			continue
		}
		root.set(cp, ast.NumberType{Width: 32, Signed: true})
		if root.consts == nil {
			root.consts = map[string]bool{}
		}
		root.consts[cp] = true
	}
	c.checkBlock(fn.Body, root)
	c.settleLitLocals(fn.Body)
	if infer {
		c.inferReturnType(fn, rets)
	}
	c.checkOwnedParams(fn)
	c.checkMustConsume(fn)
	if fn.Body != nil && !isVoidReturn(fn.ReturnType) && !funcBodyExits(fn.Body) {
		c.errfCode(fn.P, "E052", "missing return: %q has return type %s but can fall off the end without returning a value", fn.Name, fn.ReturnType.String())
	}
	c.checkCaptureCycleStores()
}

func (c *checker) checkCaptureCycleStores() {
	for _, st := range c.cellStores {
		if !c.capturedVars[st.decl] {
			continue
		}
		if st.valType == nil {
			continue
		}
		if !c.typeReachesFunc(st.valType, map[string]bool{}) {
			continue
		}
		c.errfCode(st.pos, "E049",
			"cannot assign a value of type %s to captured %s %q: a captured variable is shared by reference, so storing a value that can reach a closure would close a reference cycle the runtime cannot collect; keep the closure in a variable it does not capture",
			st.valType, st.declType, st.name)
	}
}

func (c *checker) freshLambdaCannotReach(v ast.Expr, decl *ast.Var, s *scope) bool {
	lam, ok := v.(*ast.Lambda)
	if !ok {
		return false
	}
	for _, capture := range lam.Captures {
		if s.lookupVarDecl(capture.Name) == decl {
			return false
		}
		if c.typeReachesFunc(capture.Type, map[string]bool{}) {
			return false
		}
	}
	return true
}

func (c *checker) typeHoldsView(t ast.Type, seen map[string]bool) bool {
	switch v := t.(type) {
	case ast.StrType, ast.SliceType:
		return true
	case ast.ArrayType:
		return c.typeHoldsView(v.Elem, seen)
	case ast.TupleType:
		for _, e := range v.Elems {
			if c.typeHoldsView(e, seen) {
				return true
			}
		}
	case ast.StructType:
		if v.Name == "MapIter" {
			return true
		}
		if seen[v.Name] {
			return false
		}
		seen[v.Name] = true
		for _, a := range v.Args {
			if c.typeHoldsView(a, seen) {
				return true
			}
		}
		if sd, ok := c.info.Structs[v.Name]; ok {
			for _, f := range sd.Fields {
				if c.typeHoldsView(f.Type, seen) {
					return true
				}
			}
		}
	case ast.EnumType:
		if seen[v.Name] {
			return false
		}
		seen[v.Name] = true
		for _, a := range v.Args {
			if c.typeHoldsView(a, seen) {
				return true
			}
		}
		if ed, ok := c.info.Enums[v.Name]; ok {
			for _, variant := range ed.Variants {
				for _, pt := range variant.Payloads {
					if c.typeHoldsView(pt, seen) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (c *checker) captureView(p ast.Position, name string, t ast.Type) {
	if c.typeHoldsView(t, map[string]bool{}) {
		c.errfCode(p, "E082", "closure captures %q, which holds a borrowed view: a function value's environment cannot carry a view — copy it into an owned value before the closure (`.to_owned()` on a `str`)", name)
	}
}

func declArgs(params []string, args []ast.Type) map[string]ast.Type {
	if len(params) == 0 || len(params) != len(args) {
		return nil
	}
	sub := make(map[string]ast.Type, len(params))
	for i, p := range params {
		sub[p] = args[i]
	}
	return sub
}

func (c *checker) typeReachesFunc(t ast.Type, seen map[string]bool) bool {
	switch v := t.(type) {
	case nil:
		return false
	case *ast.FuncType:
		return true
	case ast.DynTraitType, ast.ParamType, ast.SelfType, ast.ProjType:
		return true
	case ast.ArrayType:
		return c.typeReachesFunc(v.Elem, seen)
	case ast.SliceType:
		return c.typeReachesFunc(v.Elem, seen)
	case ast.StreamType:
		return c.typeReachesFunc(v.Elem, seen)
	case ast.TupleType:
		for _, e := range v.Elems {
			if c.typeReachesFunc(e, seen) {
				return true
			}
		}
		return false
	case ast.StructType:
		if seen[v.Name] {
			return false
		}
		seen[v.Name] = true
		for _, a := range v.Args {
			if c.typeReachesFunc(a, seen) {
				return true
			}
		}
		if sd, ok := c.info.Structs[v.Name]; ok {
			sub := declArgs(sd.TypeParams, v.Args)
			for _, f := range sd.Fields {
				if c.typeReachesFunc(substituteType(f.Type, sub), seen) {
					return true
				}
			}
		}
		return false
	case ast.EnumType:
		if seen[v.Name] {
			return false
		}
		seen[v.Name] = true
		for _, a := range v.Args {
			if c.typeReachesFunc(a, seen) {
				return true
			}
		}
		if ed, ok := c.info.Enums[v.Name]; ok {
			sub := declArgs(ed.TypeParams, v.Args)
			for _, variant := range ed.Variants {
				for _, pt := range variant.Payloads {
					if c.typeReachesFunc(substituteType(pt, sub), seen) {
						return true
					}
				}
			}
		}
		return false
	}
	return false
}

func (c *checker) checkEscapes(prog *ast.Program) {
	c.checkSliceEscapes(prog)
	c.checkStrEscapes(prog)
	c.checkCursorEscapes(prog)
}

func (c *checker) checkCursorEscapes(prog *ast.Program) {
	envs, order := c.escapeEnvs(prog, mentionsMapIter)
	for _, fn := range order {
		env := envs[fn]
		sources := bindingSources(fn)
		forEachReturn(fn, func(ret *ast.Return) {
			if returnsLocalCursor(ret.Value, env, sources) {
				c.errfCode(ret.P, "E065", "returning a `MapIter` over a function-local map: the map is reclaimed when %q returns, leaving the cursor reading freed storage — return the map and iterate it in the caller, or iterate a map the caller passed in", fn.Name)
			}
		})
	}
}

func mentionsMapIter(t ast.Type) bool {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name == "MapIter"
	case ast.ArrayType:
		return mentionsMapIter(x.Elem)
	case ast.TupleType:
		for _, e := range x.Elems {
			if mentionsMapIter(e) {
				return true
			}
		}
	}
	return false
}

func returnsLocalCursor(e ast.Expr, env *escapeEnv, sources map[string][]ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Call:
		if id, ok := x.Callee.(*ast.Ident); ok && id.Name == "__method_Map_iter" && len(x.Args) == 1 {
			return !lentPlace(x.Args[0], env)
		}
	case *ast.ArrayLit:
		return anyLocalCursor(x.Elems, env, sources)
	case *ast.TupleLit:
		return anyLocalCursor(x.Elems, env, sources)
	case *ast.Ident:
		if env.visiting[x.Name] {
			return false
		}
		env.visiting[x.Name] = true
		defer delete(env.visiting, x.Name)
		return anyLocalCursor(sources[x.Name], env, sources)
	}
	return false
}

func anyLocalCursor(es []ast.Expr, env *escapeEnv, sources map[string][]ast.Expr) bool {
	for _, e := range es {
		if returnsLocalCursor(e, env, sources) {
			return true
		}
	}
	return false
}

func bindingSources(fn *ast.FuncDecl) map[string][]ast.Expr {
	out := map[string][]ast.Expr{}
	if fn.Body == nil {
		return out
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if x.Init != nil {
				out[x.Name] = append(out[x.Name], x.Init)
			}
		case *ast.Assign:
			if id, ok := x.Target.(*ast.Ident); ok {
				out[id.Name] = append(out[id.Name], x.Value)
			}
		}
		return true
	})
	return out
}

func lentPlace(e ast.Expr, env *escapeEnv) bool {
	for {
		switch x := e.(type) {
		case *ast.FieldAccess:
			e = x.Target
		case *ast.Ident:
			_, ok := env.params[x.Name]
			return ok
		default:
			return false
		}
	}
}

type escapeEnv struct {
	params   map[string]int
	locals   map[string]*ast.Var
	armViews map[string]ast.Expr
	visiting map[string]bool
}

func (c *checker) escapeEnv(fn *ast.FuncDecl) *escapeEnv {
	if fn.Body == nil {
		return nil
	}
	env := &escapeEnv{
		params:   map[string]int{},
		locals:   map[string]*ast.Var{},
		armViews: map[string]ast.Expr{},
		visiting: map[string]bool{},
	}
	for i, p := range fn.Params {
		env.params[p.Name] = i
	}
	for _, v := range c.info.Locals[fn] {
		env.locals[v.Name] = v
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch m := n.(type) {
		case *ast.Match:
			for _, arm := range m.Arms {
				bindArmViews(m.Tag, arm.Bindings, arm.BindingTypes, env)
			}
		case *ast.MatchExpr:
			for _, arm := range m.Arms {
				bindArmViews(m.Tag, arm.Bindings, arm.BindingTypes, env)
			}
		}
		return true
	})
	return env
}

func (c *checker) escapeEnvs(prog *ast.Program, wants func(ast.Type) bool) (map[*ast.FuncDecl]*escapeEnv, []*ast.FuncDecl) {
	envs := map[*ast.FuncDecl]*escapeEnv{}
	var order []*ast.FuncDecl
	for _, fn := range prog.Funcs {
		if _, dup := envs[fn]; dup || !wants(fn.ReturnType) {
			continue
		}
		env := c.escapeEnv(fn)
		if env == nil {
			continue
		}
		envs[fn] = env
		order = append(order, fn)
	}
	return envs, order
}

type viewRoots struct {
	local  bool
	params map[int]bool
}

func (r *viewRoots) merge(o viewRoots) {
	r.local = r.local || o.local
	for i := range o.params {
		r.params[i] = true
	}
}

func forEachReturn(fn *ast.FuncDecl, f func(*ast.Return)) {
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if ret, ok := n.(*ast.Return); ok && ret.Value != nil {
			f(ret)
		}
		return true
	})
}

type viewSummary struct {
	params map[int]bool
	fresh  bool
}

func returnViewSummaries(fns []*ast.FuncDecl, rootsOf func(*ast.FuncDecl, ast.Expr, map[string]viewSummary) viewRoots) map[string]viewSummary {
	sums := map[string]viewSummary{}
	for _, fn := range fns {
		sums[fn.Name] = viewSummary{params: map[int]bool{}}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			cur := sums[fn.Name]
			owned := !isViewReturn(fn.ReturnType)
			forEachReturn(fn, func(ret *ast.Return) {
				roots := rootsOf(fn, ret.Value, sums)
				for i := range roots.params {
					if !cur.params[i] {
						cur.params[i] = true
						changed = true
					}
				}
				if roots.local && owned && !cur.fresh {
					cur.fresh = true
					sums[fn.Name] = cur
					changed = true
				}
			})
		}
	}
	return sums
}

func isViewReturn(t ast.Type) bool {
	switch t.(type) {
	case ast.SliceType, ast.StrType:
		return true
	}
	return false
}

func (c *checker) checkSliceEscapes(prog *ast.Program) {
	envs, order := c.escapeEnvs(prog, func(t ast.Type) bool { return true })
	sums := returnViewSummaries(order, func(fn *ast.FuncDecl, ret ast.Expr, sums map[string]viewSummary) viewRoots {
		if _, isSlice := fn.ReturnType.(ast.SliceType); isSlice {
			return c.sliceViewRoots(ret, envs[fn], sums)
		}
		return c.sliceSourceRoots(ret, envs[fn], sums)
	})
	for _, fn := range prog.Funcs {
		env, ok := envs[fn]
		if !ok {
			continue
		}
		if _, isSlice := fn.ReturnType.(ast.SliceType); !isSlice {
			continue
		}
		forEachReturn(fn, func(ret *ast.Return) {
			if c.sliceViewRoots(ret.Value, env, sums).local {
				c.errfCode(ret.P, "E063", "returning a `[T]` slice that views function-local storage: the backing array is reclaimed when %q returns, leaving a dangling view — return an owned array (`T[]`) or slice a parameter instead", fn.Name)
			}
		})
	}
}

func (c *checker) sliceViewRoots(expr ast.Expr, env *escapeEnv, sums map[string]viewSummary) viewRoots {
	out := viewRoots{params: map[int]bool{}}
	switch e := expr.(type) {
	case *ast.SliceExpr:
		if e.IsString {
			return out
		}
		out.merge(c.sliceSourceRoots(e.Source, env, sums))
	case *ast.Call:
		if id, ok := e.Callee.(*ast.Ident); ok {
			for i := range sums[id.Name].params {
				if i < len(e.Args) {
					out.merge(c.sliceSourceRoots(e.Args[i], env, sums))
				}
			}
		}
	case *ast.Ident:
		if i, ok := env.params[e.Name]; ok {
			out.params[i] = true
			return out
		}
		v, ok := env.locals[e.Name]
		if !ok || v.Init == nil || env.visiting[e.Name] {
			return out
		}
		env.visiting[e.Name] = true
		defer delete(env.visiting, e.Name)
		out.merge(c.sliceViewRoots(v.Init, env, sums))
	}
	return out
}

func (c *checker) sliceSourceRoots(src ast.Expr, env *escapeEnv, sums map[string]viewSummary) viewRoots {
	out := viewRoots{params: map[int]bool{}}
	switch s := src.(type) {
	case *ast.ArrayLit:
		out.local = true
	case *ast.SliceExpr:
		if s.IsString {
			return out
		}
		out.merge(c.sliceSourceRoots(s.Source, env, sums))
	case *ast.Call:
		if id, ok := s.Callee.(*ast.Ident); ok {
			sum := sums[id.Name]
			out.local = out.local || sum.fresh
			for i := range sum.params {
				if i < len(s.Args) {
					out.merge(c.sliceSourceRoots(s.Args[i], env, sums))
				}
			}
		}
	case *ast.Ident:
		if i, ok := env.params[s.Name]; ok {
			out.params[i] = true
			return out
		}
		if env.visiting[s.Name] {
			return out
		}
		v, ok := env.locals[s.Name]
		if !ok {
			return out
		}
		if _, isArr := v.Type.(ast.ArrayType); isArr {
			out.local = true
			return out
		}
		if _, isSlice := v.Type.(ast.SliceType); isSlice && v.Init != nil {
			env.visiting[s.Name] = true
			defer delete(env.visiting, s.Name)
			out.merge(c.sliceViewRoots(v.Init, env, sums))
		}
	}
	return out
}

func (c *checker) checkStrEscapes(prog *ast.Program) {
	envs, order := c.escapeEnvs(prog, func(t ast.Type) bool {
		switch t.(type) {
		case ast.StrType, ast.StringType:
			return true
		}
		return false
	})
	sums := returnViewSummaries(order, func(fn *ast.FuncDecl, ret ast.Expr, sums map[string]viewSummary) viewRoots {
		if _, isStr := fn.ReturnType.(ast.StrType); isStr {
			return c.strViewRoots(ret, envs[fn], sums)
		}
		return c.stringSourceRoots(ret, envs[fn], sums)
	})
	for _, fn := range order {
		if _, isStr := fn.ReturnType.(ast.StrType); !isStr {
			continue
		}
		forEachReturn(fn, func(ret *ast.Return) {
			if c.strViewRoots(ret.Value, envs[fn], sums).local {
				c.errfCode(ret.P, "E065", "returning a `str` view of a function-local string: the backing string is reclaimed when %q returns, leaving a dangling view — return an owned `string` (materialise with .to_owned()) or view a parameter instead", fn.Name)
			}
		})
	}
}

func bindArmViews(tag ast.Expr, names []string, types []ast.Type, env *escapeEnv) {
	src := viewedSource(tag, env, map[string]bool{})
	if src == nil {
		return
	}
	for i, name := range names {
		if i >= len(types) {
			break
		}
		if _, isStr := types[i].(ast.StrType); isStr {
			env.armViews[name] = src
		}
	}
}

func viewedSource(e ast.Expr, env *escapeEnv, visiting map[string]bool) ast.Expr {
	switch x := e.(type) {
	case *ast.SliceExpr:
		return x.Source
	case *ast.Call:
		if id, ok := x.Callee.(*ast.Ident); ok && id.Name == "slice_unchecked" && len(x.Args) == 3 {
			return x.Args[0]
		}
	case *ast.Ident:
		if visiting[x.Name] {
			return nil
		}
		if v, ok := env.locals[x.Name]; ok && v.Init != nil {
			visiting[x.Name] = true
			defer delete(visiting, x.Name)
			return viewedSource(v.Init, env, visiting)
		}
	}
	return nil
}

func (c *checker) stringSourceRoots(expr ast.Expr, env *escapeEnv, sums map[string]viewSummary) viewRoots {
	out := viewRoots{params: map[int]bool{}}
	switch e := expr.(type) {
	case *ast.StringLit:
		return out
	case *ast.Ident:
		if i, ok := env.params[e.Name]; ok {
			out.params[i] = true
			return out
		}
		if v, ok := env.locals[e.Name]; ok {
			if _, isString := v.Type.(ast.StringType); isString {
				out.local = true
				return out
			}
			if v.Init != nil && !env.visiting[e.Name] {
				env.visiting[e.Name] = true
				defer delete(env.visiting, e.Name)
				return c.stringSourceRoots(v.Init, env, sums)
			}
		}
		out.local = true
		return out
	case *ast.Call:
		if id, ok := e.Callee.(*ast.Ident); ok {
			sum, summarised := sums[id.Name]
			out.local = !summarised || sum.fresh
			for i := range sum.params {
				if i < len(e.Args) {
					out.merge(c.stringSourceRoots(e.Args[i], env, sums))
				}
			}
			return out
		}
	}
	out.local = true
	return out
}

func (c *checker) strViewRoots(expr ast.Expr, env *escapeEnv, sums map[string]viewSummary) viewRoots {
	out := viewRoots{params: map[int]bool{}}
	switch e := expr.(type) {
	case *ast.StringLit:
		return out
	case *ast.SliceExpr:
		return out
	case *ast.Call:
		if id, ok := e.Callee.(*ast.Ident); ok {
			if id.Name == "slice_unchecked" && len(e.Args) == 3 {
				out.merge(c.stringSourceRoots(e.Args[0], env, sums))
				return out
			}
			for i := range sums[id.Name].params {
				if i < len(e.Args) {
					out.merge(c.strViewRoots(e.Args[i], env, sums))
				}
			}
		}
		return out
	case *ast.Ident:
		if src, ok := env.armViews[e.Name]; ok && !env.visiting[e.Name] {
			env.visiting[e.Name] = true
			defer delete(env.visiting, e.Name)
			return c.strViewRoots(src, env, sums)
		}
		if i, ok := env.params[e.Name]; ok {
			out.params[i] = true
			return out
		}
		v, ok := env.locals[e.Name]
		if !ok || env.visiting[e.Name] {
			return out
		}
		if _, isStr := v.Type.(ast.StrType); isStr && v.Init != nil {
			env.visiting[e.Name] = true
			defer delete(env.visiting, e.Name)
			return c.strViewRoots(v.Init, env, sums)
		}
		if _, isString := v.Type.(ast.StringType); isString {
			out.local = true
		}
		return out
	}
	return out
}

func (c *checker) inferReturnType(fn *ast.FuncDecl, rets []ast.Type) {
	var unified ast.Type
	hasVoid := false
	for _, t := range rets {
		if t == nil {
			hasVoid = true
			continue
		}
		if unified == nil {
			unified = t
			continue
		}
		if u, ok := unifyReturnType(unified, t); ok {
			unified = u
		} else {
			c.errfCode(fn.P, "E002", "cannot infer return type for %q: conflicting return types %s and %s; add an explicit return type", fn.Name, unified, t)
		}
	}
	if unified == nil {
		return
	}
	if hasVoid {
		c.errfCode(fn.P, "E012", "function %q returns a value on some paths but not others; add an explicit return type", fn.Name)
	}
	sig := c.info.FuncSigs[fn.Name]
	if sig != nil && typeMentionsSig(unified, sig) {
		c.errfCode(fn.P, "E002", "cannot infer return type for %q: it returns itself; add an explicit return type", fn.Name)
		return
	}
	fn.ReturnType = unified
	if sig != nil {
		sig.Result = unified
	}
}

func typeMentionsSig(t ast.Type, sig *ast.FuncType) bool {
	switch x := t.(type) {
	case *ast.FuncType:
		if x == sig {
			return true
		}
		for _, p := range x.Params {
			if typeMentionsSig(p, sig) {
				return true
			}
		}
		return typeMentionsSig(x.Result, sig)
	case ast.ArrayType:
		return typeMentionsSig(x.Elem, sig)
	case ast.SliceType:
		return typeMentionsSig(x.Elem, sig)
	case ast.StreamType:
		return typeMentionsSig(x.Elem, sig)
	case ast.TupleType:
		for _, e := range x.Elems {
			if typeMentionsSig(e, sig) {
				return true
			}
		}
	case ast.StructType:
		for _, a := range x.Args {
			if typeMentionsSig(a, sig) {
				return true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if typeMentionsSig(a, sig) {
				return true
			}
		}
	}
	return false
}

func unifyReturnType(a, b ast.Type) (ast.Type, bool) {
	if ast.Equal(a, b) {
		return a, true
	}
	if _, ok := a.(ast.NeverType); ok {
		return b, true
	}
	if _, ok := b.(ast.NeverType); ok {
		return a, true
	}
	ea, aok := a.(ast.EnumType)
	eb, bok := b.(ast.EnumType)
	if aok && bok && ea.Name == eb.Name {
		return mergeEnumInference(ea, eb)
	}
	return a, false
}

func (c *checker) methodConsumesReceiver(m *ast.MethodCallSite) bool {
	if m == nil {
		return false
	}
	if dt, ok := m.Receiver.(ast.DynTraitType); ok {
		if m.OwnerTrait != "" {
			return c.dynMethodConsumes(m.OwnerTrait, m.Field)
		}
		for _, tr := range dt.Traits {
			td, ok := c.info.Traits[tr]
			if !ok {
				continue
			}
			for i := range td.Methods {
				if td.Methods[i].Name == m.Field {
					return traitSelfIsOwn(td.Methods[i])
				}
			}
		}
		return false
	}
	typeName, ok := methodTypeName(m.Receiver)
	if !ok {
		return false
	}
	if mangled, _, ok := c.resolveMethod(typeName, m.Field, []string{m.OwnerTrait}); ok {
		flags := c.ownFuncs[mangled]
		return len(flags) > 0 && flags[0]
	}
	return false
}

func (c *checker) dynMethodConsumes(trait, field string) bool {
	td, ok := c.info.Traits[trait]
	if !ok {
		return false
	}
	for i := range td.Methods {
		if td.Methods[i].Name == field {
			return traitSelfIsOwn(td.Methods[i])
		}
	}
	return false
}

func traitSelfIsOwn(m ast.TraitMethod) bool {
	return !m.Assoc && len(m.Params) > 0 && m.Params[0].Own
}

func SelfReassignOwnMoveArg(asn *ast.Assign, ownFuncs map[string][]bool) *ast.Ident {
	call, ok := asn.Value.(*ast.Call)
	if !ok {
		return nil
	}
	cid, isID := call.Callee.(*ast.Ident)
	if !isID {
		return nil
	}
	return selfReassignOwnMoveArgWith(asn, ownFuncs[cid.Name])
}

func selfReassignOwnMoveArgWith(asn *ast.Assign, flags []bool) *ast.Ident {
	if len(flags) == 0 {
		return nil
	}
	tid, ok := asn.Target.(*ast.Ident)
	if !ok {
		return nil
	}
	call, ok := asn.Value.(*ast.Call)
	if !ok {
		return nil
	}
	cid, ok := call.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	count := 0
	ast.Walk(call, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id != cid && id.Name == tid.Name {
			count++
		}
		return true
	})
	if count != 1 {
		return nil
	}
	for i, a := range call.Args {
		if id, ok := a.(*ast.Ident); ok && id.Name == tid.Name {
			if i < len(flags) && flags[i] {
				return id
			}
			return nil
		}
	}
	return nil
}

func (c *checker) selfReassignOwnMoveArg(asn *ast.Assign) *ast.Ident {
	if arg := SelfReassignOwnMoveArg(asn, c.ownFuncs); arg != nil {
		return arg
	}
	call, ok := asn.Value.(*ast.Call)
	if !ok {
		return nil
	}
	return selfReassignOwnMoveArgWith(asn, c.callOwnFlags[call])
}

func FieldMoveLiteral(e ast.Expr) *ast.StructLit {
	if sl, ok := e.(*ast.StructLit); ok {
		return sl
	}
	if call, ok := e.(*ast.Call); ok && call.IsVariantCall && len(call.Args) == 1 {
		if sl, ok := call.Args[0].(*ast.StructLit); ok {
			return sl
		}
	}
	return nil
}

func SupersededFieldOwnMoveArgs(sl *ast.StructLit, target string, ownFuncs map[string][]bool) []*ast.FieldAccess {
	if len(ownFuncs) == 0 {
		return nil
	}
	return SupersededFieldMoves(sl, target, func(field string, value ast.Expr) *ast.FieldAccess {
		call, ok := value.(*ast.Call)
		if !ok || call.Method != nil {
			return nil
		}
		cid, ok := call.Callee.(*ast.Ident)
		if !ok {
			return nil
		}
		flags, isOwn := ownFuncs[cid.Name]
		if !isOwn {
			return nil
		}
		for i, a := range call.Args {
			if i >= len(flags) || !flags[i] {
				continue
			}
			if fa, ok := a.(*ast.FieldAccess); ok && fa.Field == field {
				return fa
			}
		}
		return nil
	})
}

func SupersededFieldMoves(sl *ast.StructLit, target string, consumed func(field string, value ast.Expr) *ast.FieldAccess) []*ast.FieldAccess {
	if sl.Base == nil {
		return nil
	}
	if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != target {
		return nil
	}
	fieldTargets := map[*ast.Ident]bool{}
	fieldReads := map[string]int{}
	ast.Walk(sl, func(n ast.Node) bool {
		if fa, ok := n.(*ast.FieldAccess); ok {
			if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
				fieldTargets[id] = true
				fieldReads[fa.Field]++
			}
		}
		return true
	})
	bare := 0
	ast.Walk(sl, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == target && !fieldTargets[id] {
			bare++
		}
		return true
	})
	if bare != 1 {
		return nil
	}
	var out []*ast.FieldAccess
	for _, f := range sl.Fields {
		if fieldReads[f.Name] != 1 {
			continue
		}
		fa := consumed(f.Name, f.Value)
		if fa == nil || fa.Field != f.Name {
			continue
		}
		if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
			out = append(out, fa)
		}
	}
	return out
}

func (c *checker) checkOwnedParams(fn *ast.FuncDecl) {
	owned := map[string]bool{}
	borrowedParam := map[string]bool{}
	for _, p := range fn.Params {
		if p.Own && !definitelyScalar(p.Type) {
			owned[p.Name] = true
		} else {
			borrowedParam[p.Name] = true
		}
	}
	if (len(owned) == 0 && len(c.ownFuncs) == 0 && len(c.callOwnFlags) == 0) || fn.Body == nil {
		return
	}

	selfMoveArgs := map[ast.Expr]bool{}
	lastUse := lastUseArgs(fn, c.info)
	var isOwnedExpr func(e ast.Expr) bool
	isOwnedExpr = func(e ast.Expr) bool {
		if c.scalarArgs[e] {
			return true
		}
		switch x := e.(type) {
		case *ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit:
			return true
		case *ast.Binary:
			return x.IsStringConcat
		case *ast.Ident:
			if _, vrOk, _ := c.resolveVariant(x.Name, x.EnumName); vrOk {
				return true
			}
			return owned[x.Name] || selfMoveArgs[e] || lastUse[e] || c.scalarArgs[e]
		case *ast.FieldAccess:
			if _, isVariant := c.info.EnumConstructions[x]; isVariant {
				return true
			}
			return selfMoveArgs[e] || c.scalarArgs[e]
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
					return true
				}
				if sig, ok := c.info.FuncSigs[id.Name]; ok && sig.Result != nil && ast.IsPointerType(sig.Result) {
					if !c.calleeResultBorrows(x) {
						return true
					}
				}
			}
			return false
		}
		return false
	}
	guardCallArgs := func(x *ast.Call) {
		flags, isOwn := c.callOwnFlags[x]
		if !isOwn {
			id, idOK := x.Callee.(*ast.Ident)
			if !idOK {
				return
			}
			if flags, isOwn = c.ownFuncs[id.Name]; !isOwn {
				return
			}
		}
		for i := 0; i < len(x.Args) && i < len(flags); i++ {
			if flags[i] && !isOwnedExpr(x.Args[i]) {
				c.errfCode(x.Args[i].Pos(), "E051", "argument to owned parameter must be an owned value (a fresh construction, another `own` parameter, or a local at its last use), not a borrowed one")
			}
		}
	}

	type movedSet = map[string]ast.Position

	var walkStmt func(st ast.Stmt, moved movedSet)
	var walkStmts func(stmts []ast.Stmt, moved movedSet)

	walkNested := func(params []ast.Param, body *ast.Block, moved movedSet) {
		if body == nil {
			return
		}
		type binding struct {
			wasOwned    bool
			wasBorrowed bool
			wasMoved    ast.Position
			hadMoved    bool
		}
		shadowed := map[string]binding{}
		for _, p := range params {
			if _, dup := shadowed[p.Name]; dup {
				continue
			}
			b := binding{wasOwned: owned[p.Name], wasBorrowed: borrowedParam[p.Name]}
			b.wasMoved, b.hadMoved = moved[p.Name]
			shadowed[p.Name] = b
			if p.Own && !definitelyScalar(p.Type) {
				owned[p.Name] = true
				delete(borrowedParam, p.Name)
			} else {
				delete(owned, p.Name)
				borrowedParam[p.Name] = true
			}
			delete(moved, p.Name)
		}
		walkStmts(body.Stmts, moved)
		for name, b := range shadowed {
			if b.wasOwned {
				owned[name] = true
			} else {
				delete(owned, name)
			}
			if b.wasBorrowed {
				borrowedParam[name] = true
			} else {
				delete(borrowedParam, name)
			}
			if b.hadMoved {
				moved[name] = b.wasMoved
			} else {
				delete(moved, name)
			}
		}
	}

	joinInto := func(dst, branch movedSet, diverges bool) {
		if diverges {
			return
		}
		for k, v := range branch {
			if _, ok := dst[k]; !ok {
				dst[k] = v
			}
		}
	}
	cloneMoved := func(m movedSet) movedSet {
		n := make(movedSet, len(m))
		for k, v := range m {
			n[k] = v
		}
		return n
	}

	bindArm := func(bindings []string, types []ast.Type, scrutOwned bool) (added, lent []string) {
		for i, bname := range bindings {
			if bname == "" || bname == "_" || i >= len(types) || types[i] == nil || !ast.IsPointerType(types[i]) {
				continue
			}
			if scrutOwned {
				if !owned[bname] {
					owned[bname] = true
					added = append(added, bname)
				}
			} else if !borrowedParam[bname] {
				borrowedParam[bname] = true
				lent = append(lent, bname)
			}
		}
		return added, lent
	}
	unbindArm := func(added, lent []string, armMoved movedSet) {
		for _, bname := range lent {
			delete(borrowedParam, bname)
		}
		for _, bname := range added {
			delete(owned, bname)
			delete(armMoved, bname)
		}
	}

	var recordExprUses func(e ast.Expr, moved movedSet)
	recordExprUses = func(e ast.Expr, moved movedSet) {
		if e == nil {
			return
		}
		borrow := map[*ast.Ident]bool{}
		dynConsumed := map[*ast.Ident]bool{}
		var lambdas []*ast.Lambda
		var matches []*ast.MatchExpr
		ast.Walk(e, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Lambda:
				lambdas = append(lambdas, x)
				return false
			case *ast.MatchExpr:
				matches = append(matches, x)
				return false
			case *ast.FieldAccess:
				if id, ok := x.Target.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.Index:
				if id, ok := x.Array.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.Idx.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.CastExpr:
				if id, ok := x.Inner.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.DowncastExpr:
				if id, ok := x.Inner.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.Binary:
				if id, ok := x.Left.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.Right.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.Unary:
				if id, ok := x.Operand.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.SliceExpr:
				if id, ok := x.Source.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.Low.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.High.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.StructLit:
				if x.Base != nil {
					if id, ok := x.Base.(*ast.Ident); ok {
						borrow[id] = true
					}
				}
			case *ast.Call:
				guardCallArgs(x)
				if id, ok := x.Callee.(*ast.Ident); ok {
					borrow[id] = true
				}
				flags, isOwn := c.callOwnFlags[x]
				if id, named := x.Callee.(*ast.Ident); named && !isOwn {
					flags = c.ownFuncs[id.Name]
				}
				for ai, arg := range x.Args {
					if x.Method != nil && ai == 0 {
						continue
					}
					if ai < len(flags) && flags[ai] {
						continue
					}
					if aid, ok := arg.(*ast.Ident); ok {
						borrow[aid] = true
					}
				}
				if x.Method != nil && len(x.Args) > 0 && !c.methodConsumesReceiver(x.Method) {
					if id, ok := x.Args[0].(*ast.Ident); ok {
						borrow[id] = true
					}
				}
				if x.DynTrait != "" {
					if fa, ok := x.Callee.(*ast.FieldAccess); ok {
						if rid, ok := fa.Target.(*ast.Ident); ok && c.dynMethodConsumes(x.DynTrait, fa.Field) {
							dynConsumed[rid] = true
						}
					}
				}
			}
			return true
		})
		for id := range dynConsumed {
			delete(borrow, id)
		}
		ast.Walk(e, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.Lambda, *ast.MatchExpr:
				return false
			}
			id, ok := n.(*ast.Ident)
			if !ok || !owned[id.Name] {
				return true
			}
			if at, isMoved := moved[id.Name]; isMoved {
				c.errfCode(id.Pos(), "E050", "use of owned parameter %q after it was consumed (moved at line %d)", id.Name, at.Line)
				return true
			}
			if !borrow[id] {
				moved[id.Name] = id.Pos()
			}
			return true
		})
		for _, m := range matches {
			scrutOwned := c.isOwnedScrutinee(m.Tag, isOwnedExpr)
			if id, ok := m.Tag.(*ast.Ident); ok && !ast.MatchExprTakesPointerPayload(m.Arms) {
				prev, had := moved[id.Name]
				recordExprUses(m.Tag, moved)
				if had {
					moved[id.Name] = prev
				} else {
					delete(moved, id.Name)
				}
			} else {
				recordExprUses(m.Tag, moved)
			}
			for _, arm := range m.Arms {
				armMoved := cloneMoved(moved)
				added, lent := bindArm(arm.Bindings, arm.BindingTypes, scrutOwned)
				recordExprUses(arm.Guard, armMoved)
				recordExprUses(arm.Body, armMoved)
				unbindArm(added, lent, armMoved)
				joinInto(moved, armMoved, false)
			}
		}
		for _, lam := range lambdas {
			walkNested(lam.Params, lam.Body, moved)
		}
	}

	loopBody := func(body ast.Stmt, step ast.Stmt, moved movedSet) {
		inner := cloneMoved(moved)
		if body != nil {
			walkStmt(body, inner)
		}
		if step != nil {
			walkStmt(step, inner)
		}
		for name, at := range inner {
			if _, wasLive := moved[name]; !wasLive {
				c.errfCode(at, "E050", "owned parameter %q is consumed inside a loop; a later iteration would use it after move", name)
			}
		}
	}

	walkStmt = func(st ast.Stmt, moved movedSet) {
		switch x := st.(type) {
		case *ast.Block:
			walkStmts(x.Stmts, moved)
		case *ast.FuncDecl:
			walkNested(x.Params, x.Body, moved)
		case *ast.Var:
			recordExprUses(x.Init, moved)
		case *ast.ExprStmt:
			if asn, ok := x.Expr.(*ast.Assign); ok {
				if id, ok := asn.Target.(*ast.Ident); ok && !owned[id.Name] {
					if arg := c.selfReassignOwnMoveArg(asn); arg != nil {
						selfMoveArgs[arg] = true
					}
				}
				if id, ok := asn.Target.(*ast.Ident); ok && !borrowedParam[id.Name] {
					if sl := FieldMoveLiteral(asn.Value); sl != nil {
						for _, arg := range SupersededFieldOwnMoveArgs(sl, id.Name, c.ownFuncs) {
							selfMoveArgs[arg] = true
						}
					}
				}
				recordExprUses(asn.Value, moved)
				if id, ok := asn.Target.(*ast.Ident); ok && owned[id.Name] {
					delete(moved, id.Name)
				} else {
					recordExprUses(asn.Target, moved)
				}
				return
			}
			recordExprUses(x.Expr, moved)
		case *ast.Return:
			if sl := FieldMoveLiteral(x.Value); sl != nil && sl.Base != nil {
				if bid, ok := sl.Base.(*ast.Ident); ok && !borrowedParam[bid.Name] {
					for _, arg := range SupersededFieldOwnMoveArgs(sl, bid.Name, c.ownFuncs) {
						selfMoveArgs[arg] = true
					}
				}
			}
			recordExprUses(x.Value, moved)
		case *ast.If:
			recordExprUses(x.Cond, moved)
			thenMoved := cloneMoved(moved)
			walkStmt(x.Then, thenMoved)
			elseMoved := cloneMoved(moved)
			if x.Else != nil {
				walkStmt(x.Else, elseMoved)
			}
			joinInto(moved, thenMoved, stmtDiverges(x.Then))
			joinInto(moved, elseMoved, x.Else != nil && stmtDiverges(x.Else))
		case *ast.While:
			recordExprUses(x.Cond, moved)
			loopBody(x.Body, nil, moved)
		case *ast.Loop:
			loopBody(x.Body, nil, moved)
		case *ast.For:
			if x.Init != nil {
				walkStmt(x.Init, moved)
			}
			recordExprUses(x.Cond, moved)
			loopBody(x.Body, x.Step, moved)
		case *ast.Match:
			scrutOwned := c.isOwnedScrutinee(x.Tag, isOwnedExpr)
			if id, ok := x.Tag.(*ast.Ident); ok && !ast.MatchTakesPointerPayload(x.Arms) {
				prev, had := moved[id.Name]
				recordExprUses(x.Tag, moved)
				if had {
					moved[id.Name] = prev
				} else {
					delete(moved, id.Name)
				}
			} else {
				recordExprUses(x.Tag, moved)
			}
			for _, arm := range x.Arms {
				armMoved := cloneMoved(moved)
				added, lent := bindArm(arm.Bindings, arm.BindingTypes, scrutOwned)
				if arm.Body != nil {
					walkStmts(arm.Body.Stmts, armMoved)
				}
				unbindArm(added, lent, armMoved)
				joinInto(moved, armMoved, arm.Body != nil && blockDiverges(arm.Body))
			}
		default:
			ast.Walk(st, func(n ast.Node) bool {
				if fd, ok := n.(*ast.FuncDecl); ok {
					walkNested(fd.Params, fd.Body, moved)
					return false
				}
				if e, ok := n.(ast.Expr); ok {
					recordExprUses(e, moved)
					return false
				}
				return true
			})
		}
	}
	walkStmts = func(stmts []ast.Stmt, moved movedSet) {
		for _, st := range stmts {
			walkStmt(st, moved)
		}
	}

	walkStmts(fn.Body.Stmts, movedSet{})
}

func (c *checker) checkBlock(b *ast.Block, parent *scope) {
	s := newScope(parent)
	prevMutualRec := c.mutualRecSiblings
	var localFns []*ast.FuncDecl
	for _, st := range b.Stmts {
		fn, ok := st.(*ast.FuncDecl)
		if !ok || !fn.IsLocal {
			continue
		}
		localFns = append(localFns, fn)
	}
	if len(localFns) > 1 {
		c.mutualRecSiblings = detectMutualRecSCCs(localFns)
		for _, fn := range localFns {
			if c.mutualRecSiblings[fn.Name] {
				s.set(fn.Name, paramSig(fn.Params, fn.ReturnType))
			}
		}
	} else {
		c.mutualRecSiblings = nil
	}
	for i, st := range b.Stmts {
		b.Stmts[i] = c.checkStmtLoweringForEach(st, s)
	}
	c.mutualRecSiblings = prevMutualRec
}

func (c *checker) checkBlockExpr(n *ast.BlockExpr, parent *scope) ast.Type {
	s := newScope(parent)
	prevMutualRec := c.mutualRecSiblings
	c.mutualRecSiblings = nil
	for i, st := range n.Stmts {
		n.Stmts[i] = c.checkStmtLoweringForEach(st, s)
	}
	c.mutualRecSiblings = prevMutualRec
	if n.Tail == nil {
		if stmtsDiverge(n.Stmts) {
			return ast.NeverType{}
		}
		c.errfCode(n.P, "E061", "block-expression has no trailing value (its last element is a `;`-terminated statement); a value is required here — drop the trailing `;` to make the final expression the block's value")
		return ast.VoidType{}
	}
	return c.checkExpr(n.Tail, s)
}

func stmtsDiverge(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtDiverges(stmts[len(stmts)-1])
}

func detectMutualRecSCCs(fns []*ast.FuncDecl) map[string]bool {
	siblings := map[string]*ast.FuncDecl{}
	for _, fn := range fns {
		siblings[fn.Name] = fn
	}
	adj := map[string][]string{}
	for _, fn := range fns {
		seen := map[string]bool{}
		walkBodyForNames(fn.Body, fn.Name, siblings, seen)
		out := make([]string, 0, len(seen))
		for name := range seen {
			out = append(out, name)
		}
		adj[fn.Name] = out
	}
	index := 0
	indices := map[string]int{}
	lowlinks := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	out := map[string]bool{}
	var strongconnect func(name string)
	strongconnect = func(name string) {
		indices[name] = index
		lowlinks[name] = index
		index++
		stack = append(stack, name)
		onStack[name] = true
		for _, succ := range adj[name] {
			if _, ok := indices[succ]; !ok {
				strongconnect(succ)
				if lowlinks[succ] < lowlinks[name] {
					lowlinks[name] = lowlinks[succ]
				}
			} else if onStack[succ] {
				if indices[succ] < lowlinks[name] {
					lowlinks[name] = indices[succ]
				}
			}
		}
		if lowlinks[name] == indices[name] {
			var scc []string
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				scc = append(scc, top)
				if top == name {
					break
				}
			}
			if len(scc) >= 2 {
				for _, n := range scc {
					out[n] = true
				}
			}
		}
	}
	for _, fn := range fns {
		if _, ok := indices[fn.Name]; !ok {
			strongconnect(fn.Name)
		}
	}
	return out
}

func walkBodyForNames(b *ast.Block, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		walkStmtForNames(st, selfName, siblings, seen)
	}
}

func walkStmtForNames(s ast.Stmt, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	switch n := s.(type) {
	case *ast.Block:
		walkBodyForNames(n, selfName, siblings, seen)
	case *ast.If:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Then, selfName, siblings, seen)
		walkStmtForNames(n.Else, selfName, siblings, seen)
	case *ast.While:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.Loop:
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.For:
		walkStmtForNames(n.Init, selfName, siblings, seen)
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Step, selfName, siblings, seen)
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.Return:
		walkExprForNames(n.Value, selfName, siblings, seen)
	case *ast.Var:
		walkExprForNames(n.Init, selfName, siblings, seen)
	case *ast.Destructure:
		walkExprForNames(n.Init, selfName, siblings, seen)
	case *ast.ExprStmt:
		walkExprForNames(n.Expr, selfName, siblings, seen)
	case *ast.Match:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, arm := range n.Arms {
			if arm.Literal != nil {
				walkExprForNames(arm.Literal, selfName, siblings, seen)
			}
			walkExprForNames(arm.Guard, selfName, siblings, seen)
			walkBodyForNames(arm.Body, selfName, siblings, seen)
		}
	case *ast.Defer:
		walkExprForNames(n.Expr, selfName, siblings, seen)
	}
}

func walkExprForNames(e ast.Expr, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Ident:
		if n.Name != selfName {
			if _, ok := siblings[n.Name]; ok {
				seen[n.Name] = true
			}
		}
	case *ast.Binary:
		walkExprForNames(n.Left, selfName, siblings, seen)
		walkExprForNames(n.Right, selfName, siblings, seen)
	case *ast.Unary:
		walkExprForNames(n.Operand, selfName, siblings, seen)
	case *ast.CastExpr:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.DowncastExpr:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.SliceExpr:
		walkExprForNames(n.Source, selfName, siblings, seen)
		walkExprForNames(n.Low, selfName, siblings, seen)
		walkExprForNames(n.High, selfName, siblings, seen)
	case *ast.Call:
		walkExprForNames(n.Callee, selfName, siblings, seen)
		for _, a := range n.Args {
			walkExprForNames(a, selfName, siblings, seen)
		}
	case *ast.Index:
		walkExprForNames(n.Array, selfName, siblings, seen)
		walkExprForNames(n.Idx, selfName, siblings, seen)
	case *ast.ArrayLit:
		for _, el := range n.Elems {
			walkExprForNames(el, selfName, siblings, seen)
		}
	case *ast.Assign:
		walkExprForNames(n.Target, selfName, siblings, seen)
		walkExprForNames(n.Value, selfName, siblings, seen)
	case *ast.IfExpr:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkExprForNames(n.Then, selfName, siblings, seen)
		walkExprForNames(n.Else, selfName, siblings, seen)
	case *ast.TryOp:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.MatchExpr:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, arm := range n.Arms {
			if arm.Literal != nil {
				walkExprForNames(arm.Literal, selfName, siblings, seen)
			}
			walkExprForNames(arm.Guard, selfName, siblings, seen)
			walkExprForNames(arm.Body, selfName, siblings, seen)
		}
	case *ast.BlockExpr:
		for _, st := range n.Stmts {
			walkStmtForNames(st, selfName, siblings, seen)
		}
		walkExprForNames(n.Tail, selfName, siblings, seen)
	case *ast.StructLit:
		if n.Base != nil {
			walkExprForNames(n.Base, selfName, siblings, seen)
		}
		for _, f := range n.Fields {
			walkExprForNames(f.Value, selfName, siblings, seen)
		}
	case *ast.FieldAccess:
		walkExprForNames(n.Target, selfName, siblings, seen)
	case *ast.FString:
		for _, p := range n.Parts {
			walkExprForNames(p.Expr, selfName, siblings, seen)
		}
		walkExprForNames(n.Desugared, selfName, siblings, seen)
	case *ast.TupleLit:
		for _, el := range n.Elems {
			walkExprForNames(el, selfName, siblings, seen)
		}
	case *ast.MapLit:
		for _, ent := range n.Entries {
			walkExprForNames(ent.Key, selfName, siblings, seen)
			walkExprForNames(ent.Value, selfName, siblings, seen)
		}
	case *ast.Lambda:
		walkBodyForNames(n.Body, selfName, siblings, seen)
	}
}

func (c *checker) labelInScope(label string) bool {
	for _, l := range c.loopLabels {
		if l == label {
			return true
		}
	}
	return false
}

func (c *checker) checkStmt(st ast.Stmt, s *scope) {
	switch n := st.(type) {
	case *ast.Block:
		c.checkBlock(n, s)
	case *ast.If:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errfCode(n.Cond.Pos(), "E008", "if condition must be boolean, got %s", t)
		}
		n.Then = c.checkStmtLoweringForEach(n.Then, s)
		if n.Else != nil {
			n.Else = c.checkStmtLoweringForEach(n.Else, s)
		}
	case *ast.While:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errfCode(n.Cond.Pos(), "E008", "while condition must be boolean, got %s", t)
		}
		c.loopDepth++
		if n.Label != "" {
			c.loopLabels = append(c.loopLabels, n.Label)
		}
		n.Body = c.checkStmtLoweringForEach(n.Body, s)
		if n.Label != "" {
			c.loopLabels = c.loopLabels[:len(c.loopLabels)-1]
		}
		c.loopDepth--
	case *ast.Loop:
		c.loopDepth++
		if n.Label != "" {
			c.loopLabels = append(c.loopLabels, n.Label)
		}
		n.Body = c.checkStmtLoweringForEach(n.Body, s)
		if n.Label != "" {
			c.loopLabels = c.loopLabels[:len(c.loopLabels)-1]
		}
		c.loopDepth--
	case *ast.For:
		inner := newScope(s)
		if n.Init != nil {
			c.checkStmt(n.Init, inner)
		}
		ct := c.checkExpr(n.Cond, inner)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errfCode(n.Cond.Pos(), "E008", "for condition must be boolean, got %s", ct)
		}
		c.loopDepth++
		if n.Label != "" {
			c.loopLabels = append(c.loopLabels, n.Label)
		}
		n.Body = c.checkStmtLoweringForEach(n.Body, inner)
		if n.Step != nil {
			c.checkStmt(n.Step, inner)
		}
		if n.Label != "" {
			c.loopLabels = c.loopLabels[:len(c.loopLabels)-1]
		}
		c.loopDepth--
	case *ast.Break:
		if c.loopDepth == 0 {
			c.errfCode(n.P, "E011", "break outside of a loop")
		} else if n.Label != "" && !c.labelInScope(n.Label) {
			c.errfCode(n.P, "E058", "break label %q does not match any enclosing loop", n.Label)
		}
	case *ast.Continue:
		if c.loopDepth == 0 {
			c.errfCode(n.P, "E011", "continue outside of a loop")
		} else if n.Label != "" && !c.labelInScope(n.Label) {
			c.errfCode(n.P, "E058", "continue label %q does not match any enclosing loop", n.Label)
		}
	case *ast.Return:
		want := c.current.ReturnType
		if c.inferReturns != nil && c.current.ReturnUnannotated {
			if n.Value == nil {
				*c.inferReturns = append(*c.inferReturns, nil)
				return
			}
			got := c.checkExpr(n.Value, s)
			got = c.postSettleType(n.Value, got)
			*c.inferReturns = append(*c.inferReturns, got)
			return
		}
		if n.Value == nil {
			if !ast.Equal(want, ast.VoidType{}) {
				c.errfCode(n.P, "E012", "return without value in function returning %s", want)
			}
			return
		}
		c.setElemHintFor(n.Value, want)
		c.expectedType = want
		got := c.checkExpr(n.Value, s)
		c.expectedType = nil
		c.elemHint = nil
		c.settleNumeric(n.Value, want)
		got = c.postSettleType(n.Value, got)
		got = c.maybeWrapForUnion(want, &n.Value, got, s)
		c.refineCallTypeArgsFromDest(n.Value, want)
		if got != nil && !c.assignable(want, got) {
			c.errfCode(n.P, "E002", "return value: expected %s, got %s%s", want, got, assignHint(want, got))
		}
	case *ast.Defer:
		if pos, found := firstTryOp(n.Expr); found {
			kind := "a `defer`"
			if n.OnError {
				kind = "an `errdefer`"
			}
			c.errfCode(pos, "E079", "`?` is not allowed inside %s action: it propagates a failure to the caller, and a deferred action has no caller to propagate to", kind)
			prevInfer := c.inferReturns
			c.inferReturns = nil
			defer func() { c.inferReturns = prevInfer }()
		}
		if blk, ok := n.Expr.(*ast.BlockExpr); ok {
			bs := newScope(s)
			prevMutualRec := c.mutualRecSiblings
			c.mutualRecSiblings = nil
			for i, st := range blk.Stmts {
				blk.Stmts[i] = c.checkStmtLoweringForEach(st, bs)
			}
			c.mutualRecSiblings = prevMutualRec
			if blk.Tail != nil {
				c.checkExpr(blk.Tail, bs)
			}
		} else {
			c.checkExpr(n.Expr, s)
		}
	case *ast.Var:
		if _, dup := s.names[n.Name]; dup {
			err := c.report(c.currentModule(), n.P, "E013", fmt.Sprintf("variable %q already declared in this scope", n.Name))
			if prior := s.vars[n.Name]; prior != nil && err != nil {
				err.Related = []diag.Label{{Pos: prior.P, Message: "previous declaration is here", Kind: diag.LabelSecondary}}
			}
		}
		unannotated := n.Type == nil
		c.setElemHintFor(n.Init, n.Type)
		c.expectedType = n.Type
		got := c.checkExpr(n.Init, s)
		c.expectedType = nil
		c.elemHint = nil
		if n.Type != nil {
			c.settleNumeric(n.Init, n.Type)
			got = c.postSettleType(n.Init, got)
			c.stampStructTypeArgs(n.Init, n.Type)
			c.refineCallTypeArgsFromDest(n.Init, n.Type)
		}
		if n.Type == nil {
			if got == nil {
				s.bindVar(n.Name, nil, n)
				return
			}
			if at, ok := got.(ast.ArrayType); ok && at.Elem == nil {
				c.errfCode(n.P, "E020", "empty array literal needs a type annotation")
				s.bindVar(n.Name, nil, n)
				return
			}
			if call, ok := n.Init.(*ast.Call); ok {
				if widened := c.widenGenericCallByLiterals(call); widened != nil {
					got = widened
				}
			}
			litLocal := false
			if gn, ok := got.(ast.NumberType); ok && gn.Polymorphic {
				if def := c.polymorphicIntDefault(n.Init); def.Width == 64 {
					c.settleInt(n.Init, def)
					got = def
				} else {
					litLocal = true
				}
			} else if holdsUntypedFloat(got) {
				c.promoteIntLiterals(n.Init, got)
			} else if widened := c.widenCompositeByLiterals(got, n.Init); widened != nil {
				c.settleNumeric(n.Init, widened)
				got = widened
			}
			n.Type = got
			if litLocal {
				s.bindVar(n.Name, n.Type, n)
				c.info.VarTypes[n] = n.Type
				c.info.Locals[c.current] = append(c.info.Locals[c.current], n)
				c.beginLitLocal(n, s)
				return
			}
		} else if got != nil {
			if isArrayViewLend(n.Type, got) {
				c.lendArrayAsView(&n.Init, n.Type, got, false)
				got = n.Type
			}
			got = c.maybeWrapForUnion(n.Type, &n.Init, got, s)
			if got != nil && !c.assignable(n.Type, got) {
				c.errfCode(n.P, "E003", "variable %q: expected %s, got %s%s", n.Name, n.Type, got, assignHint(n.Type, got))
			}
		}
		s.bindVar(n.Name, n.Type, n)
		c.info.VarTypes[n] = n.Type
		c.info.Locals[c.current] = append(c.info.Locals[c.current], n)
		if st, ok := n.Type.(ast.StructType); ok && unannotated {
			c.beginLitCompositeLocal(n, st.Args, s)
		}
		if et, ok := n.Type.(ast.EnumType); ok && unannotated {
			c.beginLitCompositeLocal(n, et.Args, s)
		}
	case *ast.Destructure:
		got := c.checkExpr(n.Init, s)
		if n.Fields != nil {
			c.checkStructDestructure(n, got, s)
			return
		}
		tup, ok := got.(ast.TupleType)
		if !ok {
			if got != nil {
				c.errfCode(n.P, "E024", "tuple destructure needs a tuple expression, got %s", got)
			}
			s.poisonAll(n.Names)
			return
		}
		if len(tup.Elems) != len(n.Names) {
			c.errfCode(n.P, "E024", "tuple has %d elements, but %d names given", len(tup.Elems), len(n.Names))
			s.poisonAll(n.Names)
			return
		}
		tempName := c.destructHolderName(n, s)
		tempVar := &ast.Var{P: n.P, Name: tempName, Type: tup}
		s.set(tempName, tup)
		c.info.VarTypes[tempVar] = tup
		c.info.Locals[c.current] = append(c.info.Locals[c.current], tempVar)
		for i, name := range n.Names {
			if _, dup := s.names[name]; dup {
				c.errfCode(n.P, "E013", "variable %q already declared in this scope", name)
				continue
			}
			elemT := tup.Elems[i]
			v := &ast.Var{P: n.P, Name: name, Type: elemT}
			s.set(name, elemT)
			c.info.VarTypes[v] = elemT
			c.info.Locals[c.current] = append(c.info.Locals[c.current], v)
		}
		for i := range n.Nested {
			if n.Nested[i] != nil {
				c.checkStmt(n.Nested[i], s)
			}
		}
	case *ast.ExprStmt:
		c.checkExpr(n.Expr, s)
		c.checkUnusedCollectionResult(n.Expr)
	case *ast.Match:
		c.checkMatch(n, s)
	case *ast.FuncDecl:
		c.checkLocalFunc(n, s)
	}
}

func (c *checker) destructHolderName(n *ast.Destructure, s *scope) string {
	if n.AtName == "" {
		n.TempName = c.destructTempName(n.P)
		return n.TempName
	}
	if _, dup := s.names[n.AtName]; dup {
		c.errfCode(n.P, "E013", "variable %q already declared in this scope", n.AtName)
	}
	n.TempName = n.AtName
	return n.AtName
}

func (c *checker) destructTempName(pos ast.Position) string {
	base := fmt.Sprintf("__destruct_%d_%d", pos.Line, pos.Col)
	name := base
	for n := 1; c.localDeclared(name); n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	return name
}

func (c *checker) localDeclared(name string) bool {
	for _, v := range c.info.Locals[c.current] {
		if v.Name == name {
			return true
		}
	}
	return false
}

func (c *checker) checkStructDestructure(n *ast.Destructure, got ast.Type, s *scope) {
	st, ok := got.(ast.StructType)
	if !ok {
		if got != nil {
			c.errfCode(n.P, "E024", "struct destructure needs a struct expression, got %s", got)
		}
		s.poisonAll(n.Names)
		return
	}
	sd := c.info.Structs[st.Name]
	if sd == nil {
		c.errfCode(n.P, "E043", "cannot find struct %q in this scope", demangle(st.Name))
		s.poisonAll(n.Names)
		return
	}
	if n.StructName != "" && n.StructName != st.Name {
		c.errfCode(n.P, "E024", "struct destructure pattern names %s, but the expression is %s", n.StructName, st.Name)
		s.poisonAll(n.Names)
		return
	}
	c.checkOpaqueAccess(sd, n.P, "destructure a field of")
	c.checkStructPatternCovers(n.P, sd, n.Fields, n.RestWritten)
	var sub map[string]ast.Type
	if len(sd.TypeParams) > 0 && len(st.Args) == len(sd.TypeParams) {
		sub = make(map[string]ast.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			sub[tp] = st.Args[i]
		}
	}
	tempName := c.destructHolderName(n, s)
	tempVar := &ast.Var{P: n.P, Name: tempName, Type: st}
	s.set(tempName, st)
	c.info.VarTypes[tempVar] = st
	c.info.Locals[c.current] = append(c.info.Locals[c.current], tempVar)
	for i, name := range n.Names {
		field := n.Fields[i]
		var ft ast.Type
		found := false
		for _, f := range sd.Fields {
			if f.Name == field {
				ft = f.Type
				if sub != nil {
					ft = substituteType(ft, sub)
				}
				found = true
				break
			}
		}
		if !found {
			declared := make([]string, 0, len(sd.Fields))
			for _, df := range sd.Fields {
				declared = append(declared, df.Name)
			}
			c.errUnknownField(n.P, n.P, st.Name, field, declared)
			s.poison(name)
			continue
		}
		if _, dup := s.names[name]; dup {
			c.errfCode(n.P, "E013", "variable %q already declared in this scope", name)
			continue
		}
		v := &ast.Var{P: n.P, Name: name, Type: ft}
		s.set(name, ft)
		c.info.VarTypes[v] = ft
		c.info.Locals[c.current] = append(c.info.Locals[c.current], v)
	}
}

func (c *checker) payloadsExhaustive(arms []structArmPat) bool {
	var types []ast.Type
	var rows [][]coverPat
	for _, a := range arms {
		for len(types) < len(a.types) {
			types = append(types, nil)
		}
		for k, t := range a.types {
			if types[k] == nil {
				types[k] = t
			}
		}
	}
	for _, a := range arms {
		if a.guarded {
			continue
		}
		row := make([]coverPat, len(types))
		for k := range row {
			if k < len(a.payloads) {
				row[k] = c.coverPatOf(a.payloads[k])
			}
		}
		rows = append(rows, row)
	}
	return !c.coverUseful(rows, types)
}

type coverPat struct {
	ctor     string
	opaque   bool
	args     []coverPat
	argTypes []ast.Type
}

func (c *checker) coverPatOf(el *ast.TuplePatElem) coverPat {
	switch {
	case el == nil:
		return coverPat{}
	case el.IsStruct:
		sd := c.info.Structs[el.VariantName]
		if sd == nil {
			return coverPat{opaque: true}
		}
		fields := structArmPatOf(sd, el.VariantBindings, el.VariantFieldNames, el.VariantPayloads, el.VariantBindingTypes, false)
		args := make([]coverPat, len(fields.payloads))
		for i, field := range fields.payloads {
			args[i] = c.coverPatOf(field)
		}
		return coverPat{ctor: "{}", args: args, argTypes: fields.types}
	case el.Literal != nil:
		if b, ok := el.Literal.(*ast.BoolLit); ok && el.RangeHi == nil {
			return coverPat{ctor: strconv.FormatBool(b.Value)}
		}
		return coverPat{opaque: true}
	case el.VariantName != "":
		args := make([]coverPat, len(el.VariantBindings))
		for k := range args {
			if k < len(el.VariantPayloads) {
				args[k] = c.coverPatOf(el.VariantPayloads[k])
			}
		}
		return coverPat{ctor: el.VariantName, args: args, argTypes: el.VariantBindingTypes}
	case el.Nested != nil:
		args := make([]coverPat, len(el.Nested))
		for k := range el.Nested {
			args[k] = c.coverPatOf(&el.Nested[k])
		}
		return coverPat{ctor: "()", args: args, argTypes: el.NestedTypes}
	}
	return coverPat{}
}

type coverCtor struct {
	name  string
	arity int
}

func (c *checker) coverCtors(t ast.Type) []coverCtor {
	switch tt := t.(type) {
	case ast.EnumType:
		ed, ok := c.info.Enums[tt.Name]
		if !ok {
			return nil
		}
		out := make([]coverCtor, len(ed.Variants))
		for i, v := range ed.Variants {
			out[i] = coverCtor{name: v.Name, arity: len(v.Payloads)}
		}
		return out
	case ast.TupleType:
		return []coverCtor{{name: "()", arity: len(tt.Elems)}}
	case ast.StructType:
		if sd := c.info.Structs[tt.Name]; sd != nil {
			return []coverCtor{{name: "{}", arity: len(sd.Fields)}}
		}
	case ast.BoolType:
		return []coverCtor{{name: "false"}, {name: "true"}}
	}
	return nil
}

func (c *checker) coverUseful(rows [][]coverPat, types []ast.Type) bool {
	if len(types) == 0 {
		return len(rows) == 0
	}
	ctors := c.coverCtors(types[0])
	named := map[string]bool{}
	for _, r := range rows {
		if r[0].ctor != "" && !r[0].opaque {
			named[r[0].ctor] = true
		}
	}
	complete := len(ctors) > 0
	for _, k := range ctors {
		if !named[k.name] {
			complete = false
		}
	}
	if !complete {
		var rest [][]coverPat
		for _, r := range rows {
			if r[0].ctor == "" && !r[0].opaque {
				rest = append(rest, r[1:])
			}
		}
		return c.coverUseful(rest, types[1:])
	}
	for _, k := range ctors {
		argTypes := make([]ast.Type, k.arity)
		var split [][]coverPat
		for _, r := range rows {
			if r[0].opaque || r[0].ctor != "" && r[0].ctor != k.name {
				continue
			}
			args := r[0].args
			if r[0].ctor == "" || len(args) != k.arity {
				args = make([]coverPat, k.arity)
			}
			for i := range argTypes {
				if argTypes[i] == nil && i < len(r[0].argTypes) {
					argTypes[i] = r[0].argTypes[i]
				}
			}
			split = append(split, append(append([]coverPat{}, args...), r[1:]...))
		}
		if c.coverUseful(split, append(argTypes, types[1:]...)) {
			return true
		}
	}
	return false
}

func structArmPatOf(sd *ast.StructDecl, bindings, fieldNames []string, payloads []*ast.TuplePatElem, types []ast.Type, guarded bool) structArmPat {
	out := structArmPat{payloads: make([]*ast.TuplePatElem, len(sd.Fields)), types: make([]ast.Type, len(sd.Fields)), guarded: guarded}
	for k, b := range bindings {
		field := b
		if k < len(fieldNames) && fieldNames[k] != "" {
			field = fieldNames[k]
		}
		for i, f := range sd.Fields {
			if f.Name != field {
				continue
			}
			if k < len(payloads) {
				out.payloads[i] = payloads[k]
			}
			if k < len(types) {
				out.types[i] = types[k]
			}
		}
	}
	return out
}

func tupleArmPat(elems []ast.TuplePatElem, guarded bool, tup ast.TupleType) structArmPat {
	payloads := make([]*ast.TuplePatElem, len(elems))
	for k := range elems {
		payloads[k] = &elems[k]
	}
	return structArmPat{payloads: payloads, types: tup.Elems, guarded: guarded}
}

type structArmPat struct {
	payloads []*ast.TuplePatElem
	types    []ast.Type
	guarded  bool
}

func armPayloadsRefutable(payloads []*ast.TuplePatElem) bool {
	for _, el := range payloads {
		if el != nil && patElemRefutable(*el) {
			return true
		}
	}
	return false
}

func patElemRefutable(el ast.TuplePatElem) bool {
	if el.IsStruct {
		for _, sub := range el.VariantPayloads {
			if sub != nil && patElemRefutable(*sub) {
				return true
			}
		}
		return false
	}
	if el.VariantName != "" || el.Literal != nil {
		return true
	}
	for _, sub := range el.Nested {
		if patElemRefutable(sub) {
			return true
		}
	}
	return false
}

func (c *checker) bindArmPayloads(pos ast.Position, bindings []string, types []ast.Type, payloads []*ast.TuplePatElem, s, armScope *scope) {
	seen := map[string]bool{}
	for k, name := range bindings {
		var bt ast.Type
		if k < len(types) {
			bt = types[k]
		}
		if k < len(payloads) && payloads[k] != nil {
			slot := []ast.TuplePatElem{*payloads[k]}
			st := []ast.Type{bt}
			c.checkTuplePatElem(pos, slot, st, 0, bt, s, armScope, seen)
			*payloads[k] = slot[0]
			continue
		}
		if name == "" || name == "_" {
			continue
		}
		seen[name] = true
		if _, unreachable := bt.(ast.NeverType); unreachable {
			armScope.poison(name)
			continue
		}
		armScope.set(name, bt)
	}
}

func scrutineeSub(ed *ast.EnumDecl, et ast.EnumType) map[string]ast.Type {
	sub := map[string]ast.Type{}
	if len(et.Args) != 0 && len(et.Args) != len(ed.TypeParams) {
		return sub
	}
	for i, p := range ed.TypeParams {
		sub[p] = ast.NeverType{}
		if len(et.Args) > 0 {
			if _, open := et.Args[i].(ast.UnboundType); !open {
				sub[p] = et.Args[i]
			}
		}
	}
	return sub
}

func (c *checker) resolveVariantBindings(pos ast.Position, ed *ast.EnumDecl, variant *ast.EnumVariant, bindings []string, sub map[string]ast.Type) ([]string, []ast.Type) {
	if len(bindings) != len(variant.Payloads) {
		c.errfCode(pos, "E015", "%s", payloadCountMsg(c.variantHintName(ed, variant.Name), len(variant.Payloads), len(bindings)))
	}
	outTypes := make([]ast.Type, len(bindings))
	for k := range bindings {
		if k < len(variant.Payloads) {
			outTypes[k] = substituteType(variant.Payloads[k], sub)
			if en, clash := c.payloadlessVariantNamed(bindings[k], outTypes[k]); clash {
				c.errPayloadlessVariantBinder(pos, bindings[k], en, "a payload slot")
			}
		}
	}
	return bindings, outTypes
}

func (c *checker) settleNamedVariantPattern(pos ast.Position, ed *ast.EnumDecl, variant *ast.EnumVariant, fields, bindings []string, payloads []*ast.TuplePatElem, rest bool) ([]string, []*ast.TuplePatElem) {
	if len(variant.FieldNames) == 0 {
		c.errfCode(pos, "E015", "variant %s has positional payloads; match it as %s(...), not %s { ... }",
			variant.Name, variant.Name, variant.Name)
		return bindings, payloads
	}
	outNames := make([]string, len(variant.FieldNames))
	var outPayloads []*ast.TuplePatElem
	bound := make([]bool, len(variant.FieldNames))
	for k, f := range fields {
		idx := slices.Index(variant.FieldNames, f)
		if idx < 0 {
			c.errfCode(pos, "E015", "variant %s has no field %q", c.variantHintName(ed, variant.Name), f)
			continue
		}
		if bound[idx] {
			c.errfCode(pos, "E015", "field %q bound more than once in pattern for %s", f, c.variantHintName(ed, variant.Name))
			continue
		}
		bound[idx] = true
		outNames[idx] = bindings[k]
		if k < len(payloads) && payloads[k] != nil {
			if outPayloads == nil {
				outPayloads = make([]*ast.TuplePatElem, len(variant.FieldNames))
			}
			outPayloads[idx] = payloads[k]
		}
	}
	omitted := false
	for i := range outNames {
		if !bound[i] {
			outNames[i] = "_"
			omitted = true
		}
	}
	if omitted && !rest {
		c.errMissingPatternFields(pos, variant.Name, variant.FieldNames)
	}
	return outNames, outPayloads
}

func (c *checker) errMissingPatternFields(pos ast.Position, name string, declared []string) {
	c.errfCode(pos, "E015", "named-field pattern for %s must bind all %d field(s) (%s), or end in `..` to leave the rest out",
		demangle(name), len(declared), strings.Join(declared, ", "))
}

func patternFields(bindings, fieldNames []string) []string {
	out := make([]string, len(bindings))
	for k, b := range bindings {
		out[k] = b
		if k < len(fieldNames) && fieldNames[k] != "" {
			out[k] = fieldNames[k]
		}
	}
	return out
}

func (c *checker) checkStructPatternCovers(pos ast.Position, sd *ast.StructDecl, fields []string, rest bool) {
	if rest {
		return
	}
	declared := make([]string, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		declared = append(declared, f.Name)
	}
	for _, f := range fields {
		if !slices.Contains(declared, f) {
			return
		}
	}
	if slices.ContainsFunc(declared, func(f string) bool { return !slices.Contains(fields, f) }) {
		c.errMissingPatternFields(pos, sd.Name, declared)
	}
}

func (c *checker) errPayloadlessVariantBinder(pos ast.Position, name, enum, where string) {
	c.errfCode(pos, "E015", "%s is a payload-less variant of enum %s, but a bare name in %s is a binder that matches every value — write `%s()` to match the variant, or rename the binder",
		name, enum, where, name)
}

func (c *checker) checkTuplePatElem(pos ast.Position, elems []ast.TuplePatElem, bindingTypes []ast.Type, k int, elT ast.Type, s, armScope *scope, seen map[string]bool) bool {
	el := &elems[k]
	bindingTypes[k] = elT
	if el.AtBinding != "" {
		if seen[el.AtBinding] {
			c.errfCode(pos, "E013", "variable %q already declared in this scope", el.AtBinding)
		} else {
			seen[el.AtBinding] = true
			armScope.names[el.AtBinding] = elT
		}
	}
	if el.Nested != nil {
		return c.checkTuplePatNestedElem(pos, el, k, elT, s, armScope, seen)
	}
	if el.VariantName != "" {
		if st, isStruct := elT.(ast.StructType); isStruct {
			return c.checkTuplePatStructElem(pos, el, k, st, s, armScope, seen)
		}
		c.checkTuplePatVariantElem(pos, el, k, elT, s, armScope, seen)
		return true
	}
	if el.Literal != nil {
		litT := c.checkExpr(el.Literal, s)
		if litT != nil {
			c.settleNumeric(el.Literal, elT)
			litT = c.postSettleType(el.Literal, litT)
			if !c.assignable(litT, elT) {
				c.errfCode(pos, "E035", "literal pattern of type %s does not match tuple element %d of type %s", litT, k, elT)
			}
		}
		return true
	}
	if el.IsWildcard {
		return false
	}
	if seen[el.Name] {
		c.errfCode(pos, "E013", "variable %q already declared in this scope", el.Name)
		return false
	}
	seen[el.Name] = true
	if en, clash := c.payloadlessVariantNamed(el.Name, elT); clash {
		c.errPayloadlessVariantBinder(pos, el.Name, en, "a tuple element")
	}
	armScope.set(el.Name, elT)
	return false
}

func (c *checker) checkTuplePatNestedElem(pos ast.Position, el *ast.TuplePatElem, k int, elT ast.Type, s, armScope *scope, seen map[string]bool) bool {
	tup, isTuple := elT.(ast.TupleType)
	if !isTuple {
		c.errfCode(pos, "E035", "nested tuple pattern on tuple element %d, but the element has type %s", k, elT)
		return true
	}
	if len(el.Nested) != len(tup.Elems) {
		c.errfCode(pos, "E035", "nested tuple pattern on element %d has %d elements, but that element is a tuple of %d", k, len(el.Nested), len(tup.Elems))
		return true
	}
	el.NestedTypes = make([]ast.Type, len(el.Nested))
	refutable := false
	for j := range el.Nested {
		if c.checkTuplePatElem(pos, el.Nested, el.NestedTypes, j, tup.Elems[j], s, armScope, seen) {
			refutable = true
		}
	}
	return refutable
}

func (c *checker) checkTuplePatStructElem(pos ast.Position, el *ast.TuplePatElem, k int, st ast.StructType, s, armScope *scope, seen map[string]bool) bool {
	if el.VariantFieldNames == nil {
		c.errfCode(pos, "E035", "struct pattern on element %d must name its fields — `%s { … }`", k, st.Name)
		return true
	}
	if el.VariantName != st.Name {
		c.errfCode(pos, "E035", "struct pattern names %s, but element %d has type %s", el.VariantName, k, st.Name)
		return true
	}
	sd, known := c.info.Structs[st.Name]
	if !known {
		c.errfCode(pos, "E043", "cannot find struct %q in this scope", demangle(st.Name))
		return true
	}
	var sub map[string]ast.Type
	if len(sd.TypeParams) > 0 && len(st.Args) == len(sd.TypeParams) {
		sub = make(map[string]ast.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			sub[tp] = st.Args[i]
		}
	}
	c.checkStructPatternCovers(pos, sd, patternFields(el.VariantBindings, el.VariantFieldNames), el.RestWritten)
	el.IsStruct = true
	if len(el.VariantFieldNames) > 0 {
		c.checkOpaqueAccess(sd, pos, "destructure")
	}
	el.VariantBindingTypes = make([]ast.Type, len(el.VariantBindings))
	refutable := false
	for i, b := range el.VariantBindings {
		field := b
		if i < len(el.VariantFieldNames) && el.VariantFieldNames[i] != "" {
			field = el.VariantFieldNames[i]
		}
		var ft ast.Type
		found := false
		for _, f := range sd.Fields {
			if f.Name == field {
				ft = f.Type
				if sub != nil {
					ft = substituteType(ft, sub)
				}
				found = true
				break
			}
		}
		if !found {
			declared := make([]string, 0, len(sd.Fields))
			for _, df := range sd.Fields {
				declared = append(declared, df.Name)
			}
			c.errUnknownField(pos, pos, st.Name, field, declared)
			refutable = true
			continue
		}
		el.VariantBindingTypes[i] = ft
		if i < len(el.VariantPayloads) && el.VariantPayloads[i] != nil {
			slot := []ast.TuplePatElem{*el.VariantPayloads[i]}
			types := []ast.Type{ft}
			if c.checkTuplePatElem(pos, slot, types, 0, ft, s, armScope, seen) {
				refutable = true
			}
			*el.VariantPayloads[i] = slot[0]
			continue
		}
		if b == "" {
			continue
		}
		if seen[b] {
			c.errfCode(pos, "E013", "variable %q already declared in this scope", b)
			continue
		}
		seen[b] = true
		armScope.set(b, ft)
	}
	return refutable
}

func (c *checker) checkTuplePatVariantElem(pos ast.Position, el *ast.TuplePatElem, k int, elT ast.Type, s, armScope *scope, seen map[string]bool) {
	et, isEnum := elT.(ast.EnumType)
	if !isEnum {
		c.errfCode(pos, "E035", "variant pattern on tuple element %d, but the element has type %s", k, elT)
		return
	}
	ed, known := c.info.Enums[et.Name]
	if !known {
		c.errfCode(pos, "E043", "unknown enum type %q", et.Name)
		return
	}
	badQualifier := c.checkVariantQualifier(pos, el.VariantModule, ed)
	var variant *ast.EnumVariant
	for j := range ed.Variants {
		if ed.Variants[j].Name == el.VariantName {
			variant = &ed.Variants[j]
			break
		}
	}
	if variant == nil {
		if !badQualifier {
			c.errfCode(pos, "E014", "variant %q is not part of enum %s%s",
				c.variantHintName(ed, el.VariantName), c.enumHintName(ed.Name), c.variantSpellingHint(el.VariantName, ed))
		}
		return
	}
	if el.VariantFieldNames != nil {
		el.VariantBindings, el.VariantPayloads = c.settleNamedVariantPattern(pos, ed, variant, el.VariantFieldNames, el.VariantBindings, el.VariantPayloads, el.RestWritten)
		el.VariantFieldNames = slices.Clone(variant.FieldNames)
	}
	el.VariantBindings, el.VariantBindingTypes = c.resolveVariantBindings(pos, ed, variant, el.VariantBindings, scrutineeSub(ed, et))
	for i, name := range el.VariantBindings {
		if i < len(el.VariantPayloads) && el.VariantPayloads[i] != nil {
			slot := []ast.TuplePatElem{*el.VariantPayloads[i]}
			types := []ast.Type{el.VariantBindingTypes[i]}
			c.checkTuplePatElem(pos, slot, types, 0, el.VariantBindingTypes[i], s, armScope, seen)
			*el.VariantPayloads[i] = slot[0]
			continue
		}
		if name == "" || name == "_" {
			continue
		}
		if seen[name] {
			c.errfCode(pos, "E013", "variable %q already declared in this scope", name)
			continue
		}
		seen[name] = true
		armScope.set(name, el.VariantBindingTypes[i])
	}
}

func (c *checker) payloadlessVariantNamed(name string, t ast.Type) (string, bool) {
	et, ok := t.(ast.EnumType)
	if !ok {
		return "", false
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		return "", false
	}
	for i := range ed.Variants {
		if ed.Variants[i].Name == name && len(ed.Variants[i].Payloads) == 0 {
			return ed.Name, true
		}
	}
	return "", false
}

func originLabel(origin string) string {
	if origin == ast.OriginLetElse {
		return "let-else"
	}
	return "if-let"
}

func syntheticElseArm(n *ast.Match, i int) bool {
	return n.Origin != "" && i == len(n.Arms)-1 && n.Arms[i].IsWildcard
}

func bindsVariantPattern(n *ast.Match) bool {
	for i, arm := range n.Arms {
		if syntheticElseArm(n, i) {
			continue
		}
		if arm.VariantName == "" || arm.NamedFields {
			return false
		}
	}
	return true
}

func stampRemainderArm[A matchArmLike](arms []A, ed *ast.EnumDecl) {
	for _, a := range arms {
		a.setCoversRemainder(false)
	}
	if ed == nil || len(arms) == 0 {
		return
	}
	last := arms[len(arms)-1]
	if last.isWildcard() || last.enumName() == "" || last.guard() != nil ||
		armPayloadsRefutable(last.payloads()) {
		return
	}
	covered := map[string]bool{}
	for _, a := range arms[:len(arms)-1] {
		if a.isWildcard() {
			return
		}
		if a.guard() == nil && !armPayloadsRefutable(a.payloads()) {
			covered[a.variantName()] = true
		}
	}
	for _, v := range ed.Variants {
		if v.Name == last.variantName() {
			if covered[v.Name] {
				return
			}
			continue
		}
		if !covered[v.Name] {
			return
		}
	}
	last.setCoversRemainder(true)
}

type matchArmLike interface {
	isWildcard() bool
	enumName() string
	variantName() string
	guard() ast.Expr
	payloads() []*ast.TuplePatElem
	setCoversRemainder(bool)
}

type stmtArm struct{ a *ast.MatchArm }

func (x stmtArm) isWildcard() bool              { return x.a.IsWildcard }
func (x stmtArm) enumName() string              { return x.a.EnumName }
func (x stmtArm) variantName() string           { return x.a.VariantName }
func (x stmtArm) guard() ast.Expr               { return x.a.Guard }
func (x stmtArm) payloads() []*ast.TuplePatElem { return x.a.Payloads }
func (x stmtArm) setCoversRemainder(v bool)     { x.a.CoversRemainder = v }

type exprArm struct{ a *ast.MatchExprArm }

func (x exprArm) isWildcard() bool              { return x.a.IsWildcard }
func (x exprArm) enumName() string              { return x.a.EnumName }
func (x exprArm) variantName() string           { return x.a.VariantName }
func (x exprArm) guard() ast.Expr               { return x.a.Guard }
func (x exprArm) payloads() []*ast.TuplePatElem { return x.a.Payloads }
func (x exprArm) setCoversRemainder(v bool)     { x.a.CoversRemainder = v }

func stampRemainderStmtArms(arms []*ast.MatchArm, ed *ast.EnumDecl) {
	wrapped := make([]matchArmLike, 0, len(arms))
	for _, a := range arms {
		wrapped = append(wrapped, stmtArm{a})
	}
	stampRemainderArm(wrapped, ed)
}

func stampRemainderExprArms(arms []*ast.MatchExprArm, ed *ast.EnumDecl) {
	wrapped := make([]matchArmLike, 0, len(arms))
	for _, a := range arms {
		wrapped = append(wrapped, exprArm{a})
	}
	stampRemainderArm(wrapped, ed)
}

func (c *checker) poisonedMatchScope(s *scope, binders []string, guard ast.Expr) *scope {
	armScope := newScope(s)
	for _, name := range binders {
		armScope.poison(name)
	}
	if guard != nil {
		gt := c.checkExpr(guard, armScope)
		if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
			c.errfCode(guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
		}
	}
	return armScope
}

func (c *checker) checkMatch(n *ast.Match, s *scope) {
	if n.Origin == ast.OriginLetElse && len(n.Arms) >= 2 {
		if els := n.Arms[len(n.Arms)-1].Body; !blockDiverges(els) {
			c.errfCode(els.P, "E022", "let-else: else branch must diverge (return / break / continue)")
		}
	}
	tagT := c.checkExpr(n.Tag, s)
	if tagT == nil {
		for _, arm := range n.Arms {
			c.checkBlock(arm.Body, c.poisonedMatchScope(s, arm.Binders(), arm.Guard))
		}
		return
	}
	tagT = c.widenGenericScrutinee(n.Tag, tagT)
	et, ok := tagT.(ast.EnumType)
	if !ok {
		if n.Origin != "" && bindsVariantPattern(n) {
			c.errfCode(n.Tag.Pos(), "E022", "%s source must be an enum value, got %s", originLabel(n.Origin), tagT)
			for _, arm := range n.Arms {
				c.checkBlock(arm.Body, s)
			}
			return
		}
		if tup, isTup := tagT.(ast.TupleType); isTup {
			c.checkTupleMatch(n, tup, s)
			return
		}
		if st, isStruct := tagT.(ast.StructType); isStruct {
			if _, known := c.info.Structs[st.Name]; known {
				c.checkStructMatch(n, st, s)
				return
			}
		}
		c.checkLiteralMatch(n, tagT, s)
		return
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		c.errfCode(n.Tag.Pos(), "E023", "unknown enum %q", et.Name)
		return
	}
	sub := scrutineeSub(ed, et)
	covered := map[string]bool{}
	literalLocals := c.enumLiteralLocals(n.Tag, et, s)
	refutableByVariant := map[string][]structArmPat{}
	sawWildcard := false
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.TupleElems != nil {
			c.errfCode(arm.P, "E035", "tuple pattern requires a tuple scrutinee, got enum %s", ed.Name)
			c.checkBlock(arm.Body, s)
			continue
		}
		badQualifier := c.checkVariantQualifier(arm.P, arm.VariantModule, ed)
		varIdx := -1
		var variant *ast.EnumVariant
		for j := range ed.Variants {
			if ed.Variants[j].Name == arm.VariantName {
				varIdx = j
				variant = &ed.Variants[j]
				break
			}
		}
		if varIdx < 0 {
			if !badQualifier {
				c.errfCode(arm.P, "E014", "variant %q is not part of enum %s%s",
					c.variantHintName(ed, arm.VariantName), c.enumHintName(ed.Name), c.unknownVariantHint(arm.VariantName, ed))
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		arm.EnumName = ed.Name
		arm.VariantIndex = varIdx
		if covered[arm.VariantName] {
			c.errfCode(arm.P, "E028", "variant %q already covered earlier in this match", c.variantHintName(ed, arm.VariantName))
		}
		if arm.NamedFields {
			arm.Bindings, arm.Payloads = c.settleNamedVariantPattern(arm.P, ed, variant, arm.FieldNames, arm.Bindings, arm.Payloads, arm.RestWritten)
			arm.FieldNames = slices.Clone(variant.FieldNames)
		}
		arm.Bindings, arm.BindingTypes = c.resolveVariantBindings(arm.P, ed, variant, arm.Bindings, sub)
		refutableByVariant[arm.VariantName] = append(refutableByVariant[arm.VariantName],
			structArmPat{payloads: arm.Payloads, types: arm.BindingTypes, guarded: arm.Guard != nil})
		armScope := newScope(s)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, et)
		}
		c.bindArmPayloads(arm.P, arm.Bindings, arm.BindingTypes, arm.Payloads, s, armScope)
		c.bindEnumLiteralPayloads(n.Tag, ed, variant, arm.Bindings, arm.BindingTypes, armScope, literalLocals)
		if arm.Guard == nil && !armPayloadsRefutable(arm.Payloads) {
			covered[arm.VariantName] = true
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawWildcard {
		for name, group := range refutableByVariant {
			if !covered[name] && c.payloadsExhaustive(group) {
				covered[name] = true
			}
		}
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errfCode(n.P, "E030", "match does not cover variant %s of enum %s — add an arm for it, or a `_` arm",
					c.variantHintName(ed, v.Name), c.enumHintName(ed.Name))
			}
		}
	}
	stampRemainderStmtArms(n.Arms, ed)
}

func (c *checker) widenGenericScrutinee(tag ast.Expr, tagT ast.Type) ast.Type {
	if call, ok := tag.(*ast.Call); ok {
		if widened := c.widenGenericCallByLiterals(call); widened != nil {
			return widened
		}
	}
	return tagT
}

func (c *checker) settlePolymorphicScrutinee(tag ast.Expr, tagT ast.Type, pats []ast.Expr) ast.Type {
	if nt, ok := tagT.(ast.NumberType); !ok || !nt.Polymorphic {
		return tagT
	}
	def := c.polymorphicIntDefault(append([]ast.Expr{tag}, pats...)...)
	c.settleInt(tag, def)
	return def
}

func literalPatterns(arms []*ast.MatchArm) []ast.Expr {
	var out []ast.Expr
	for _, arm := range arms {
		if arm.Literal != nil {
			out = append(out, arm.Literal)
		}
		if arm.RangeHi != nil {
			out = append(out, arm.RangeHi)
		}
	}
	return out
}

func literalPatternExprs(arms []*ast.MatchExprArm) []ast.Expr {
	var out []ast.Expr
	for _, arm := range arms {
		if arm == nil {
			continue
		}
		if arm.Literal != nil {
			out = append(out, arm.Literal)
		}
		if arm.RangeHi != nil {
			out = append(out, arm.RangeHi)
		}
	}
	return out
}

func (c *checker) checkLiteralMatch(n *ast.Match, tagT ast.Type, s *scope) {
	tagT = c.settlePolymorphicScrutinee(n.Tag, tagT, literalPatterns(n.Arms))
	sawWildcard := false
	var closing []ast.Expr
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.Literal == nil {
			c.errfCode(arm.P, "E035", "match on non-enum value `%s` only accepts literal patterns or `_`", tagT)
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.Guard == nil && arm.RangeHi == nil && arm.AtBinding == "" {
			closing = append(closing, arm.Literal)
		}
		litT := c.checkExpr(arm.Literal, s)
		if litT != nil {
			c.settleNumeric(arm.Literal, tagT)
			litT = c.postSettleType(arm.Literal, litT)
			if !c.assignable(litT, tagT) {
				c.errfCode(arm.P, "E035", "literal pattern of type %s does not match scrutinee type %s", litT, tagT)
			}
		}
		if arm.RangeHi != nil {
			tagNum, tagIsNum := tagT.(ast.NumberType)
			_, tagIsFloat := tagT.(ast.FloatType)
			if !tagIsNum && !tagIsFloat {
				c.errfCode(arm.P, "E035", "range patterns require a numeric scrutinee, got %s", tagT)
			} else if tagIsNum && !tagNum.IsSigned() {
				c.errfCode(arm.P, "E035", "range patterns are not yet supported on an unsigned scrutinee (%s)", tagT)
			}
			hiT := c.checkExpr(arm.RangeHi, s)
			if hiT != nil {
				c.settleNumeric(arm.RangeHi, tagT)
				hiT = c.postSettleType(arm.RangeHi, hiT)
				if !c.assignable(hiT, tagT) {
					c.errfCode(arm.P, "E035", "range pattern bound of type %s does not match scrutinee type %s", hiT, tagT)
				}
			}
		}
		armScope := newScope(s)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, tagT)
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawWildcard && !closesBool(tagT, closing) {
		c.errfCode(n.P, "E030", "%s", literalMatchNotExhaustive(tagT))
	}
}

func closesBool(tagT ast.Type, lits []ast.Expr) bool {
	if _, ok := tagT.(ast.BoolType); !ok {
		return false
	}
	seen := map[bool]bool{}
	for _, l := range lits {
		if b, ok := l.(*ast.BoolLit); ok {
			seen[b.Value] = true
		}
	}
	return seen[true] && seen[false]
}

func literalMatchNotExhaustive(tagT ast.Type) string {
	if _, ok := tagT.(ast.BoolType); ok {
		return "match on boolean is not exhaustive — add the missing `true` or `false` arm, or an unguarded `_`"
	}
	return "match on non-enum value is not exhaustive — add an unguarded `_` arm"
}

func (c *checker) checkTupleMatch(n *ast.Match, tup ast.TupleType, s *scope) {
	sawIrrefutable := false
	var tupleArms []structArmPat
	for i, arm := range n.Arms {
		if sawIrrefutable && !syntheticElseArm(n, i) {
			c.errfCode(arm.P, "E026", "arm is unreachable — a preceding arm matches every value")
		}
		if arm.IsWildcard {
			if arm.Guard == nil {
				sawIrrefutable = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.TupleElems == nil {
			c.errfCode(arm.P, "E035", "match on tuple `%s` only accepts tuple patterns or `_`", tup)
			c.checkBlock(arm.Body, s)
			continue
		}
		if len(arm.TupleElems) != len(tup.Elems) {
			c.errfCode(arm.P, "E035", "tuple pattern has %d elements, but scrutinee tuple has %d", len(arm.TupleElems), len(tup.Elems))
			c.checkBlock(arm.Body, s)
			continue
		}
		armScope := newScope(s)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, tup)
		}
		irrefutable := true
		arm.BindingTypes = make([]ast.Type, len(arm.TupleElems))
		seen := map[string]bool{}
		for k := range arm.TupleElems {
			if c.checkTuplePatElem(arm.P, arm.TupleElems, arm.BindingTypes, k, tup.Elems[k], s, armScope, seen) {
				irrefutable = false
			}
		}
		tupleArms = append(tupleArms, tupleArmPat(arm.TupleElems, arm.Guard != nil, tup))
		if arm.Guard != nil {
			irrefutable = false
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		if irrefutable {
			sawIrrefutable = true
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawIrrefutable && !c.payloadsExhaustive(tupleArms) {
		c.errfCode(n.P, "E030", "match on tuple is not exhaustive — add an unguarded `_` or all-binder arm")
	}
}

func (c *checker) checkStructMatch(n *ast.Match, st ast.StructType, s *scope) {
	sd := c.info.Structs[st.Name]
	if sd == nil {
		c.errfCode(n.Tag.Pos(), "E043", "cannot find struct %q in this scope", demangle(st.Name))
		return
	}
	n.StructMatch = st.Name
	var sub map[string]ast.Type
	if len(sd.TypeParams) > 0 && len(st.Args) == len(sd.TypeParams) {
		sub = make(map[string]ast.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			sub[tp] = st.Args[i]
		}
	}
	sawIrrefutable := false
	var structArms []structArmPat
	for i, arm := range n.Arms {
		if sawIrrefutable && !syntheticElseArm(n, i) {
			c.errfCode(arm.P, "E026", "arm is unreachable — a preceding arm matches every value")
		}
		if arm.IsWildcard {
			if arm.Guard == nil {
				sawIrrefutable = true
			} else {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		if !arm.NamedFields {
			c.errfCode(arm.P, "E035", "match on struct `%s` only accepts struct patterns `%s { … }` or `_`", st.Name, st.Name)
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.VariantName != st.Name {
			c.errfCode(arm.P, "E035", "struct pattern names %s, but the scrutinee is %s", arm.VariantName, st.Name)
			c.checkBlock(arm.Body, s)
			continue
		}
		armScope := newScope(s)
		if len(arm.Bindings) > 0 {
			c.checkOpaqueAccess(sd, arm.P, "destructure")
		}
		c.checkStructPatternCovers(arm.P, sd, patternFields(arm.Bindings, arm.FieldNames), arm.RestWritten)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, st)
		}
		arm.BindingTypes = make([]ast.Type, len(arm.Bindings))
		seen := map[string]bool{}
		for k, b := range arm.Bindings {
			field := b
			if k < len(arm.FieldNames) && arm.FieldNames[k] != "" {
				field = arm.FieldNames[k]
			}
			var ft ast.Type
			found := false
			for _, f := range sd.Fields {
				if f.Name == field {
					ft = f.Type
					if sub != nil {
						ft = substituteType(ft, sub)
					}
					found = true
					break
				}
			}
			if !found {
				declared := make([]string, 0, len(sd.Fields))
				for _, df := range sd.Fields {
					declared = append(declared, df.Name)
				}
				c.errUnknownField(arm.P, arm.P, st.Name, field, declared)
				continue
			}
			arm.BindingTypes[k] = ft
			if k < len(arm.Payloads) && arm.Payloads[k] != nil {
				slot := []ast.TuplePatElem{*arm.Payloads[k]}
				st := []ast.Type{ft}
				c.checkTuplePatElem(arm.P, slot, st, 0, ft, s, armScope, seen)
				*arm.Payloads[k] = slot[0]
				continue
			}
			if seen[b] {
				c.errfCode(arm.P, "E013", "variable %q already declared in this scope", b)
				continue
			}
			seen[b] = true
			armScope.set(b, ft)
		}
		structArms = append(structArms, structArmPatOf(sd, arm.Bindings, arm.FieldNames, arm.Payloads, arm.BindingTypes, arm.Guard != nil))
		irrefutable := !armPayloadsRefutable(arm.Payloads)
		if arm.Guard != nil {
			irrefutable = false
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		if irrefutable {
			sawIrrefutable = true
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawIrrefutable && !c.payloadsExhaustive(structArms) {
		c.errfCode(n.P, "E030", "match on struct is not exhaustive — add an unguarded struct-pattern or `_` arm")
	}
}

func (c *checker) checkLiteralMatchExpr(n *ast.MatchExpr, tagT ast.Type, s *scope) ast.Type {
	tagT = c.settlePolymorphicScrutinee(n.Tag, tagT, literalPatternExprs(n.Arms))
	sawWildcard := false
	var closing []ast.Expr
	var result ast.Type
	unify := func(armT ast.Type, p ast.Position) {
		if armT == nil {
			return
		}
		if result == nil {
			result = armT
			return
		}
		if _, ok := result.(ast.NeverType); ok {
			result = armT
			return
		}
		if _, ok := armT.(ast.NeverType); ok {
			return
		}
		if unified := unifyIfArms(result, armT); unified != nil {
			result = unified
			return
		}
		if c.assignable(armT, result) {
			return
		}
		if c.assignable(result, armT) {
			result = armT
			return
		}
		c.errfCode(p, "E031", "match arms have incompatible types: %s vs %s", result, armT)
	}
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.P)
			continue
		}
		if arm.Literal == nil {
			c.errfCode(arm.P, "E035", "match on non-enum value `%s` only accepts literal patterns or `_`", tagT)
			continue
		}
		if arm.Guard == nil && arm.RangeHi == nil && arm.AtBinding == "" {
			closing = append(closing, arm.Literal)
		}
		litT := c.checkExpr(arm.Literal, s)
		if litT != nil {
			c.settleNumeric(arm.Literal, tagT)
			litT = c.postSettleType(arm.Literal, litT)
			if !c.assignable(litT, tagT) {
				c.errfCode(arm.P, "E035", "literal pattern of type %s does not match scrutinee type %s", litT, tagT)
			}
		}
		if arm.RangeHi != nil {
			tagNum, tagIsNum := tagT.(ast.NumberType)
			_, tagIsFloat := tagT.(ast.FloatType)
			if !tagIsNum && !tagIsFloat {
				c.errfCode(arm.P, "E035", "range patterns require a numeric scrutinee, got %s", tagT)
			} else if tagIsNum && !tagNum.IsSigned() {
				c.errfCode(arm.P, "E035", "range patterns are not yet supported on an unsigned scrutinee (%s)", tagT)
			}
			hiT := c.checkExpr(arm.RangeHi, s)
			if hiT != nil {
				c.settleNumeric(arm.RangeHi, tagT)
				hiT = c.postSettleType(arm.RangeHi, hiT)
				if !c.assignable(hiT, tagT) {
					c.errfCode(arm.P, "E035", "range pattern bound of type %s does not match scrutinee type %s", hiT, tagT)
				}
			}
		}
		armScope := newScope(s)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, tagT)
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		unify(c.checkExpr(arm.Body, armScope), arm.P)
	}
	if !sawWildcard && !closesBool(tagT, closing) {
		c.errfCode(n.P, "E030", "%s", literalMatchNotExhaustive(tagT))
	}
	return result
}

func (c *checker) checkTupleMatchExpr(n *ast.MatchExpr, tup ast.TupleType, s *scope) ast.Type {
	sawIrrefutable := false
	var tupleArms []structArmPat
	var result ast.Type
	unify := func(armT ast.Type, p ast.Position) {
		if armT == nil {
			return
		}
		if result == nil {
			result = armT
			return
		}
		if _, ok := result.(ast.NeverType); ok {
			result = armT
			return
		}
		if _, ok := armT.(ast.NeverType); ok {
			return
		}
		if unified := unifyIfArms(result, armT); unified != nil {
			result = unified
			return
		}
		if c.assignable(armT, result) {
			return
		}
		if c.assignable(result, armT) {
			result = armT
			return
		}
		c.errfCode(p, "E031", "match arms have incompatible types: %s vs %s", result, armT)
	}
	for _, arm := range n.Arms {
		if sawIrrefutable {
			c.errfCode(arm.P, "E026", "arm is unreachable — a preceding arm matches every value")
		}
		if arm.IsWildcard {
			if arm.Guard == nil {
				sawIrrefutable = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.P)
			continue
		}
		if arm.TupleElems == nil {
			c.errfCode(arm.P, "E035", "match on tuple `%s` only accepts tuple patterns or `_`", tup)
			continue
		}
		if len(arm.TupleElems) != len(tup.Elems) {
			c.errfCode(arm.P, "E035", "tuple pattern has %d elements, but scrutinee tuple has %d", len(arm.TupleElems), len(tup.Elems))
			continue
		}
		armScope := newScope(s)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, tup)
		}
		irrefutable := true
		arm.BindingTypes = make([]ast.Type, len(arm.TupleElems))
		seen := map[string]bool{}
		for k := range arm.TupleElems {
			if c.checkTuplePatElem(arm.P, arm.TupleElems, arm.BindingTypes, k, tup.Elems[k], s, armScope, seen) {
				irrefutable = false
			}
		}
		tupleArms = append(tupleArms, tupleArmPat(arm.TupleElems, arm.Guard != nil, tup))
		if arm.Guard != nil {
			irrefutable = false
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		if irrefutable {
			sawIrrefutable = true
		}
		unify(c.checkExpr(arm.Body, armScope), arm.P)
	}
	if !sawIrrefutable && !c.payloadsExhaustive(tupleArms) {
		c.errfCode(n.P, "E030", "match on tuple is not exhaustive — add an unguarded `_` or all-binder arm")
	}
	return result
}

func (c *checker) checkStructMatchExpr(n *ast.MatchExpr, st ast.StructType, s *scope) ast.Type {
	sd := c.info.Structs[st.Name]
	if sd == nil {
		c.errfCode(n.Tag.Pos(), "E043", "cannot find struct %q in this scope", demangle(st.Name))
		return nil
	}
	n.StructMatch = st.Name
	var sub map[string]ast.Type
	if len(sd.TypeParams) > 0 && len(st.Args) == len(sd.TypeParams) {
		sub = make(map[string]ast.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			sub[tp] = st.Args[i]
		}
	}
	sawIrrefutable := false
	var structArms []structArmPat
	var result ast.Type
	unify := func(armT ast.Type, p ast.Position) {
		if armT == nil {
			return
		}
		if result == nil {
			result = armT
			return
		}
		if unified := unifyIfArms(result, armT); unified != nil {
			result = unified
			return
		}
		if !ast.Equal(result, armT) {
			if c.assignable(armT, result) {
				return
			}
			if c.assignable(result, armT) {
				result = armT
				return
			}
			c.errfCode(p, "E031", "match arms have incompatible types: %s vs %s", result, armT)
		}
	}
	for _, arm := range n.Arms {
		if sawIrrefutable {
			c.errfCode(arm.P, "E026", "arm is unreachable — a preceding arm matches every value")
		}
		if arm.IsWildcard {
			if arm.Guard == nil {
				sawIrrefutable = true
			} else {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.P)
			continue
		}
		if !arm.NamedFields {
			c.errfCode(arm.P, "E035", "match on struct `%s` only accepts struct patterns `%s { … }` or `_`", st.Name, st.Name)
			continue
		}
		if arm.VariantName != st.Name {
			c.errfCode(arm.P, "E035", "struct pattern names %s, but the scrutinee is %s", arm.VariantName, st.Name)
			continue
		}
		armScope := newScope(s)
		if len(arm.Bindings) > 0 {
			c.checkOpaqueAccess(sd, arm.P, "destructure")
		}
		c.checkStructPatternCovers(arm.P, sd, patternFields(arm.Bindings, arm.FieldNames), arm.RestWritten)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, st)
		}
		arm.BindingTypes = make([]ast.Type, len(arm.Bindings))
		seen := map[string]bool{}
		for k, b := range arm.Bindings {
			field := b
			if k < len(arm.FieldNames) && arm.FieldNames[k] != "" {
				field = arm.FieldNames[k]
			}
			var ft ast.Type
			found := false
			for _, f := range sd.Fields {
				if f.Name == field {
					ft = f.Type
					if sub != nil {
						ft = substituteType(ft, sub)
					}
					found = true
					break
				}
			}
			if !found {
				declared := make([]string, 0, len(sd.Fields))
				for _, df := range sd.Fields {
					declared = append(declared, df.Name)
				}
				c.errUnknownField(arm.P, arm.P, st.Name, field, declared)
				continue
			}
			arm.BindingTypes[k] = ft
			if k < len(arm.Payloads) && arm.Payloads[k] != nil {
				slot := []ast.TuplePatElem{*arm.Payloads[k]}
				st := []ast.Type{ft}
				c.checkTuplePatElem(arm.P, slot, st, 0, ft, s, armScope, seen)
				*arm.Payloads[k] = slot[0]
				continue
			}
			if seen[b] {
				c.errfCode(arm.P, "E013", "variable %q already declared in this scope", b)
				continue
			}
			seen[b] = true
			armScope.set(b, ft)
		}
		structArms = append(structArms, structArmPatOf(sd, arm.Bindings, arm.FieldNames, arm.Payloads, arm.BindingTypes, arm.Guard != nil))
		irrefutable := !armPayloadsRefutable(arm.Payloads)
		if arm.Guard != nil {
			irrefutable = false
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		if irrefutable {
			sawIrrefutable = true
		}
		unify(c.checkExpr(arm.Body, armScope), arm.P)
	}
	if !sawIrrefutable && !c.payloadsExhaustive(structArms) {
		c.errfCode(n.P, "E030", "match on struct is not exhaustive — add an unguarded struct-pattern or `_` arm")
	}
	if isFloat(result) {
		n.IsFloat = true
	}
	return result
}

func (c *checker) checkMatchExpr(n *ast.MatchExpr, s *scope) ast.Type {
	tagT := c.checkExpr(n.Tag, s)
	if tagT == nil {
		for _, arm := range n.Arms {
			c.checkExpr(arm.Body, c.poisonedMatchScope(s, arm.Binders(), arm.Guard))
		}
		return nil
	}
	tagT = c.widenGenericScrutinee(n.Tag, tagT)
	et, ok := tagT.(ast.EnumType)
	if !ok {
		if tup, isTup := tagT.(ast.TupleType); isTup {
			return c.checkTupleMatchExpr(n, tup, s)
		}
		if st, isStruct := tagT.(ast.StructType); isStruct {
			if _, known := c.info.Structs[st.Name]; known {
				return c.checkStructMatchExpr(n, st, s)
			}
		}
		return c.checkLiteralMatchExpr(n, tagT, s)
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		c.errfCode(n.Tag.Pos(), "E023", "unknown enum %q", et.Name)
		return nil
	}
	sub := scrutineeSub(ed, et)
	covered := map[string]bool{}
	literalLocals := c.enumLiteralLocals(n.Tag, et, s)
	refutableByVariant := map[string][]structArmPat{}
	sawWildcard := false
	var result ast.Type
	unify := func(armT ast.Type, p ast.Position) {
		if armT == nil {
			return
		}
		if result == nil {
			result = armT
			return
		}
		if unified := unifyIfArms(result, armT); unified != nil {
			result = unified
			return
		}
		if !ast.Equal(result, armT) {
			c.errfCode(p, "E031", "match-expression arms differ: %s vs %s", result, armT)
		}
	}
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.Body.Pos())
			continue
		}
		if arm.TupleElems != nil {
			c.errfCode(arm.P, "E035", "tuple pattern requires a tuple scrutinee, got enum %s", ed.Name)
			continue
		}
		badQualifier := c.checkVariantQualifier(arm.P, arm.VariantModule, ed)
		varIdx := -1
		var variant *ast.EnumVariant
		for j := range ed.Variants {
			if ed.Variants[j].Name == arm.VariantName {
				varIdx = j
				variant = &ed.Variants[j]
				break
			}
		}
		if varIdx < 0 {
			if !badQualifier {
				c.errfCode(arm.P, "E014", "variant %q is not part of enum %s%s",
					c.variantHintName(ed, arm.VariantName), c.enumHintName(ed.Name), c.unknownVariantHint(arm.VariantName, ed))
			}
			unify(c.checkExpr(arm.Body, s), arm.Body.Pos())
			continue
		}
		arm.EnumName = ed.Name
		arm.VariantIndex = varIdx
		if covered[arm.VariantName] {
			c.errfCode(arm.P, "E028", "variant %q already covered earlier in this match", c.variantHintName(ed, arm.VariantName))
		}
		if arm.NamedFields {
			arm.Bindings, arm.Payloads = c.settleNamedVariantPattern(arm.P, ed, variant, arm.FieldNames, arm.Bindings, arm.Payloads, arm.RestWritten)
			arm.FieldNames = slices.Clone(variant.FieldNames)
		}
		arm.Bindings, arm.BindingTypes = c.resolveVariantBindings(arm.P, ed, variant, arm.Bindings, sub)
		refutableByVariant[arm.VariantName] = append(refutableByVariant[arm.VariantName],
			structArmPat{payloads: arm.Payloads, types: arm.BindingTypes, guarded: arm.Guard != nil})
		armScope := newScope(s)
		if arm.AtBinding != "" {
			armScope.set(arm.AtBinding, et)
		}
		c.bindArmPayloads(arm.P, arm.Bindings, arm.BindingTypes, arm.Payloads, s, armScope)
		c.bindEnumLiteralPayloads(n.Tag, ed, variant, arm.Bindings, arm.BindingTypes, armScope, literalLocals)
		if arm.Guard == nil && !armPayloadsRefutable(arm.Payloads) {
			covered[arm.VariantName] = true
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		unify(c.checkExpr(arm.Body, armScope), arm.Body.Pos())
	}
	if !sawWildcard {
		for name, group := range refutableByVariant {
			if !covered[name] && c.payloadsExhaustive(group) {
				covered[name] = true
			}
		}
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errfCode(n.P, "E030", "match does not cover variant %s of enum %s — add an arm for it, or a `_` arm",
					c.variantHintName(ed, v.Name), c.enumHintName(ed.Name))
			}
		}
	}
	stampRemainderExprArms(n.Arms, ed)
	if isFloat(result) {
		n.IsFloat = true
	}
	return result
}

func (c *checker) inferUseParam(fn *ast.FuncDecl, outer *scope) {
	if len(fn.Params) == 0 || fn.UseSource == nil {
		return
	}
	untyped := fn.Params[0].Type == nil
	cbSig := c.useCallbackSlot(fn, outer, untyped)
	if cbSig == nil {
		return
	}
	if untyped {
		if containsParamType(cbSig.Params[0]) {
			id := fn.UseSource.Callee.(*ast.Ident)
			c.errfCode(fn.P, "E032", "use: could not infer binding type for %q from its arguments — add an explicit `: TYPE` annotation", id.Name)
			return
		}
		fn.Params[0].Type = cbSig.Params[0]
	}
	if cbSig.Result != nil && !containsParamType(cbSig.Result) {
		fn.ReturnType = cbSig.Result
	}
}

func (c *checker) useCallbackSlot(fn *ast.FuncDecl, outer *scope, report bool) *ast.FuncType {
	src := fn.UseSource
	id, ok := src.Callee.(*ast.Ident)
	if !ok {
		if report {
			c.errfCode(fn.P, "E032", "use: cannot infer binding type for non-identifier source — add an explicit `: TYPE` annotation")
		}
		return nil
	}
	var sig *ast.FuncType
	if t, isValue := c.identValueBinding(id.Name, outer); isValue {
		sig, _ = t.(*ast.FuncType)
	} else if f, visible := c.visibleFuncSig(id.Name); visible {
		sig = f
	}
	if sig == nil || len(sig.Params) == 0 {
		if report {
			c.errfCode(fn.P, "E032", "use: callee %q has no signature; add an explicit `: TYPE` annotation", id.Name)
		}
		return nil
	}
	last := sig.Params[len(sig.Params)-1]
	cbSig, isFunc := last.(*ast.FuncType)
	if !isFunc {
		if report {
			c.errfCode(fn.P, "E032", "use: callee %q's last parameter isn't a function — add an explicit `: TYPE` annotation", id.Name)
		}
		return nil
	}
	if len(cbSig.Params) == 0 {
		if report {
			c.errfCode(fn.P, "E032", "use: callee %q's callback takes no arguments — there's nothing to bind", id.Name)
		}
		return nil
	}
	if _, isGen := c.info.GenericFuncs[id.Name]; !isGen {
		return cbSig
	}
	sub := map[string]ast.Type{}
	for i, arg := range src.Args {
		if i >= len(sig.Params)-1 {
			break
		}
		argType := c.checkExpr(arg, outer)
		if argType == nil {
			continue
		}
		c.unifyType(sig.Params[i], argType, sub)
	}
	resolved, _ := substituteType(cbSig, sub).(*ast.FuncType)
	if resolved == nil {
		return cbSig
	}
	return resolved
}

func (c *checker) calleeIsGenericFunc(id *ast.Ident, s *scope) bool {
	if _, isGen := c.info.GenericFuncs[id.Name]; !isGen || !c.funcVisibleHere(id.Name) {
		return false
	}
	_, bound := c.identValueBinding(id.Name, s)
	return !bound
}

func (c *checker) retagIndexAsTypeArgs(n *ast.Call, s *scope) {
	ix, ok := n.Callee.(*ast.Index)
	if !ok || len(n.TypeArgs) > 0 {
		return
	}
	fn, ok := ix.Array.(*ast.Ident)
	if !ok || !c.calleeIsGenericFunc(fn, s) {
		return
	}
	if gen := c.info.GenericFuncs[fn.Name]; len(gen.TypeParams) > 0 && slices.Contains(gen.ConstParams, gen.TypeParams[0]) {
		n.Callee = fn
		n.TypeArgs = []ast.Type{c.indexAsConstArg(ix.Idx, s)}
		n.TypeArgsWritten = true
		return
	}
	params := c.typeParamsInScope()
	t, ok := c.indexAsType(ix.Idx, params, s)
	if !ok {
		return
	}
	c.resolveType(&t, params, ix.Idx.Pos())
	n.Callee = fn
	n.TypeArgs = []ast.Type{t}
	n.TypeArgsWritten = true
}

func (c *checker) indexAsConstArg(e ast.Expr, s *scope) ast.Type {
	switch x := e.(type) {
	case *ast.NumberLit:
		if isConstArgLit(x) {
			return constArgOf(x)
		}
	case *ast.Ident:
		if c.isConstParam(x.Name, s) {
			return ast.ParamType{Name: x.Name}
		}
		if _, bound := c.identValueBinding(x.Name, s); !bound {
			t := ast.Type(ast.StructType{Name: x.Name})
			c.resolveType(&t, c.typeParamsInScope(), x.P)
			return t
		}
	}
	return ast.StructType{}
}

func isConstArgLit(n *ast.NumberLit) bool {
	return n.Width == 0 && !n.IsUnsigned && !n.IsFloat
}

func constArgOf(n *ast.NumberLit) ast.ConstArg {
	if n.ExceedsI64 || n.ExceedsU64 {
		return ast.ConstArg{Value: math.MaxInt64}
	}
	return ast.ConstArg{Value: n.Value}
}

func (c *checker) isConstParam(name string, s *scope) bool {
	if b := s.binder(name); b != nil {
		return b.consts[name]
	}
	for i := len(c.captureChain) - 1; i >= 0; i-- {
		if ent := c.captureChain[i]; ent.scope != nil {
			if b := ent.scope.binder(name); b != nil {
				return b.consts[name]
			}
		}
	}
	return false
}

func (c *checker) constArgDiags(fn *ast.FuncDecl, written []ast.Type, pos ast.Position, s *scope) {
	for i, ta := range written {
		if i >= len(fn.TypeParams) {
			return
		}
		tp := fn.TypeParams[i]
		_, isInt := ta.(ast.ConstArg)
		if !slices.Contains(fn.ConstParams, tp) {
			if isInt {
				c.errfCode(pos, "E090", "type parameter %s takes a type, not the integer %s", tp, ta)
			}
			continue
		}
		if ca, ok := ta.(ast.ConstArg); ok {
			if ca.Value > math.MaxInt32 {
				c.errfCode(pos, "E089", "the const argument for %s must fit in i32", tp)
			}
			continue
		}
		if pt, ok := ta.(ast.ParamType); ok && c.isConstParam(pt.Name, s) {
			continue
		}
		if st, ok := ta.(ast.StructType); ok && len(st.Args) == 0 {
			if _, bound := c.identValueBinding(st.Name, s); st.Name == "" || bound {
				c.errfCode(pos, "E089", "the const argument for %s must be an integer literal or a const parameter", tp)
				continue
			}
		}
		c.errfCode(pos, "E090", "const parameter %s takes an integer, not the type %s", tp, ta)
	}
}

func (c *checker) indexAsType(e ast.Expr, params map[string]bool, s *scope) (ast.Type, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if _, bound := c.identValueBinding(x.Name, s); bound {
			return nil, false
		}
		_, isStruct := c.info.Structs[x.Name]
		_, isEnum := c.info.Enums[x.Name]
		_, isResource := c.info.Resources[x.Name]
		if !params[x.Name] && !isStruct && !isEnum && !isResource {
			return nil, false
		}
		return ast.StructType{Name: x.Name}, true
	case *ast.Index:
		head, ok := c.indexAsType(x.Array, params, s)
		if !ok {
			return nil, false
		}
		arg, ok := c.indexAsType(x.Idx, params, s)
		if !ok {
			return nil, false
		}
		st := head.(ast.StructType)
		return ast.StructType{Name: st.Name, Args: append(st.Args, arg)}, true
	}
	return nil, false
}

func (c *checker) instantiateFuncValue(n *ast.Ident, gf *ast.FuncDecl) (ast.Type, bool) {
	exp, ok := c.expectedType.(*ast.FuncType)
	if !ok {
		return nil, false
	}
	sig, ok := c.info.FuncSigs[n.Name]
	if !ok || len(sig.Params) != len(exp.Params) {
		return nil, false
	}
	sub := map[string]ast.Type{}
	for i := range sig.Params {
		c.unifyType(sig.Params[i], exp.Params[i], sub)
	}
	c.unifyType(sig.Result, exp.Result, sub)
	args := make([]ast.Type, len(gf.TypeParams))
	for i, tp := range gf.TypeParams {
		t, bound := sub[tp]
		if !bound {
			return nil, false
		}
		args[i] = t
	}
	c.checkTypeArgBounds(gf, args, sub, c.typeParamsInScope(), n.P)
	n.TypeArgs = args
	return substituteType(sig, sub), true
}

func (c *checker) errE040GenericFuncAsValue(p ast.Position, name string, fn *ast.FuncDecl) {
	tps := strings.Join(fn.TypeParams, ", ")
	plural := ""
	if len(fn.TypeParams) > 1 {
		plural = "s"
	}
	params := make([]string, 0, len(fn.Params))
	args := make([]string, 0, len(fn.Params))
	for _, pa := range fn.Params {
		params = append(params, fmt.Sprintf("%s: %s", pa.Name, pa.Type))
		args = append(args, pa.Name)
	}
	c.errfCode(p, "E040", "generic function %s cannot be used as a value — nothing here determines its type parameter%s %s. Wrap it in a lambda, which does: (%s) => %s(%s)",
		name, plural, tps, strings.Join(params, ", "), name, strings.Join(args, ", "))
}

func (c *checker) typeParamsInScope() map[string]bool {
	var set map[string]bool
	add := func(d *ast.FuncDecl) {
		if d == nil {
			return
		}
		for _, tp := range d.TypeParams {
			if set == nil {
				set = map[string]bool{}
			}
			set[tp] = true
		}
	}
	add(c.current)
	for _, e := range c.captureChain {
		add(e.outer)
	}
	return set
}

func (c *checker) errE040StructUninferred(p ast.Position, tp, name string) {
	c.errfCode(p, "E040", "could not infer type parameter %s for struct %s — supply it explicitly at the construction site (e.g. %s[i32] { ... }) or annotate the binding (e.g. let x: %s[i32] = ...)",
		tp, name, name, name)
}

func callSiteName(fn *ast.FuncDecl) (display, spelling string) {
	if fn.MethodRecv == "" || fn.MethodSimpleName == "" {
		return fn.Name, fn.Name
	}
	display = demangle(fn.MethodRecv) + "." + fn.MethodSimpleName
	if strings.HasPrefix(fn.Name, "__assoc_") {
		return display, display
	}
	return display, "." + fn.MethodSimpleName
}

func boundParams(sub map[string]ast.Type) map[string]bool {
	out := make(map[string]bool, len(sub))
	for name := range sub {
		out[name] = true
	}
	return out
}

func containsParamType(t ast.Type) bool {
	_, found := paramTypeNotIn(t, nil)
	return found
}

func paramTypeNotIn(t ast.Type, inScope map[string]bool) (string, bool) {
	switch x := t.(type) {
	case ast.ParamType:
		if !inScope[x.Name] {
			return x.Name, true
		}
	case ast.ArrayType:
		return paramTypeNotIn(x.Elem, inScope)
	case ast.SliceType:
		return paramTypeNotIn(x.Elem, inScope)
	case ast.TupleType:
		for _, e := range x.Elems {
			if name, found := paramTypeNotIn(e, inScope); found {
				return name, true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if name, found := paramTypeNotIn(a, inScope); found {
				return name, true
			}
		}
	case ast.StructType:
		for _, a := range x.Args {
			if name, found := paramTypeNotIn(a, inScope); found {
				return name, true
			}
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			if name, found := paramTypeNotIn(p, inScope); found {
				return name, true
			}
		}
		return paramTypeNotIn(x.Result, inScope)
	}
	return "", false
}

func (c *checker) checkLocalFunc(fn *ast.FuncDecl, outer *scope) {
	if fn.UseSource != nil {
		c.inferUseParam(fn, outer)
	}
	sig := paramSig(fn.Params, fn.ReturnType)
	outer.set(fn.Name, sig)

	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errfCode(fn.P, "E018", "duplicate parameter %q", p.Name)
		}
		root.set(p.Name, p.Type)
	}

	captured := map[string]ast.Type{}
	var captureOrder []string

	prev := c.current
	prevSink := c.captureSink
	prevOuter := c.captureOuter
	prevLoop := c.loopDepth
	if prev != nil && fn.SourceModule == "" {
		fn.SourceModule = prev.SourceModule
	}
	c.current = fn
	c.loopDepth = 0
	mySiblings := c.mutualRecSiblings
	c.captureSink = func(name string, t ast.Type) {
		if _, ok := captured[name]; ok {
			return
		}
		if name == fn.Name {
			return
		}
		if mySiblings[name] {
			return
		}
		switch t.(type) {
		case ast.VoidType:
			c.errfCode(fn.P, "E044", "captured variable %q has unsupported type %s", name, t)
		default:
			c.captureView(fn.P, name, t)
			captured[name] = t
			captureOrder = append(captureOrder, name)
		}
	}
	c.captureOuter = outer
	c.captureChain = append(c.captureChain, captureEntry{sink: c.captureSink, scope: outer, outer: prev})
	defer func() {
		c.current = prev
		c.captureSink = prevSink
		c.captureOuter = prevOuter
		c.captureChain = c.captureChain[:len(c.captureChain)-1]
		c.loopDepth = prevLoop
	}()

	c.checkBlock(fn.Body, root)

	fn.Captures = nil
	for _, name := range captureOrder {
		fn.Captures = append(fn.Captures, ast.Param{Name: name, Type: captured[name]})
	}
	c.restampLitCaptures(fn.Captures, outer)
}

func (c *checker) constType(cd *ast.ConstDecl) ast.Type {
	if t, ok := c.constTypes[cd.Name]; ok {
		return t
	}
	if cd.Type != nil {
		c.constTypes[cd.Name] = cd.Type
		return cd.Type
	}
	if c.constBusy[cd.Name] {
		c.errfCode(cd.P, "E001", "const %s is defined in terms of itself", cd.Name)
		return nil
	}
	c.constBusy[cd.Name] = true
	t := c.checkExpr(cd.Value, newScope(nil))
	delete(c.constBusy, cd.Name)
	c.constTypes[cd.Name] = t
	return t
}

func (c *checker) checkExpr(e ast.Expr, s *scope) ast.Type {
	switch n := e.(type) {
	case *ast.NumberLit:
		if (n.ExceedsI64 || n.ExceedsU64) && !c.wideSeen[n] {
			if c.wideSeen == nil {
				c.wideSeen = map[*ast.NumberLit]bool{}
			}
			c.wideSeen[n] = true
			c.wideLits = append(c.wideLits, wideLit{lit: n, path: c.currentModule()})
		}
		if n.IsFloat {
			return ast.FloatType{Width: n.FloatWidth}
		}
		if n.Width != 0 {
			t := ast.NumberType{Width: n.Width, Signed: !n.IsUnsigned}
			c.checkLiteralFits(n, t, c.negatedLits[n])
			return t
		}
		return ast.NumberType{Polymorphic: true}
	case *ast.DowncastExpr:
		inner := c.checkExpr(n.Inner, s)
		if st, isBareStruct := n.Target.(ast.StructType); isBareStruct && len(st.Args) == 0 {
			if _, isEnum := c.info.Enums[st.Name]; isEnum {
				n.Target = ast.EnumType{Name: st.Name}
			}
		}
		if src, ok := inner.(ast.NumberType); ok {
			dst, ok := n.Target.(ast.NumberType)
			if !ok {
				c.errfCode(n.P, "E060", "'as?' from %s converts only to an integer type, got %s", inner, n.Target)
				return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
			}
			if src.Polymorphic {
				src = ast.NumberType{Width: 64, Signed: true}
				c.settleNumeric(n.Inner, src)
			}
			n.Lowered = c.buildCheckedCast(n, src, dst, s)
			return ast.EnumType{Name: "Option", Args: []ast.Type{dst}}
		}
		dt, ok := inner.(ast.DynTraitType)
		if !ok {
			c.errfCode(n.P, "E059", "'as?' requires a 'dyn Trait' value or an integer on the left, got %s", inner)
			return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
		}
		n.Trait = dt.Trait0()
		n.Traits = dt.Traits
		tn, hasName := methodTypeName(n.Target)
		_, isStruct := n.Target.(ast.StructType)
		_, isEnum := n.Target.(ast.EnumType)
		if !hasName || !(isStruct || isEnum) {
			c.errfCode(n.P, "E060", "'as?' downcast target must be a concrete struct or enum type (slice 1), got %s", n.Target)
			return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
		}
		if !c.implementsAllDynTraits(dt, tn) {
			c.errfCode(n.P, "E060", "%s does not implement %s, so a '%s' cannot downcast to it", n.Target, c.missingDynTraits(dt, n.Target), dt.String())
		}
		return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
	case *ast.CastExpr:
		inner := c.checkExpr(n.Inner, s)
		if c.holdsPendingLitLocal(n.Inner) {
		} else if ft, innerIsFloat := inner.(ast.FloatType); innerIsFloat {
			if _, tgtIsInt := n.Target.(ast.NumberType); tgtIsInt || !ft.Polymorphic {
				floatHint := ast.FloatType(ft)
				if ft.Polymorphic {
					floatHint = ast.FloatType{Width: 64}
				}
				c.settleNumeric(n.Inner, floatHint)
			} else {
				c.settleNumeric(n.Inner, n.Target)
			}
		} else if _, innerIsInt := inner.(ast.NumberType); innerIsInt && isFloat(n.Target) && !isBareNumericLiteral(n.Inner) {
			c.settleNumeric(n.Inner, c.castOperandInt(inner, n.Inner))
		} else if _, tgtIsChar := n.Target.(ast.CharType); tgtIsChar {
			c.settleNumeric(n.Inner, c.castOperandInt(inner, n.Inner))
		} else if nt, tgtNum := n.Target.(ast.NumberType); tgtNum &&
			nt.NormalWidth() > 0 && nt.NormalWidth() < 32 &&
			!isBareNumericLiteral(n.Inner) {
			c.settleNumeric(n.Inner, c.castOperandInt(inner, n.Inner))
		} else {
			c.settleNumeric(n.Inner, n.Target)
		}
		inner = c.postSettleType(n.Inner, inner)
		n.InnerType = inner
		_, innerIsNum := inner.(ast.NumberType)
		_, innerIsFloat := inner.(ast.FloatType)
		_, targetIsNum := n.Target.(ast.NumberType)
		_, targetIsFloat := n.Target.(ast.FloatType)
		if (innerIsNum || innerIsFloat) && (targetIsNum || targetIsFloat) {
			return n.Target
		}
		if _, tgtIsChar := n.Target.(ast.CharType); tgtIsChar && innerIsNum {
			if lit, ok := n.Inner.(*ast.NumberLit); ok && !validScalarValue(lit.Value) {
				c.errfCode(n.P, "E071", "%d is not a Unicode scalar value, so it cannot be cast to `char`: the range is 0..0x10FFFF excluding the surrogates 0xD800..0xDFFF%s — use `utf8.char_from_i32` for a value that is only known at run time", lit.Value, surrogateHint(lit.Value))
			}
			return n.Target
		}
		if _, innerIsChar := inner.(ast.CharType); innerIsChar && targetIsNum {
			return n.Target
		}
		if nt, ok := n.Target.(ast.NumberType); ok && (nt.NormalWidth() == 32 || nt.IsPointerWidth()) {
			switch inner.(type) {
			case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
				return n.Target
			}
		}
		if nt, ok := inner.(ast.NumberType); ok && (nt.NormalWidth() == 32 || nt.IsPointerWidth()) {
			switch n.Target.(type) {
			case ast.ArrayType, ast.StringType, ast.StructType:
				if nt.NormalWidth() == 32 && !nt.IsPointerWidth() {
					c.errfCode(n.P, "E069", "reinterpreting a 32-bit `%s` value as the pointer-shaped type `%s` via `as` truncates the address: the high 32 bits were lost when the value became `%s`, so this recovers a corrupt pointer once the heap exceeds 4 GiB — carry pointer-shaped values in a `usize` local/param instead", inner, n.Target, inner)
				}
				return n.Target
			}
		}
		c.stampStructTypeArgs(n.Inner, n.Target)
		c.refineCallTypeArgsFromDest(n.Inner, n.Target)
		inner = c.maybeWrapForUnion(n.Target, &n.Inner, inner, s)
		n.InnerType = inner
		if c.assignable(n.Target, inner) {
			return n.Target
		}
		if inner == nil {
			return n.Target
		}
		c.errfCode(n.P, "E033", "cannot cast %s to %s: `as` converts only between numeric types", inner, n.Target)
		return n.Target
	case *ast.BoolLit:
		return ast.BoolType{}
	case *ast.UnitLit:
		return ast.VoidType{}
	case *ast.StringLit:
		return ast.StringType{}
	case *ast.CharLit:
		if n.IsByte {
			return ast.NumberType{Width: 8, Signed: false}
		}
		return ast.CharType{}
	case *ast.FString:
		if n.Desugared == nil {
			if len(n.Parts) == 0 {
				n.Desugared = &ast.StringLit{P: n.P, Value: ""}
			} else {
				var built ast.Expr
				for _, part := range n.Parts {
					var piece ast.Expr
					if part.Expr != nil {
						piece = &ast.Call{
							P: n.P,
							Callee: &ast.FieldAccess{
								P:      n.P,
								Target: part.Expr,
								Field:  "to_string",
							},
						}
					} else {
						piece = &ast.StringLit{P: n.P, Value: part.Lit}
					}
					if built == nil {
						built = piece
					} else {
						built = &ast.Binary{P: n.P, Op: "+", Left: built, Right: piece}
					}
				}
				n.Desugared = built
			}
		}
		_ = c.checkExpr(n.Desugared, s)
		return ast.StringType{}
	case *ast.FloatLit:
		if n.Width != 0 {
			return ast.FloatType{Width: n.Width}
		}
		return ast.FloatType{Polymorphic: true}
	case *ast.Ident:
		if t, ok := s.lookup(n.Name); ok {
			n.Local = true
			n.ConstParam = s.binder(n.Name).consts[n.Name]
			c.noteLitIdent(n, t, s.lookupVarDecl(n.Name))
			return t
		}
		if len(c.captureChain) > 0 {
			for i := len(c.captureChain) - 1; i >= 0; i-- {
				ent := c.captureChain[i]
				if ent.scope == nil {
					continue
				}
				if t, ok := ent.scope.lookup(n.Name); ok {
					n.Local = true
					n.ConstParam = ent.scope.binder(n.Name).consts[n.Name]
					for j := i; j < len(c.captureChain); j++ {
						if c.captureChain[j].sink != nil {
							c.captureChain[j].sink(n.Name, t)
						}
					}
					if decl := ent.scope.lookupVarDecl(n.Name); decl != nil {
						c.noteLitIdent(n, t, decl)
						if c.capturedVars == nil {
							c.capturedVars = map[*ast.Var]bool{}
						}
						c.capturedVars[decl] = true
					}
					return t
				}
			}
		}
		if gf, isGen := c.info.GenericFuncs[n.Name]; isGen && c.funcVisibleHere(n.Name) {
			if t, ok := c.instantiateFuncValue(n, gf); ok {
				return t
			}
			c.errE040GenericFuncAsValue(n.P, n.Name, gf)
			return nil
		}
		if sig, ok := c.visibleFuncSig(n.Name); ok {
			if lanesInside(sig) && c.callee != ast.Expr(n) {
				c.errfCode(n.P, "E085", "function %s takes or returns a lane value and cannot be used as a value; a lane value crosses only a direct call", n.Name)
			}
			return sig
		}
		if cd, ok := c.consts[n.Name]; ok && c.moduleSeesVariant(c.currentModule(), cd.SourceModule) {
			return c.constType(cd)
		}
		if n.EnumName == "" {
			if en := c.monomorphCloneEnumName(c.expectedType); en != "" {
				if _, ok, _ := c.resolveVariant(n.Name, en); ok {
					n.EnumName = en
				}
			}
		}
		if vr, ok, multi := c.resolveVariant(n.Name, n.EnumName); ok {
			if len(vr.payloads) > 0 {
				c.errfCode(n.P, "E036", "variant %s expects %d payload argument(s); call it as %s(...)",
					n.Name, len(vr.payloads), n.Name)
				return nil
			}
			n.EnumName = vr.enumName
			c.recordEnumConstruction(n, vr, ast.EnumType{Name: vr.enumName}, nil, c.expectedType)
			return ast.EnumType{Name: vr.enumName}
		} else if multi {
			c.errfCode(n.P, "E036", "variant %q is declared in multiple enums (%s) — qualify the reference, e.g. %s",
				n.Name, c.variantEnumList(n.Name), c.variantQualifierHint(n.Name, ""))
			return nil
		} else if n.EnumName != "" {
			c.errfCode(n.P, "E036", "enum %s has no variant %q", c.enumHintName(n.EnumName), n.Name)
			return nil
		}
		c.errIdent(n, s, "cannot find %q in this scope", n.Name)
		return nil
	case *ast.ArrayLit:
		hint := c.elemHint
		c.elemHint = nil
		var elemExpected ast.Type
		if at, ok := c.expectedType.(ast.ArrayType); ok {
			elemExpected = at.Elem
		}
		c.expectedType = nil
		checkElem := func(el ast.Expr) ast.Type {
			c.expectedType = elemExpected
			t := c.checkExpr(el, s)
			c.expectedType = nil
			return t
		}
		if dt, ok := hint.(ast.DynTraitType); ok {
			for i := range n.Elems {
				t := checkElem(n.Elems[i])
				t = c.maybeWrapForUnion(dt, &n.Elems[i], t, s)
				if t != nil && !c.assignable(dt, t) {
					c.errfCode(n.Elems[i].Pos(), "E034",
						"array element of type %s does not implement %s, so it cannot be a `%s`",
						t, c.missingDynTraits(dt, t), dt.String())
				}
			}
			n.ElemType = dt
			return ast.ArrayType{Elem: dt}
		}
		if len(n.Elems) == 0 {
			if n.ElemType != nil {
				return ast.ArrayType{Elem: n.ElemType}
			}
			return ast.ArrayType{Elem: nil}
		}
		if eu, ok := hint.(ast.EnumType); ok {
			for i := range n.Elems {
				et := checkElem(n.Elems[i])
				et = c.maybeWrapForUnion(eu, &n.Elems[i], et, s)
				if et != nil && !c.assignable(eu, et) {
					c.errfCode(n.Elems[i].Pos(), "E034", "array element: expected %s, got %s", eu, et)
				}
			}
			n.ElemType = eu
			return ast.ArrayType{Elem: eu}
		}
		if _, ok := elemExpected.(ast.StrType); ok {
			for _, el := range n.Elems {
				if t := checkElem(el); t != nil && !c.assignable(elemExpected, t) {
					c.errfCode(el.Pos(), "E034", "array element: expected %s, got %s", elemExpected, t)
				}
			}
			n.ElemType = elemExpected
			return ast.ArrayType{Elem: elemExpected}
		}
		elemT := checkElem(n.Elems[0])
		for _, el := range n.Elems[1:] {
			t := checkElem(el)
			if t == nil || elemT == nil {
				continue
			}
			if ast.Equal(t, elemT) {
				continue
			}
			if unified := unifyIfArms(elemT, t); unified != nil {
				elemT = unified
				continue
			}
			c.errfCode(el.Pos(), "E034", "array element: expected %s, got %s", elemT, t)
		}
		if elemT == nil {
			return nil
		}
		n.ElemType = elemT
		for _, el := range n.Elems {
			c.settleNumeric(el, elemT)
		}
		c.lanesContained(ast.ArrayType{Elem: elemT}, n.P)
		return ast.ArrayType{Elem: elemT}
	case *ast.Index:
		at := c.checkExpr(n.Array, s)
		it := c.settleIndexOperand(n.Idx, c.checkExpr(n.Idx, s))
		if nt, ok := it.(ast.NumberType); ok {
			n.IdxUnsigned = !nt.IsSigned()
		} else if it != nil {
			c.errfCode(n.Idx.Pos(), "E034", "index must be an integer, got %s", it)
		}
		if arr, ok := at.(ast.ArrayType); ok {
			n.ElemType = arr.Elem
			return arr.Elem
		}
		if sl, ok := at.(ast.SliceType); ok {
			n.IsSlice = true
			n.ElemType = sl.Elem
			return sl.Elem
		}
		if _, ok := at.(ast.StringType); ok {
			n.IsString = true
			return ast.NumberType{Width: 8, Signed: false}
		}
		if _, ok := at.(ast.StrType); ok {
			n.IsString = true
			return ast.NumberType{Width: 8, Signed: false}
		}
		if at != nil {
			c.errfCode(n.P, "E034", "indexing non-array value of type %s", at)
		}
		return nil
	case *ast.SliceExpr:
		st := c.checkExpr(n.Source, s)
		if n.Low != nil {
			lt := c.settleIndexOperand(n.Low, c.checkExpr(n.Low, s))
			if lt != nil && !ast.Equal(lt, ast.NumberType{}) {
				c.errfCode(n.Low.Pos(), "E037", "slice low bound must be i32, got %s", lt)
			}
		}
		if n.High != nil {
			ht := c.settleIndexOperand(n.High, c.checkExpr(n.High, s))
			if ht != nil && !ast.Equal(ht, ast.NumberType{}) {
				c.errfCode(n.High.Pos(), "E037", "slice high bound must be i32, got %s", ht)
			}
		}
		if arr, ok := st.(ast.ArrayType); ok {
			n.ElemType = arr.Elem
			return ast.SliceType{Elem: arr.Elem}
		}
		if sl, ok := st.(ast.SliceType); ok {
			n.SourceIsSlice = true
			n.ElemType = sl.Elem
			return sl
		}
		if _, ok := st.(ast.StringType); ok {
			n.IsString = true
			return ast.EnumType{Name: "Option", Args: []ast.Type{ast.StrType{}}}
		}
		if _, ok := st.(ast.StrType); ok {
			n.IsString = true
			return ast.EnumType{Name: "Option", Args: []ast.Type{ast.StrType{}}}
		}
		if st != nil {
			c.errfCode(n.P, "E037", "cannot slice value of type %s", st)
		}
		return nil
	case *ast.Call:
		c.laneIndexArg(n)
		delete(c.info.IntrinsicCalls, n)
		callExpected := c.expectedType
		c.expectedType = nil
		var writtenTypeArgs []ast.Type
		c.retagIndexAsTypeArgs(n, s)
		if id, ok := n.Callee.(*ast.Ident); ok && len(n.Args) == 1 {
			switch id.Name {
			case "print", "write", "eprint":
				at := c.checkExpr(n.Args[0], s)
				at = c.postSettleType(n.Args[0], at)
				if at == nil {
					return ast.VoidType{}
				}
				switch at.(type) {
				case ast.StringType, ast.StrType:
					return ast.VoidType{}
				}
				if !c.typeImplementsDisplay(at) {
					atn, _ := methodTypeName(at)
					c.errfCode(n.Args[0].Pos(), "E038",
						"argument 1 to %q: %s does not implement `Display` (no `to_string(): string` in scope) — give %s a `to_string(): string` method, or %s",
						id.Name, typeLabel(at), typeLabel(at), deriveHint(atn, "cmp.Display", "cmp.Display"))
					return ast.VoidType{}
				}
				arg := n.Args[0]
				n.Args[0] = &ast.Call{
					P: arg.Pos(),
					Callee: &ast.FieldAccess{
						P:      arg.Pos(),
						Target: arg,
						Field:  "to_string",
					},
				}
				c.typedRecv, c.typedRecvType = arg, at
				_ = c.checkExpr(n.Args[0], s)
				c.typedRecv, c.typedRecvType = nil, nil
				return ast.VoidType{}
			}
		}
		if id, ok := n.Callee.(*ast.Ident); ok && id.Name == "map_new" {
			c.needCoreMap(n.P)
		}
		if id, ok := n.Callee.(*ast.Ident); ok && id.Name == "cell_new" {
			if len(n.Args) != 1 {
				c.errfCode(n.P, "E004", "cell_new expects 1 argument, got %d", len(n.Args))
				return ast.StructType{Name: "Cell"}
			}
			var want ast.Type
			if ct, ok := callExpected.(ast.StructType); ok && ct.Name == "Cell" && len(ct.Args) == 1 {
				want = ct.Args[0]
			}
			c.setElemHintFor(n.Args[0], want)
			c.expectedType = want
			at := c.checkExpr(n.Args[0], s)
			c.expectedType = nil
			c.elemHint = nil
			if want != nil {
				c.settleNumeric(n.Args[0], want)
				at = c.postSettleType(n.Args[0], at)
				c.stampStructTypeArgs(n.Args[0], want)
				c.refineCallTypeArgsFromDest(n.Args[0], want)
				if at != nil && c.assignable(want, at) {
					at = want
				}
			}
			at = c.postSettleType(n.Args[0], at)
			if at == nil {
				return ast.StructType{Name: "Cell"}
			}
			c.cellElemError(n.Args[0].Pos(), at)
			n.TypeArgs = []ast.Type{at}
			return ast.StructType{Name: "Cell", Args: []ast.Type{at}}
		}
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			if tid, ok := fa.Target.(*ast.Ident); ok {
				if tid.Name == "Self" && c.current != nil && c.current.MethodRecv != "" {
					tid.Name = c.current.MethodRecv
				}
				if _, shadowed := c.identValueBinding(tid.Name, s); !shadowed {
					if c.typeParamDecl(tid.Name) != nil {
						tm, boundTrait, found := c.resolveTraitMethodForParam(tid.Name, fa.Field)
						if n.AssocConst && !(found && tm.Assoc) {
							c.errfCode(n.P, "E043", "%s has no associated constant %q", demangle(tid.Name), fa.Field)
							return nil
						}
						if found && tm.Assoc {
							if !c.assocConstReadOK(n, tid.Name, fa.Field, tm.Const) {
								return nil
							}
							tp := ast.ParamType{Name: tid.Name}
							if len(n.Args) != len(tm.Params) {
								c.errfCode(n.P, "E004", "%s", arityMsg(fmt.Sprintf("associated function %q", fa.Field), len(tm.Params), len(n.Args), "argument"))
								return ast.SubstSelf(tm.Result, tp)
							}
							for i, arg := range n.Args {
								at := c.checkExpr(arg, s)
								want := ast.SubstSelf(tm.Params[i].Type, tp)
								if at != nil && !c.argOK(&n.Args[i], want, at, tm.Params[i].Own) {
									c.errfCode(arg.Pos(), "E038", "argument %d to %q: expected %s, got %s", i+1, fa.Field, want, at)
								}
							}
							n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: tp, OwnerTrait: boundTrait, Assoc: true}
							return ast.SubstSelf(tm.Result, tp)
						}
						c.errTypeParamAssoc(tid, fa.Field)
						for _, arg := range n.Args {
							c.checkExpr(arg, s)
						}
						return nil
					}
					_, isStruct := c.info.Structs[tid.Name]
					_, isEnum := c.info.Enums[tid.Name]
					isPrim := isPrimitiveTypeName(tid.Name)
					if isStruct || isEnum || isPrim {
						var prefer []string
						if n.Method != nil && n.Method.Assoc && n.Method.OwnerTrait != "" {
							prefer = []string{n.Method.OwnerTrait}
						}
						if mangled, _, tied, ok := c.info.ResolveMethodFrom(c.currentModule(), tid.Name, fa.Field, prefer); ok {
							if len(tied) > 0 {
								c.errAmbiguousMethod(fa.FieldPos, tid.Name, fa.Field, c.info.methodCands(tid.Name, fa.Field, tied))
							}
							if strings.HasPrefix(mangled, "__assoc_") {
								if !c.assocConstReadOK(n, tid.Name, fa.Field, c.info.AssocConsts[mangled]) {
									return nil
								}
								n.Callee = &ast.Ident{P: fa.P, Name: mangled}
							} else {
								c.errfCode(n.P, "E043", "%s.%s is a method; call it on a value (`v.%s(...)`), not on the type — only associated functions (no `self`) use `%s.%s(...)`",
									demangle(tid.Name), fa.Field, fa.Field, demangle(tid.Name), fa.Field)
								return nil
							}
						} else if n.AssocConst {
							c.errfCode(n.P, "E043", "%s has no associated constant %q", demangle(tid.Name), fa.Field)
							return nil
						}
					}
				}
			}
		}
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			if tid, ok := fa.Target.(*ast.Ident); ok {
				if _, shadowed := c.identValueBinding(tid.Name, s); !shadowed && c.info.Enums[tid.Name] != nil {
					n.Callee = &ast.Ident{P: fa.P, Name: fa.Field, EnumName: tid.Name}
				}
			}
		}
		if id, ok := n.Callee.(*ast.Ident); ok {
			vr, vrOk, vrMulti := c.resolveVariant(id.Name, id.EnumName)
			nameTaken := c.isUserFuncOrLocal(id.Name, s)
			qualified := id.EnumName != ""
			if vrMulti && id.EnumName == "" && !nameTaken {
				if en := c.monomorphCloneEnumName(callExpected); en != "" {
					if vr2, ok2, _ := c.resolveVariant(id.Name, en); ok2 {
						vr, vrOk, vrMulti = vr2, true, false
						id.EnumName = en
					}
				}
			}
			isVar := vrOk && (id.EnumName != "" || !nameTaken)
			if !isVar && vrMulti && id.EnumName == "" && !c.isUserFuncOrLocal(id.Name, s) {
				c.errfCode(n.P, "E036", "variant %q is declared in multiple enums (%s) — qualify the reference, e.g. %s",
					id.Name, c.variantEnumList(id.Name), c.variantQualifierHint(id.Name, "(...)"))
				return nil
			}
			if isVar {
				if len(vr.payloads) == 0 && !qualified {
					c.errfCode(n.P, "E038", "calling non-function value of type %s", ast.EnumType{Name: vr.enumName})
					return nil
				}
				if len(n.Args) != len(vr.payloads) {
					c.errfCode(n.P, "E036", "%s", arityMsg("variant "+id.Name, len(vr.payloads), len(n.Args), "argument"))
				}
				id.EnumName = vr.enumName
				ed := c.info.Enums[vr.enumName]
				sub := map[string]ast.Type{}
				for i, a := range n.Args {
					if i >= len(vr.payloads) {
						break
					}
					if !containsParamType(vr.payloads[i]) {
						c.settleNumeric(a, vr.payloads[i])
					}
				}
				for i, a := range n.Args {
					if i < len(vr.payloads) && c.monomorphCloneEnumName(substituteType(vr.payloads[i], sub)) != "" {
						c.expectedType = substituteType(vr.payloads[i], sub)
					}
					at := c.checkExpr(a, s)
					c.expectedType = nil
					if i < len(vr.payloads) {
						if hint := substituteType(vr.payloads[i], sub); !containsParamType(hint) {
							c.settleNumeric(a, hint)
						}
						at = c.postSettleType(a, at)
					}
					if i >= len(vr.payloads) || at == nil {
						continue
					}
					c.requireValue(a, at)
					dynPayloadOK := false
					if dt, ok := substituteType(vr.payloads[i], sub).(ast.DynTraitType); ok {
						if _, srcDyn := at.(ast.DynTraitType); !srcDyn && c.assignable(dt, at) {
							if tn, ok2 := methodTypeName(at); ok2 {
								dynPayloadOK = true
								if c.info.DynCoercions == nil {
									c.info.DynCoercions = map[ast.Expr]DynCoercion{}
								}
								c.info.DynCoercions[a] = DynCoercion{Trait: dt.Trait0(), Traits: dt.Traits, Concrete: tn}
							}
						}
					}
					if !c.unifyType(vr.payloads[i], at, sub) && !dynPayloadOK {
						c.errfCode(a.Pos(), "E036", "variant %s payload %d type %s, expected %s",
							id.Name, i, at, substituteType(vr.payloads[i], sub))
					}
				}
				resolvedPayloads := make([]ast.Type, len(vr.payloads))
				for i := range vr.payloads {
					resolvedPayloads[i] = substituteType(vr.payloads[i], sub)
				}
				for i, a := range n.Args {
					if i < len(resolvedPayloads) {
						c.settleNumeric(a, resolvedPayloads[i])
					}
				}
				n.IsVariantCall = true
				if ed != nil && len(ed.TypeParams) > 0 {
					args := make([]ast.Type, len(ed.TypeParams))
					complete := true
					destArgs := destEnumArgs(callExpected, vr.enumName, len(ed.TypeParams))
					numericConflict := false
					for i, p := range ed.TypeParams {
						pinned, ok := sub[p]
						if !ok || destArgs == nil {
							continue
						}
						if resultPayloadWidens(vr.enumName, pinned, destArgs[i]) {
							numericConflict = true
							destArgs = nil
						}
					}
					for i, p := range ed.TypeParams {
						if v, ok := sub[p]; ok {
							args[i] = v
							continue
						}
						if destArgs != nil {
							if _, isParam := destArgs[i].(ast.ParamType); !isParam {
								args[i] = destArgs[i]
								sub[p] = destArgs[i]
								continue
							}
						}
						complete = false
						args[i] = ast.UnboundType{}
					}
					if !complete {
						if len(sub) > 0 && !numericConflict {
							result := ast.EnumType{Name: vr.enumName, Args: args}
							c.recordEnumConstruction(n, vr, result, resolvedPayloads, callExpected)
							return result
						}
						c.recordEnumConstruction(n, vr, ast.EnumType{Name: vr.enumName}, resolvedPayloads, callExpected)
						return ast.EnumType{Name: vr.enumName}
					}
					c.recordEnumConstruction(n, vr, ast.EnumType{Name: vr.enumName, Args: args}, resolvedPayloads, callExpected)
					return ast.EnumType{Name: vr.enumName, Args: args}
				}
				c.recordEnumConstruction(n, vr, ast.EnumType{Name: vr.enumName}, resolvedPayloads, callExpected)
				return ast.EnumType{Name: vr.enumName}
			}
		}
		var recvFA *ast.FieldAccess
		var recvType ast.Type
		var recvIsArg0 bool
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			tt := c.receiverType(fa.Target, s)
			recvFA, recvType = fa, tt
			if tt == nil {
				return nil
			}
			if pt, ok := tt.(ast.ParamType); ok {
				tm, boundTrait, found := c.resolveTraitMethodForParam(pt.Name, fa.Field)
				if !found {
					c.errfCode(n.P, "E021",
						"no method %q on type parameter %s; add a trait bound such as [%s: SomeTrait] that provides it",
						fa.Field, pt.Name, pt.Name)
					return nil
				}
				if tm.Assoc {
					c.errfCode(n.P, "E021",
						"%q is an associated function of trait %s, not a method; call it as %s.%s(...)",
						fa.Field, boundTrait, pt.Name, fa.Field)
					return nil
				}
				wantParams := tm.Params[1:]
				if len(n.Args) != len(wantParams) {
					c.errfCode(n.P, "E004", "%s", arityMsg(fmt.Sprintf("method %q", fa.Field), len(wantParams), len(n.Args), "argument"))
					return ast.SubstSelf(tm.Result, tt)
				}
				n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: tt, OwnerTrait: boundTrait}
				return c.checkBoundTraitCall(n, tm, wantParams, tt, callExpected, s)
			}
			if dt, ok := tt.(ast.DynTraitType); ok {
				return c.checkDynMethodCall(n, fa, dt, s)
			}
			var typeName string
			switch t := tt.(type) {
			case ast.StructType:
				typeName = t.Name
			case ast.EnumType:
				typeName = t.Name
			case ast.ArrayType:
				_ = t
				typeName = "Array"
			case ast.SliceType:
				_ = t
				typeName = "slice"
			default:
				if n, ok := methodTypeName(tt); ok {
					typeName = n
				}
			}
			if typeName != "" {
				var prefer []string
				if n.Method != nil && n.Method.OwnerTrait != "" {
					prefer = []string{n.Method.OwnerTrait}
				}
				if prefer == nil && c.current != nil && c.current.TraitDefault != "" && typeName == c.current.MethodRecv {
					prefer = []string{c.current.TraitDefault}
				}
				if mangled, ownerTrait, tied, ok := c.info.ResolveMethodFrom(c.currentModule(), typeName, fa.Field, prefer); ok && (c.methodVisibleHere(mangled) || c.methodImplementsTrait(typeName, fa.Field)) {
					if len(tied) > 0 {
						c.errAmbiguousMethod(fa.FieldPos, typeName, fa.Field, c.info.methodCands(typeName, fa.Field, tied))
					}
					if fa.Field == "drop" && c.hasDropImpl(typeName) {
						c.errfCode(fa.FieldPos, "E073",
							"`drop` is a finalizer, not a method to call: the runtime runs %s's `drop` when the value's last reference goes away, so calling it here runs the body a second time on a value that will still be finalized", typeName)
					}
					n.Method = &ast.MethodCallSite{
						Field:      fa.Field,
						FieldPos:   fa.FieldPos,
						Receiver:   tt,
						OwnerTrait: ownerTrait,
					}
					mangled = c.instForReceiver(typeName, fa.Field, ownerTrait, mangled, tt)
					n.Callee = &ast.Ident{P: fa.P, Name: mangled}
					n.Args = append([]ast.Expr{fa.Target}, n.Args...)
					recvIsArg0 = true
					if st, ok := tt.(ast.StructType); ok && len(st.Args) > 0 {
						writtenTypeArgs = takeWrittenTypeArgs(n, writtenTypeArgs)
						n.TypeArgs = st.Args
					}
					if at, ok := tt.(ast.ArrayType); ok {
						writtenTypeArgs = takeWrittenTypeArgs(n, writtenTypeArgs)
						n.TypeArgs = []ast.Type{at.Elem}
					}
					if sl, ok := tt.(ast.SliceType); ok {
						writtenTypeArgs = takeWrittenTypeArgs(n, writtenTypeArgs)
						n.TypeArgs = []ast.Type{sl.Elem}
					}
				} else if builtin, ok := builtinScalarMethod(typeName, fa.Field); ok {
					n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: tt}
					n.Callee = &ast.Ident{P: fa.P, Name: builtin}
					n.Args = append([]ast.Expr{fa.Target}, n.Args...)
					recvIsArg0 = true
				}
			}
		}
		var callee ast.Type
		if fa, ok := n.Callee.(*ast.FieldAccess); ok && fa == recvFA {
			callee = c.fieldAccessType(fa, s, recvType)
		} else if id, ok := n.Callee.(*ast.Ident); ok && c.calleeIsGenericFunc(id, s) {
			callee = c.info.FuncSigs[id.Name]
		} else {
			prevCallee := c.callee
			c.callee = n.Callee
			callee = c.checkExpr(n.Callee, s)
			c.callee = prevCallee
			if inner, ok := n.Callee.(*ast.Call); ok {
				if widened := c.widenGenericCallByLiterals(inner); widened != nil {
					callee = widened
				}
			}
		}
		ft, ok := callee.(*ast.FuncType)
		if !ok {
			if callee != nil {
				c.errfCode(n.P, "E038", "calling non-function value of type %s", callee)
			}
			return nil
		}
		intrinsic := IntrinsicNone
		if id, direct := n.Callee.(*ast.Ident); direct {
			if _, local := c.identValueBinding(id.Name, s); !local {
				intrinsic = c.intrinsicSigs[ft]
			}
		}
		methodSubResult := ft.Result
		if id, ok := n.Callee.(*ast.Ident); ok && len(n.TypeArgs) > 0 &&
			strings.HasPrefix(id.Name, "__method_") {
			rest := id.Name[len("__method_"):]
			if idx := strings.Index(rest, "_"); idx > 0 {
				typeName := rest[:idx]
				var typeParams []string
				if sd := c.info.Structs[typeName]; sd != nil {
					typeParams = sd.TypeParams
				} else if ed := c.info.Enums[typeName]; ed != nil {
					typeParams = ed.TypeParams
				} else if typeName == "Array" {
					typeParams = []string{"T"}
				} else if typeName == "slice" {
					typeParams = []string{"T"}
				}
				if len(typeParams) == len(n.TypeArgs) {
					sub := make(map[string]ast.Type, len(typeParams))
					for i, tp := range typeParams {
						if c.receiverDeclares(id.Name, tp) {
							sub[tp] = n.TypeArgs[i]
						}
					}
					substitutedParams := make([]ast.Type, len(ft.Params))
					for i, p := range ft.Params {
						substitutedParams[i] = substituteType(p, sub)
					}
					ft = &ast.FuncType{
						Params:   substitutedParams,
						ParamOwn: ft.ParamOwn,
						Result:   substituteType(ft.Result, sub),
					}
					methodSubResult = ft.Result
				}
			}
		}
		_ = methodSubResult
		if len(n.Args) != len(ft.Params) {
			subject, want, got := "this function", len(ft.Params), len(n.Args)
			if recvIsArg0 && n.Method != nil {
				subject, want, got = fmt.Sprintf("method %q", n.Method.Field), want-1, got-1
			} else if n.SourceCallee != "" {
				subject = fmt.Sprintf("function %q", n.SourceCallee)
			} else if id, ok := n.Callee.(*ast.Ident); ok {
				subject = fmt.Sprintf("function %q", demangle(id.Name))
			}
			c.errfCode(n.P, "E004", "%s", arityMsg(subject, want, got, "argument"))
		}
		if id, ok := n.Callee.(*ast.Ident); ok {
			if _, isGen := c.info.GenericFuncs[id.Name]; isGen {
				if _, isValue := c.identValueBinding(id.Name, s); isValue {
					c.shadowedGenericCalls[n] = true
				}
			}
		}
		var sub map[string]ast.Type
		var genericFn *ast.FuncDecl
		if id, ok := n.Callee.(*ast.Ident); ok && !c.shadowedGenericCalls[n] {
			if fn, isGen := c.info.GenericFuncs[id.Name]; isGen {
				genericFn = fn
				sub = make(map[string]ast.Type, len(fn.TypeParams))
				if len(n.TypeArgs) > 0 {
					recvArgs, written := n.TypeArgs, writtenTypeArgs
					if written == nil && n.TypeArgsWritten {
						recvArgs, written = nil, n.TypeArgs
					}
					count := len(n.TypeArgs)
					if written != nil {
						count = len(written)
					}
					tooMany := count > len(fn.TypeParams)
					tooFew := count < len(fn.TypeParams)
					if tooMany || (tooFew && n.Method == nil) {
						display, _ := callSiteName(fn)
						c.errfCode(n.P, "E040", "%s", arityMsg(display, len(fn.TypeParams), count, "type argument"))
					} else if n.Method == nil {
						c.constArgDiags(fn, written, n.P, s)
					}
					for i, ta := range recvArgs {
						if i < len(fn.TypeParams) && c.receiverDeclares(id.Name, fn.TypeParams[i]) {
							sub[fn.TypeParams[i]] = ta
						}
					}
					offset := 0
					if n.Method != nil && len(written) < len(fn.TypeParams) {
						offset = len(fn.TypeParams) - len(written)
					}
					for i, ta := range written {
						if offset+i >= len(fn.TypeParams) {
							continue
						}
						if _, isInt := ta.(ast.ConstArg); isInt && !slices.Contains(fn.ConstParams, fn.TypeParams[offset+i]) {
							continue
						}
						sub[fn.TypeParams[offset+i]] = ta
					}
				}
			}
		}
		var calleeOwnFlags []bool
		calleeIsValue := false
		calleeName := ""
		if cid, ok := n.Callee.(*ast.Ident); ok {
			calleeName = cid.Name
			if _, isValue := c.identValueBinding(cid.Name, s); isValue {
				calleeIsValue = true
			} else {
				calleeOwnFlags = c.info.OwnFuncs[cid.Name]
			}
		}
		if len(calleeOwnFlags) == 0 && ft != nil {
			calleeOwnFlags = ft.ParamOwn
		}
		if (len(calleeOwnFlags) > 0 || calleeIsValue) && !recvIsArg0 && n.Method == nil {
			if c.callOwnFlags == nil {
				c.callOwnFlags = map[*ast.Call][]bool{}
			}
			c.callOwnFlags[n] = calleeOwnFlags
		}
		type deferredEmptyArg struct {
			i        int
			expected ast.Type
			own      bool
		}
		var deferredArgs []deferredEmptyArg
		for i := range n.Args {
			if i < len(ft.Params) {
				c.setElemHintFor(n.Args[i], ft.Params[i])
			}
			at := recvType
			if !(recvIsArg0 && i == 0) {
				if i < len(ft.Params) {
					pt := ft.Params[i]
					if sub != nil {
						pt = substituteType(pt, sub)
					}
					if c.monomorphCloneEnumName(pt) != "" {
						c.expectedType = pt
					} else if _, isEnum := pt.(ast.EnumType); isEnum && sub != nil {
						c.expectedType = pt
					} else if st, isStruct := pt.(ast.StructType); isStruct && len(st.Args) > 0 {
						c.expectedType = pt
					} else if _, isArray := pt.(ast.ArrayType); isArray {
						if _, isLit := n.Args[i].(*ast.ArrayLit); isLit {
							c.expectedType = pt
						}
					} else if _, isFn := pt.(*ast.FuncType); isFn {
						if _, isName := n.Args[i].(*ast.Ident); isName {
							c.expectedType = pt
						}
					}
				}
				at = c.checkExpr(n.Args[i], s)
				c.expectedType = nil
			}
			c.elemHint = nil
			if definitelyScalar(at) {
				if c.scalarArgs == nil {
					c.scalarArgs = map[ast.Expr]bool{}
				}
				c.scalarArgs[n.Args[i]] = true
			}
			if i < len(ft.Params) && at != nil {
				expected := ft.Params[i]
				if sub != nil {
					expected = c.resolveProjWithSub(expected, sub)
				}
				if pt, ok := expected.(ast.ParamType); ok && sub != nil {
					if bound, isBound := sub[pt.Name]; isBound {
						switch bound.(type) {
						case ast.NumberType, ast.FloatType:
							c.settleNumeric(n.Args[i], bound)
							at = c.postSettleType(n.Args[i], at)
						}
					}
				}
				if !(sub != nil && containsParamType(expected)) {
					c.settleNumeric(n.Args[i], expected)
					at = c.postSettleType(n.Args[i], at)
				}
				at = c.maybeWrapForUnion(expected, &n.Args[i], at, s)
				if sub == nil {
					c.refineCallTypeArgsFromDest(n.Args[i], expected)
				}
				own := i < len(calleeOwnFlags) && calleeOwnFlags[i] ||
					!calleeIsValue && storesArgument(calleeName, i)
				if arr, isArr := at.(ast.ArrayType); isArr && arr.Elem == nil && sub != nil && containsParamType(expected) {
					deferredArgs = append(deferredArgs, deferredEmptyArg{i, expected, own})
					continue
				}
				if sub != nil {
					if id, isName := n.Args[i].(*ast.Ident); isName && len(id.TypeArgs) > 0 && containsParamType(at) {
					} else if !c.unifyArrayArg(&n.Args[i], expected, at, sub, own) {
						c.errArgMismatch(n, i, recvIsArg0, substituteType(expected, sub), at)
					}
				} else if !c.argOK(&n.Args[i], expected, at, own) {
					c.errArgMismatch(n, i, recvIsArg0, expected, at)
				}
			}
		}
		if genericFn != nil {
			if callExpected != nil && sub != nil {
				c.unifyType(ft.Result, callExpected, sub)
			}
			tpSet := make(map[string]bool, len(genericFn.TypeParams))
			for _, tp := range genericFn.TypeParams {
				tpSet[tp] = true
			}
			if sub != nil {
				for changed := true; changed; {
					changed = false
					for _, tp := range genericFn.TypeParams {
						bt, ok := sub[tp]
						if !ok {
							continue
						}
						if _, isParam := bt.(ast.ParamType); isParam {
							continue
						}
						tn, okTn := methodTypeName(bt)
						if !okTn {
							continue
						}
						for bi, traitName := range genericFn.Bounds[tp] {
							ba := genericFn.BoundArgs[tp]
							if bi >= len(ba) || len(ba[bi]) == 0 {
								continue
							}
							implArgs := c.implTraitArgsFor(traitName, tn, bt)
							if len(implArgs) != len(ba[bi]) {
								continue
							}
							for k := range ba[bi] {
								if bindBoundParam(ba[bi][k], implArgs[k], tpSet, sub) {
									changed = true
								}
							}
						}
					}
				}
			}
			for i := range n.Args {
				if i < len(ft.Params) && containsParamType(ft.Params[i]) && !(recvIsArg0 && i == 0) {
					if want := substituteType(ft.Params[i], sub); !containsParamType(want) {
						c.settleNumeric(n.Args[i], want)
					}
				}
			}
			for _, d := range deferredArgs {
				want := substituteType(d.expected, sub)
				if containsParamType(want) {
					continue
				}
				c.settleNumeric(n.Args[d.i], want)
				if !c.argOK(&n.Args[d.i], want, c.postSettleType(n.Args[d.i], ast.ArrayType{}), d.own) {
					c.errArgMismatch(n, d.i, recvIsArg0, want, ast.ArrayType{})
				}
			}
			args := make([]ast.Type, len(genericFn.TypeParams))
			complete := true
			for i, tp := range genericFn.TypeParams {
				if v, ok := sub[tp]; ok {
					if ft, isF := v.(ast.FloatType); isF && ft.Polymorphic {
						v = ast.FloatType{Width: 64}
					}
					args[i] = v
				} else {
					display, spelling := callSiteName(genericFn)
					c.errfCode(n.P, "E040", "could not infer type parameter %s for %s — supply it explicitly at the call site (e.g. %s[i32](...)) or annotate the binding", tp, display, spelling)
					complete = false
				}
			}
			if complete {
				c.checkTypeArgBounds(genericFn, args, sub, tpSet, n.P)
				n.TypeArgs = args
				return c.resolveProj(substituteType(ft.Result, sub))
			}
			return nil
		}
		if intrinsic != IntrinsicNone {
			if c.info.IntrinsicCalls == nil {
				c.info.IntrinsicCalls = make(map[*ast.Call]IntrinsicCall)
			}
			c.info.IntrinsicCalls[n] = IntrinsicCall{Kind: intrinsic, Signature: ft}
		}
		return ft.Result
	case *ast.Binary:
		lt := c.checkExpr(n.Left, s)
		rt := c.checkExpr(n.Right, s)
		if lt == nil || rt == nil {
			return nil
		}
		if ft, ok := lt.(ast.FloatType); ok && !ft.Polymorphic {
			if rn, ok := rt.(ast.NumberType); ok && rn.Polymorphic {
				c.settleNumeric(n.Right, ft)
				rt = c.postSettleType(n.Right, rt)
			}
		}
		if ft, ok := rt.(ast.FloatType); ok && !ft.Polymorphic {
			if ln, ok := lt.(ast.NumberType); ok && ln.Polymorphic {
				c.settleNumeric(n.Left, ft)
				lt = c.postSettleType(n.Left, lt)
			}
		}
		switch n.Op {
		case "+":
			if isStringLike(lt) && isStringLike(rt) {
				n.IsStringConcat = true
				return ast.StringType{}
			}
			fallthrough
		case "-", "*", "/":
			if isLanes(lt) || isLanes(rt) {
				return c.lanesArith(n, lt, rt)
			}
			if rtt, handled := c.compositeOpOverload(n, lt, rt, s); handled {
				return rtt
			}
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				common, ok := commonFloatWidth(lt, rt)
				if !ok {
					if isFloat(lt) && isFloat(rt) {
						c.errfCode(n.P, "E009", "operator %q requires both operands to share a float type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
					}
					return ast.FloatType{}
				}
				c.settleNumeric(n.Left, common)
				c.settleNumeric(n.Right, common)
				if !common.Polymorphic {
					n.FloatWidth = common.NormalWidth()
				}
				return common
			}
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
				if !isInteger(lt) || !isInteger(rt) {
					return nil
				}
				return ast.NumberType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return common
		case "+|", "-|", "*|", "<<|":
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			for _, t := range []ast.Type{lt, rt} {
				if nt, ok := t.(ast.NumberType); ok && nt.IsPointerWidth() {
					c.errfCode(n.P, "E009", "saturating operator %q is not supported on `usize` — its clamp bounds are target-width-dependent; cast to a fixed-width integer (`as u64`) first", n.Op)
					return ast.NumberType{}
				}
			}
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
				if !isInteger(lt) || !isInteger(rt) {
					return nil
				}
				return ast.NumberType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return common
		case "+?", "-?", "*?", "/?", "%?", "<<?", ">>?":
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			for _, t := range []ast.Type{lt, rt} {
				if nt, ok := t.(ast.NumberType); ok && nt.IsPointerWidth() {
					c.errfCode(n.P, "E009", "checked operator %q is not supported on `usize` — its overflow bound is target-width-dependent; cast to a fixed-width integer (`as u64`) first", n.Op)
					return ast.EnumType{Name: "Option", Args: []ast.Type{nt}}
				}
			}
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
				if !isInteger(lt) || !isInteger(rt) {
					return nil
				}
				return ast.EnumType{Name: "Option", Args: []ast.Type{ast.NumberType{}}}
			}
			if common.Polymorphic {
				common = ast.NumberType{Width: 32, Signed: true}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
			n.IntWidth = common.NormalWidth()
			n.IsUnsigned = !common.IsSigned()
			n.CheckedLowered = c.buildCheckedLowered(n, common, s)
			return ast.EnumType{Name: "Option", Args: []ast.Type{common}}
		case "%", "&", "|", "^", "<<", ">>":
			if rtt, handled := c.compositeOpOverload(n, lt, rt, s); handled {
				return rtt
			}
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
				if !isInteger(lt) || !isInteger(rt) {
					return nil
				}
				return ast.NumberType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return common
		case "<", ">", "<=", ">=":
			if isLanes(lt) || isLanes(rt) {
				c.lanesCompare(n, lt, rt)
				return ast.BoolType{}
			}
			if c.paramComparable(lt, rt, "cmp") {
				n.ParamCmp = true
				return ast.BoolType{}
			}
			if _, isChar := lt.(ast.CharType); isChar && ast.Equal(lt, rt) {
				return ast.BoolType{}
			}
			if lt != nil && rt != nil && ast.Equal(lt, rt) {
				switch lt.(type) {
				case ast.StructType, ast.EnumType:
					tn, _ := methodTypeName(lt)
					if mangled, _, ok := c.resolveMethod(tn, "cmp", []string{"Ord"}); ok && c.methodVisibleHere(mangled) {
						cmpCall := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: "cmp"}, Args: []ast.Expr{n.Right}}
						if rt2 := c.checkExpr(cmpCall, s); rt2 != nil {
							if nt, isNum := rt2.(ast.NumberType); !isNum || nt.NormalWidth() != 32 {
								c.errfCode(n.P, "E041", "cannot order values of type %s with %q: its `cmp` method must return i32", lt, n.Op)
							}
						}
						n.CmpCall = cmpCall
						return ast.BoolType{}
					}
					c.errfCode(n.P, "E041", "cannot order values of type %s with %q: type does not implement `Ord` — %s, so ordering can use structural comparison", typeLabel(lt), n.Op, deriveHint(tn, "cmp.Ord", "cmp.Ord"))
					return ast.BoolType{}
				case ast.ArrayType, ast.SliceType, ast.TupleType:
					c.errfCode(n.P, "E041", "cannot order values of type %s with %q: structural ordering for arrays / slices / tuples is not supported", lt, n.Op)
					return ast.BoolType{}
				}
			}
			if isStringLike(lt) && isStringLike(rt) {
				n.IsStringOrd = true
				return ast.BoolType{}
			}
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				common, ok := commonFloatWidth(lt, rt)
				if !ok && isFloat(lt) && isFloat(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share a float type; got %s and %s - use `as` for explicit conversion", n.Op, lt, rt)
				} else if ok && !common.Polymorphic {
					c.settleNumeric(n.Left, common)
					c.settleNumeric(n.Right, common)
					n.FloatWidth = common.NormalWidth()
				}
				return ast.BoolType{}
			}
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
				return ast.BoolType{}
			}
			if common.Polymorphic {
				if def := c.polymorphicIntDefault(n.Left, n.Right); def.Width == 64 {
					common = def
				}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return ast.BoolType{}
		case "==", "!=":
			if isLanes(lt) || isLanes(rt) {
				c.lanesCompare(n, lt, rt)
				return ast.BoolType{}
			}
			if common, common_ok := commonIntegerWidth(lt, rt); common_ok {
				if common.Polymorphic {
					common = c.polymorphicIntDefault(n.Left, n.Right)
					if common.Width != 64 {
						common = ast.NumberType{Polymorphic: true}
					}
				}
				if !common.Polymorphic {
					c.settleNumeric(n.Left, common)
					c.settleNumeric(n.Right, common)
					lt = c.postSettleType(n.Left, lt)
					rt = c.postSettleType(n.Right, rt)
				}
			} else if common, common_ok := commonFloatWidth(lt, rt); common_ok && !common.Polymorphic {
				c.settleNumeric(n.Left, common)
				c.settleNumeric(n.Right, common)
				lt = c.postSettleType(n.Left, lt)
				rt = c.postSettleType(n.Right, rt)
			}
			if lt != nil && rt != nil && !ast.Equal(lt, rt) && !(isStringLike(lt) && isStringLike(rt)) {
				c.errfCode(n.P, "E041", "cannot compare %s with %s: both sides of %q must have the same type", lt, rt, n.Op)
			}
			if c.paramComparable(lt, rt, "eq") {
				return ast.BoolType{}
			}
			if lt != nil && rt != nil && ast.Equal(lt, rt) {
				switch lt.(type) {
				case ast.StructType, ast.EnumType:
					tn, _ := methodTypeName(lt)
					if mangled, _, ok := c.resolveMethod(tn, "eq", []string{"Eq"}); ok && c.methodVisibleHere(mangled) {
						eqCall := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: "eq"}, Args: []ast.Expr{n.Right}}
						if rt2 := c.checkExpr(eqCall, s); rt2 != nil {
							if _, isBool := rt2.(ast.BoolType); !isBool {
								c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: its `eq` method must return boolean", lt, n.Op)
							}
						}
						n.EqCall = eqCall
						n.EqNegate = n.Op == "!="
						return ast.BoolType{}
					}
					c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: type does not implement `Eq` — %s, so `==` can use structural equality", typeLabel(lt), n.Op, deriveHint(tn, "cmp.Eq", "cmp.Eq"))
					return ast.BoolType{}
				case ast.ArrayType, ast.SliceType, ast.TupleType:
					c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: structural equality for arrays / slices / tuples is not supported — compare elements individually", lt, n.Op)
					return ast.BoolType{}
				}
			}
			if isStringLike(lt) && isStringLike(rt) {
				n.IsStringCmp = true
			}
			if isFloat(lt) && isFloat(rt) {
				n.IsFloat = true
				if common, ok := commonFloatWidth(lt, rt); ok && !common.Polymorphic {
					c.settleNumeric(n.Left, common)
					c.settleNumeric(n.Right, common)
					n.FloatWidth = common.NormalWidth()
				}
			}
			if common, ok := commonIntegerWidth(lt, rt); ok && !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
			}
			return ast.BoolType{}
		case "&&", "||":
			c.requireBool(n.P, lt, n.Op)
			c.requireBool(n.P, rt, n.Op)
			return ast.BoolType{}
		}
		c.errfCode(n.P, "E041", "unknown binary operator %q", n.Op)
		return nil
	case *ast.Unary:
		c.markLiteralSign(n, false)
		t := c.checkExpr(n.Operand, s)
		n.CheckedType = t
		switch n.Op {
		case "-":
			if ft, ok := t.(ast.FloatType); ok {
				n.IsFloat = true
				return ft
			}
			if isLanes(t) {
				return t
			}
			if pt, ok := t.(ast.ParamType); ok {
				if _, _, found := c.resolveTraitMethodForParam(pt.Name, "neg"); found {
					call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Operand, Field: "neg"}, Args: nil}
					rtt := c.checkExpr(call, s)
					n.NegCall = call
					n.CheckedType = rtt
					return rtt
				}
			}
			switch t.(type) {
			case ast.StructType, ast.EnumType:
				tn, _ := methodTypeName(t)
				if mangled, _, ok := c.resolveMethod(tn, "neg", []string{arithOpTrait["neg"]}); ok && c.methodVisibleHere(mangled) {
					call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Operand, Field: "neg"}, Args: nil}
					rtt := c.checkExpr(call, s)
					n.NegCall = call
					n.CheckedType = rtt
					return rtt
				}
				c.errfCode(n.P, "E009", "unary `-` is not defined for %s — implement `function (self: %s) neg(): %s` to overload it", t, tn, tn)
				return t
			}
			c.requireInteger(n.P, t, n.Op)
			if nt, ok := t.(ast.NumberType); ok {
				return nt
			}
			return ast.NumberType{}
		case "!":
			c.requireBool(n.P, t, n.Op)
			return ast.BoolType{}
		}
		return nil
	case *ast.Assign:
		lt := c.checkExpr(n.Target, s)
		c.setElemHintFor(n.Value, lt)
		c.expectedType = lt
		rt := c.checkExpr(n.Value, s)
		c.expectedType = nil
		c.elemHint = nil
		if id, ok := n.Target.(*ast.Ident); ok && c.litIdents[id] != nil {
			if rn, ok := rt.(ast.NumberType); ok && !rn.Polymorphic {
				c.fixLitLocal(c.litIdents[id], rn, n.P)
				lt = c.litIdents[id].width
			}
		}
		if lt != nil {
			c.settleNumeric(n.Value, lt)
			rt = c.postSettleType(n.Value, rt)
			rt = c.maybeWrapForUnion(lt, &n.Value, rt, s)
			c.stampStructTypeArgs(n.Value, lt)
			c.refineCallTypeArgsFromDest(n.Value, lt)
		}
		if lt != nil && rt != nil && !ast.Equal(lt, rt) && !c.assignable(lt, rt) {
			what := "assignment"
			if id, ok := n.Target.(*ast.Ident); ok {
				what = fmt.Sprintf("variable %q", id.Name)
			}
			c.errfCode(n.P, "E003", "%s: expected %s, got %s%s", what, lt, rt, assignHint(lt, rt))
		}
		if fa, ok := n.Target.(*ast.FieldAccess); ok {
			c.errfCode(fa.Pos(), "E048",
				"cannot assign to field %q: fields are immutable after construction; rebuild with `T { ...old, %s: value }`",
				fa.Field, fa.Field)
		}
		if idx, ok := n.Target.(*ast.Index); ok {
			c.errfCode(idx.Pos(), "E056",
				"cannot assign to an array element: subscripts are read-only after construction; use `arr = arr.with(i, value)`")
		}
		if id, ok := n.Target.(*ast.Ident); ok && c.isConstParam(id.Name, s) {
			c.errfCode(id.Pos(), "E091", "cannot assign to const parameter %s: it is the constant its instance is made for", id.Name)
		}
		if id, ok := n.Target.(*ast.Ident); ok {
			if ct, isCap := c.capturedType(id.Name, s); isCap && ast.IsPointerType(ct) {
				c.errfCode(id.Pos(), "E049",
					"cannot assign to captured %s %q: a reference-typed closure capture is read-only (it could close a reference cycle); return the new value from the closure instead",
					ct, id.Name)
			} else if decl := s.lookupVarDecl(id.Name); decl != nil && lt != nil && ast.IsPointerType(lt) && !c.freshLambdaCannotReach(n.Value, decl, s) {
				c.cellStores = append(c.cellStores, cellStore{
					pos:      id.Pos(),
					name:     id.Name,
					decl:     decl,
					declType: lt,
					valType:  rt,
				})
			}
		}
		return lt
	case *ast.Lambda:
		root := newScope(nil)
		for _, p := range n.Params {
			if _, dup := root.names[p.Name]; dup {
				c.errfCode(n.P, "E018", "duplicate parameter %q", p.Name)
			}
			root.set(p.Name, p.Type)
		}
		captured := map[string]ast.Type{}
		var captureOrder []string
		prev := c.current
		prevSink := c.captureSink
		prevOuter := c.captureOuter
		prevLoop := c.loopDepth
		synth := &ast.FuncDecl{
			P:                 n.P,
			Params:            n.Params,
			ReturnType:        n.ReturnType,
			ReturnUnannotated: n.ReturnUnannotated,
			Body:              n.Body,
		}
		if prev != nil {
			synth.SourceModule = prev.SourceModule
		}
		n.Synthetic = synth
		c.current = synth
		prevInfer := c.inferReturns
		var lamRets []ast.Type
		if n.ReturnUnannotated {
			c.inferReturns = &lamRets
		}
		c.loopDepth = 0
		c.captureSink = func(name string, t ast.Type) {
			if _, ok := captured[name]; ok {
				return
			}
			switch t.(type) {
			case ast.VoidType:
				c.errfCode(n.P, "E044", "captured variable %q has unsupported type %s", name, t)
			case ast.LanesType:
				c.errfCode(n.P, "E085", "a lambda cannot capture %q of type %s; a lane value lives only in a local, a parameter or a result", name, typeLabel(t))
			default:
				c.captureView(n.P, name, t)
				captured[name] = t
				captureOrder = append(captureOrder, name)
			}
		}
		c.captureOuter = s
		c.captureChain = append(c.captureChain, captureEntry{sink: c.captureSink, scope: s, outer: prev})
		c.checkBlock(n.Body, root)
		c.captureChain = c.captureChain[:len(c.captureChain)-1]
		c.captureSink = prevSink
		c.captureOuter = prevOuter
		c.loopDepth = prevLoop
		c.inferReturns = prevInfer
		if n.ReturnUnannotated {
			c.inferReturnType(synth, lamRets)
			c.settleLitReturns(synth)
			n.ReturnType = synth.ReturnType
		}
		c.current = prev
		n.Captures = nil
		for _, name := range captureOrder {
			n.Captures = append(n.Captures, ast.Param{Name: name, Type: captured[name]})
		}
		c.restampLitCaptures(n.Captures, s)
		if lanesSignature(n.Params, n.ReturnType) {
			c.errfCode(n.P, "E085", "a lambda cannot take or return a lane value; a lane value crosses only a direct call")
		}
		return paramSig(n.Params, n.ReturnType)
	case *ast.BlockExpr:
		return c.checkBlockExpr(n, s)
	case *ast.IfExpr:
		ct := c.checkExpr(n.Cond, s)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errfCode(n.Cond.Pos(), "E008", "if-expression condition must be boolean, got %s", ct)
		}
		tt := c.checkExpr(n.Then, s)
		et := c.checkExpr(n.Else, s)
		result := unifyIfArms(tt, et)
		if tt != nil && et != nil && result == nil {
			c.errfCode(n.P, "E031", "if-expression branches differ: %s vs %s", tt, et)
			result = tt
		}
		if result == nil {
			result = tt
		}
		if result == nil {
			result = et
		}
		if isFloat(result) {
			n.IsFloat = true
		}
		c.settleNumeric(n, result)
		return result
	case *ast.MatchExpr:
		result := c.checkMatchExpr(n, s)
		c.settleNumeric(n, result)
		return result
	case *ast.TryOp:
		inner := c.checkExpr(n.Inner, s)
		if inner == nil {
			return nil
		}
		if c.current == nil {
			c.errfCode(n.P, "E042", "`?` operator can only be used inside a function")
			return nil
		}
		srcEnum, srcOK := inner.(ast.EnumType)
		if !srcOK {
			c.errfCode(n.P, "E042", "%s", c.notATryTypeMsg(inner))
			return nil
		}
		srcShape, srcTry := c.info.TryShapes[srcEnum.Name]
		if !srcTry {
			c.errfCode(n.P, "E042", "%s", c.notATryTypeMsg(inner))
			return nil
		}
		ret := c.current.ReturnType
		if c.inferReturns != nil && c.current.ReturnUnannotated {
			*c.inferReturns = append(*c.inferReturns, inner)
			ret = inner
		}
		retEnum, retOK := ret.(ast.EnumType)
		if !retOK || retEnum.Name != srcEnum.Name {
			c.errfCode(n.P, "E042",
				"`?` on %s requires the surrounding function to return %s, got %s",
				demangle(srcEnum.Name), demangle(srcEnum.Name), ret)
			return nil
		}
		srcOkT, okResolved := c.tryPayloadType(srcEnum, srcShape.Ok)
		if !okResolved {
			c.errfCode(n.P, "E042", "malformed %s type %s", demangle(srcEnum.Name), inner)
			return nil
		}
		n.SrcEnum = srcEnum
		n.Type = srcOkT
		if srcShape.Residual == nil {
			n.Kind = ast.TryKindBuild
			return n.Type
		}
		n.Kind = ast.TryKindForward
		srcRes, srcResOK := c.tryPayloadType(srcEnum, srcShape.Residual)
		retRes, retResOK := c.tryPayloadType(retEnum, srcShape.Residual)
		if !srcResOK || !retResOK {
			c.errfCode(n.P, "E042", "malformed %s type %s", demangle(srcEnum.Name), inner)
			return nil
		}
		if !ast.Equal(srcRes, retRes) && !unboundEnumArg(srcRes) {
			if lowered, ok := c.tryConvertErrToDyn(n, srcEnum, retEnum, s); ok {
				n.Lowered = lowered
				return n.Type
			}
			if lowered, ok := c.tryConvertErrViaFrom(n, srcEnum, retEnum, s); ok {
				n.Lowered = lowered
				return n.Type
			}
			c.errfCode(n.P, "E042", "%s", c.tryResidualMsg(srcEnum, srcRes, retRes))
			return nil
		}
		return n.Type
	case *ast.StructLit:
		sd, ok := c.info.Structs[n.TypeName]
		if !ok {
			c.errfCode(n.P, "E043", "cannot find struct %q in this scope", demangle(n.TypeName))
			return nil
		}
		c.checkOpaqueAccess(sd, n.P, "construct")
		seen := map[string]bool{}
		fieldT := map[string]ast.Type{}
		for _, f := range sd.Fields {
			fieldT[f.Name] = f.Type
		}
		if n.TypeArgsWritten && len(n.TypeArgs) != len(sd.TypeParams) {
			c.errfCode(n.P, "E040", "%s", arityMsg(sd.Name, len(sd.TypeParams), len(n.TypeArgs), "type argument"))
			n.TypeArgs = nil
			n.TypeArgsWritten = false
		}
		var sub map[string]ast.Type
		seeded := map[string]bool{}
		if len(sd.TypeParams) > 0 {
			sub = make(map[string]ast.Type, len(sd.TypeParams))
			if et, ok := c.expectedType.(ast.StructType); ok && et.Name == sd.Name && len(et.Args) == len(sd.TypeParams) {
				seedScope := c.typeParamsInScope()
				for i, tp := range sd.TypeParams {
					if _, escaped := paramTypeNotIn(et.Args[i], seedScope); escaped {
						continue
					}
					sub[tp] = et.Args[i]
					seeded[tp] = true
				}
			}
			if n.TypeArgsWritten {
				for i, tp := range sd.TypeParams {
					sub[tp] = n.TypeArgs[i]
				}
			}
		}
		var baseArgs []ast.Type
		if n.Base != nil {
			bt := c.checkExpr(n.Base, s)
			if bt != nil {
				bst, ok := bt.(ast.StructType)
				if !ok || bst.Name != sd.Name {
					c.errfCode(n.Base.Pos(), "E003", "struct-update base must be %s, got %s", sd.Name, bt)
				} else {
					baseArgs = bst.Args
					if sub != nil {
						for i, tp := range sd.TypeParams {
							if i < len(bst.Args) {
								sub[tp] = bst.Args[i]
								seeded[tp] = true
							}
						}
					}
				}
			}
		}
		clashed := false
		for i := range n.Fields {
			f := n.Fields[i]
			expected, present := fieldT[f.Name]
			if !present {
				declared := make([]string, 0, len(sd.Fields))
				for _, df := range sd.Fields {
					declared = append(declared, df.Name)
				}
				c.errUnknownField(n.P, f.NamePos, sd.Name, f.Name, declared)
				continue
			}
			if seen[f.Name] {
				c.errfCode(n.P, "E007", "duplicate field %q in struct literal", f.Name)
			}
			seen[f.Name] = true
			fieldExpected := substituteType(expected, sub)
			c.setElemHintFor(f.Value, fieldExpected)
			savedExpected := c.expectedType
			c.expectedType = fieldExpected
			if _, lit := f.Value.(*ast.StructLit); lit && sub != nil {
				if _, unbound := paramTypeNotIn(expected, boundParams(sub)); unbound {
					c.expectedType = nil
				}
			}
			vt := c.checkExpr(f.Value, s)
			c.expectedType = savedExpected
			if vt == nil {
				continue
			}
			if containsParamType(fieldExpected) {
				if at, ok := vt.(ast.ArrayType); ok && at.Elem == nil {
					continue
				}
			} else {
				c.settleNumeric(f.Value, fieldExpected)
				vt = c.postSettleType(f.Value, vt)
			}
			vt = c.maybeWrapForUnion(expected, &n.Fields[i].Value, vt, s)
			dynFieldOK := false
			if _, ok := fieldExpected.(ast.DynTraitType); ok {
				dynFieldOK = c.assignable(fieldExpected, vt)
			}
			if sub != nil {
				c.seededParams = seeded
				unified := c.unifyType(expected, vt, sub)
				c.seededParams = nil
				if !unified && !dynFieldOK {
					c.errfCode(f.Value.Pos(), "E043", "field %q: expected %s, got %s%s", f.Name, substituteType(expected, sub), vt, assignHint(substituteType(expected, sub), vt))
					clashed = true
				}
			} else if !ast.Equal(vt, expected) && !dynFieldOK {
				c.stampStructTypeArgs(f.Value, expected)
				if unifyIfArms(expected, vt) == nil && !c.assignable(expected, vt) {
					c.errfCode(f.Value.Pos(), "E043", "field %q: expected %s, got %s%s", f.Name, expected, vt, assignHint(expected, vt))
				}
			}
		}
		if n.Base == nil {
			for _, f := range sd.Fields {
				if !seen[f.Name] {
					c.errfCode(n.P, "E005", "missing field %q in this %s literal", f.Name, demangle(n.TypeName))
				}
			}
		}
		if len(sd.TypeParams) > 0 {
			args := make([]ast.Type, len(sd.TypeParams))
			complete := true
			inScope := c.typeParamsInScope()
			for i, tp := range sd.TypeParams {
				if v, ok := sub[tp]; ok {
					args[i] = v
				} else if n.Base != nil && i < len(baseArgs) {
					args[i] = baseArgs[i]
				} else {
					c.errE040StructUninferred(n.P, tp, sd.Name)
					complete = false
					continue
				}
				if _, escaped := paramTypeNotIn(args[i], inScope); escaped {
					c.errE040StructUninferred(n.P, tp, sd.Name)
					complete = false
				}
			}
			if complete && !clashed {
				for _, f := range n.Fields {
					if ft, ok := fieldT[f.Name]; ok && containsParamType(ft) {
						c.settleNumeric(f.Value, substituteType(ft, sub))
					}
				}
				n.TypeArgs = args
				return ast.StructType{Name: sd.Name, Args: args}
			}
			return nil
		}
		return ast.StructType{Name: sd.Name}
	case *ast.TupleLit:
		hint, _ := c.expectedType.(ast.TupleType)
		c.expectedType = nil
		elems := make([]ast.Type, len(n.Elems))
		for i, e := range n.Elems {
			if i < len(hint.Elems) {
				c.expectedType = hint.Elems[i]
			}
			t := c.checkExpr(e, s)
			c.expectedType = nil
			if t == nil {
				return nil
			}
			elems[i] = t
		}
		c.lanesContained(ast.TupleType{Elems: elems}, n.P)
		return ast.TupleType{Elems: elems}
	case *ast.MapLit:
		c.needCoreMap(n.P)
		var keyType ast.Type = ast.NumberType{}
		var valueType ast.Type = ast.NumberType{}
		if len(n.Entries) > 0 {
			kt := c.checkExpr(n.Entries[0].Key, s)
			vt := c.checkExpr(n.Entries[0].Value, s)
			kt = c.postSettleType(n.Entries[0].Key, kt)
			vt = c.postSettleType(n.Entries[0].Value, vt)
			if kt != nil {
				keyType = kt
			}
			if vt != nil {
				valueType = vt
			}
			if msg := c.mapKeyTypeError(keyType); msg != "" {
				c.errfCode(n.Entries[0].Key.Pos(), "E045", "%s", msg)
			}
		}
		for i, ent := range n.Entries {
			kt, vt := keyType, valueType
			if i > 0 {
				kt = c.checkExpr(ent.Key, s)
				vt = c.checkExpr(ent.Value, s)
			}
			c.settleNumeric(ent.Key, keyType)
			c.settleNumeric(ent.Value, valueType)
			kt = c.postSettleType(ent.Key, kt)
			vt = c.postSettleType(ent.Value, vt)
			if kt != nil && !ast.Equal(kt, keyType) {
				c.errfCode(ent.Key.Pos(), "E045", "map key type %s, expected %s", kt, keyType)
			}
			if vt != nil && !ast.Equal(vt, valueType) {
				c.errfCode(ent.Value.Pos(), "E045", "map value type %s, expected %s", vt, valueType)
			}
		}
		n.KeyType = keyType
		n.ValueType = valueType
		return ast.StructType{Name: "Map", Args: []ast.Type{keyType, valueType}}
	case *ast.FieldAccess:
		if tid, ok := n.Target.(*ast.Ident); ok {
			if _, shadowed := c.identValueBinding(tid.Name, s); !shadowed && c.info.Enums[tid.Name] != nil {
				if vr, ok, _ := c.resolveVariant(n.Field, tid.Name); ok {
					if len(vr.payloads) > 0 {
						en := c.enumHintName(tid.Name)
						c.errfCode(n.P, "E036", "variant %s.%s expects %d payload argument(s); call it as %s.%s(...)",
							en, n.Field, len(vr.payloads), en, n.Field)
						return nil
					}
					c.recordEnumConstruction(n, vr, ast.EnumType{Name: vr.enumName}, nil, c.expectedType)
					return ast.EnumType{Name: vr.enumName}
				}
				c.errfCode(n.P, "E036", "enum %s has no variant %q", c.enumHintName(tid.Name), n.Field)
				return nil
			}
		}
		ft := c.fieldAccessType(n, s, c.checkExpr(n.Target, s))
		c.noteLitField(n, ft, s)
		return ft
	}
	return nil
}

func (c *checker) receiverType(e ast.Expr, s *scope) ast.Type {
	if c.typedRecv != nil && c.typedRecv == e {
		t := c.typedRecvType
		c.typedRecv, c.typedRecvType = nil, nil
		return t
	}
	return c.checkExpr(e, s)
}

func (c *checker) fieldAccessType(n *ast.FieldAccess, s *scope, tt ast.Type) ast.Type {
	if tup, ok := tt.(ast.TupleType); ok {
		idx, err := strconv.Atoi(n.Field)
		if err != nil || idx < 0 {
			c.errfCode(n.P, "E046", "tuple field access requires a numeric index, got %q", n.Field)
			return nil
		}
		if idx >= len(tup.Elems) {
			c.errfCode(n.P, "E046", "tuple has %d elements; index %d is out of range", len(tup.Elems), idx)
			return nil
		}
		return tup.Elems[idx]
	}
	st, ok := tt.(ast.StructType)
	if !ok {
		if tt != nil {
			if tn, ok := collectionNamespace(tt); ok && (len(c.methodsOn(tn)) > 0 || len(stdlibMethodIndex[tn]) > 0) {
				c.errUnknownMethod(n, tn, tt)
				return nil
			}
			c.errfCode(n.P, "E043", "field access on non-struct value of type %s", tt)
		}
		return nil
	}
	sd := c.info.Structs[st.Name]
	if sd == nil {
		c.errfCode(n.P, "E043", "cannot find struct %q in this scope", demangle(st.Name))
		return nil
	}
	c.checkOpaqueAccess(sd, n.P, "access a field of")
	for _, f := range sd.Fields {
		if f.Name == n.Field {
			if len(sd.TypeParams) > 0 && len(st.Args) == len(sd.TypeParams) {
				sub := make(map[string]ast.Type, len(sd.TypeParams))
				for i, tp := range sd.TypeParams {
					sub[tp] = st.Args[i]
				}
				return substituteType(f.Type, sub)
			}
			return f.Type
		}
	}
	declared := make([]string, 0, len(sd.Fields))
	for _, df := range sd.Fields {
		declared = append(declared, df.Name)
	}
	prefix := st.Name + "."
	var methodNames []string
	for key := range c.info.Methods {
		if strings.HasPrefix(key, prefix) {
			methodNames = append(methodNames, key[len(prefix):])
		}
	}
	sort.Strings(methodNames)
	declared = append(declared, methodNames...)
	c.errUnknownMember(n.P, n.FieldPos, st.Name, "field or method", n.Field, declared)
	return nil
}

func (c *checker) requireInteger(p ast.Position, t ast.Type, op string) {
	if t == nil {
		return
	}
	if _, ok := t.(ast.NumberType); !ok {
		c.errfCode(p, "E009", "operator %q requires an integer type, got %s", op, t)
	}
}

func isRuntimeGenericStruct(name string) bool {
	return name == "Map" || name == "MapIter" || name == "Cell"
}

func (c *checker) stampStructTypeArgs(e ast.Expr, dst ast.Type) {
	dStruct, ok := dst.(ast.StructType)
	if !ok || len(dStruct.Args) == 0 {
		return
	}
	call, ok := e.(*ast.Call)
	if !ok || len(call.TypeArgs) > 0 {
		return
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	if sig, ok := c.info.FuncSigs[id.Name]; ok {
		if rs, ok := sig.Result.(ast.StructType); ok && rs.Name == dStruct.Name && len(rs.Args) == 0 {
			call.TypeArgs = dStruct.Args
		}
	}
}

func takeWrittenTypeArgs(n *ast.Call, prior []ast.Type) []ast.Type {
	if prior != nil || !n.TypeArgsWritten || len(n.TypeArgs) == 0 {
		return prior
	}
	return append([]ast.Type(nil), n.TypeArgs...)
}

func destEnumArgs(dst ast.Type, name string, want int) []ast.Type {
	et, ok := dst.(ast.EnumType)
	if !ok || et.Name != name || len(et.Args) != want {
		return nil
	}
	return et.Args
}

func (c *checker) refineCallTypeArgsFromDest(e ast.Expr, dst ast.Type) {
	if e == nil || dst == nil {
		return
	}
	switch x := e.(type) {
	case *ast.Call:
		c.refineSingleCallTypeArgs(x, dst)
	case *ast.IfExpr:
		c.refineCallTypeArgsFromDest(x.Then, dst)
		c.refineCallTypeArgsFromDest(x.Else, dst)
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm != nil {
				c.refineCallTypeArgsFromDest(arm.Body, dst)
			}
		}
	case *ast.TryOp:
	}
}

func isPrimitiveTypeName(name string) bool {
	switch name {
	case "i32", "i64", "u8", "u32", "u64", "f32", "f64", "f64x2", "f32x4", "string", "boolean":
		return true
	}
	return false
}

func (c *checker) refineSingleCallTypeArgs(call *ast.Call, dst ast.Type) {
	id, ok := call.Callee.(*ast.Ident)
	if !ok || c.shadowedGenericCalls[call] {
		return
	}
	fn, isGen := c.info.GenericFuncs[id.Name]
	if !isGen || len(fn.TypeParams) == 0 {
		return
	}
	if len(call.TypeArgs) == 0 {
		sub := make(map[string]ast.Type, len(fn.TypeParams))
		refineParamSubFromDest(fn.ReturnType, dst, sub)
		args := make([]ast.Type, len(fn.TypeParams))
		for i, tp := range fn.TypeParams {
			v, ok := sub[tp]
			if !ok || v == nil {
				return
			}
			args[i] = v
		}
		call.TypeArgs = args
		return
	}
	if len(call.TypeArgs) != len(fn.TypeParams) {
		return
	}
	sub := make(map[string]ast.Type, len(fn.TypeParams))
	for i, tp := range fn.TypeParams {
		sub[tp] = call.TypeArgs[i]
	}
	refineParamSubFromDest(fn.ReturnType, dst, sub)
	for i, tp := range fn.TypeParams {
		if v, ok := sub[tp]; ok {
			call.TypeArgs[i] = v
		}
	}
}

func refineParamSubFromDest(src, dst ast.Type, sub map[string]ast.Type) {
	if src == nil || dst == nil {
		return
	}
	switch s := src.(type) {
	case ast.ParamType:
		existing, has := sub[s.Name]
		if !has {
			sub[s.Name] = dst
			return
		}
		if betterRefinement(existing, dst) {
			sub[s.Name] = dst
		}
	case ast.EnumType:
		dEnum, dok := dst.(ast.EnumType)
		if !dok || dEnum.Name != s.Name {
			return
		}
		n := len(s.Args)
		if len(dEnum.Args) < n {
			n = len(dEnum.Args)
		}
		for i := 0; i < n; i++ {
			refineParamSubFromDest(s.Args[i], dEnum.Args[i], sub)
		}
	case ast.StructType:
		dStruct, dok := dst.(ast.StructType)
		if !dok || dStruct.Name != s.Name {
			return
		}
		n := len(s.Args)
		if len(dStruct.Args) < n {
			n = len(dStruct.Args)
		}
		for i := 0; i < n; i++ {
			refineParamSubFromDest(s.Args[i], dStruct.Args[i], sub)
		}
	case ast.ArrayType:
		if dArr, ok := dst.(ast.ArrayType); ok {
			refineParamSubFromDest(s.Elem, dArr.Elem, sub)
		}
	case ast.SliceType:
		if dSlice, ok := dst.(ast.SliceType); ok {
			refineParamSubFromDest(s.Elem, dSlice.Elem, sub)
		}
	case ast.TupleType:
		dTup, dok := dst.(ast.TupleType)
		if !dok || len(dTup.Elems) != len(s.Elems) {
			return
		}
		for i := range s.Elems {
			refineParamSubFromDest(s.Elems[i], dTup.Elems[i], sub)
		}
	case *ast.FuncType:
		dFn, dok := dst.(*ast.FuncType)
		if !dok || len(dFn.Params) != len(s.Params) {
			return
		}
		for i := range s.Params {
			refineParamSubFromDest(s.Params[i], dFn.Params[i], sub)
		}
		refineParamSubFromDest(s.Result, dFn.Result, sub)
	}
}

func betterRefinement(existing, candidate ast.Type) bool {
	if existing == nil {
		return candidate != nil
	}
	switch e := existing.(type) {
	case ast.EnumType:
		c, ok := candidate.(ast.EnumType)
		if !ok || c.Name != e.Name {
			return false
		}
		merged, ok := mergeEnumInference(e, c)
		return ok && !ast.Equal(e, merged) && ast.Equal(c, merged)
	case ast.StructType:
		c, ok := candidate.(ast.StructType)
		if !ok || c.Name != e.Name {
			return false
		}
		return len(e.Args) < len(c.Args)
	}
	return false
}

func (c *checker) settleNumeric(e ast.Expr, hint ast.Type) {
	if block, ok := e.(*ast.BlockExpr); ok {
		if block.Tail != nil {
			c.settleNumeric(block.Tail, hint)
		}
		return
	}
	if to, ok := e.(*ast.TryOp); ok {
		if src, ok := to.SrcEnum.(ast.EnumType); ok {
			if wrapped, ok := c.tryEnumWithOk(src, hint); ok {
				c.settleNumeric(to.Inner, wrapped)
			}
		}
		if to.Type == nil || isPolymorphicNumeric(to.Type) {
			to.Type = hint
		}
	}
	if fa, ok := e.(*ast.FieldAccess); ok {
		if hn, ok := hint.(ast.NumberType); ok && !hn.Polymorphic {
			if ll := c.litFields[fa]; ll != nil {
				c.fixLitLocal(ll, hn, fa.P)
			}
		}
		c.settleGenericCallByHint(fa, hint)
		return
	}
	if id, ok := e.(*ast.Ident); ok {
		c.settleLitCompositeLocal(id, hint)
	}
	switch hn := hint.(type) {
	case ast.NumberType:
		if hn.Polymorphic {
			return
		}
		c.settleInt(e, hn)
	case ast.FloatType:
		if hn.Polymorphic {
			return
		}
		c.settleFloat(e, hn)
	case ast.ArrayType:
		if al, ok := e.(*ast.ArrayLit); ok {
			if c.elemSettleable(al.ElemType, hn.Elem) {
				al.ElemType = hn.Elem
				for _, el := range al.Elems {
					c.settleNumeric(el, hn.Elem)
				}
			}
		} else if call, ok := e.(*ast.Call); ok {
			c.settleGenericCallByHint(call, hint)
		} else if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
		} else if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.SliceType:
		if al, ok := e.(*ast.ArrayLit); ok {
			if c.elemSettleable(al.ElemType, hn.Elem) {
				al.ElemType = hn.Elem
				for _, el := range al.Elems {
					c.settleNumeric(el, hn.Elem)
				}
			}
		} else if call, ok := e.(*ast.Call); ok {
			c.settleGenericCallByHint(call, hint)
		} else if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
		} else if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.TupleType:
		switch tl := e.(type) {
		case *ast.TupleLit:
			if len(tl.Elems) == len(hn.Elems) {
				for i, el := range tl.Elems {
					c.settleNumeric(el, hn.Elems[i])
				}
			}
		case *ast.Call:
			c.settleGenericCallByHint(tl, hint)
		case *ast.IfExpr:
			c.settleNumeric(tl.Then, hint)
			if tl.Else != nil {
				c.settleNumeric(tl.Else, hint)
			}
		case *ast.MatchExpr:
			for _, arm := range tl.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.StructType:
		if ml, ok := e.(*ast.MapLit); ok && hn.Name == "Map" && len(hn.Args) == 2 {
			for _, ent := range ml.Entries {
				c.settleNumeric(ent.Key, hn.Args[0])
				c.settleNumeric(ent.Value, hn.Args[1])
			}
			if len(ml.Entries) == 0 {
				ml.KeyType = hn.Args[0]
				ml.ValueType = hn.Args[1]
			}
		} else if sl, ok := e.(*ast.StructLit); ok && len(hn.Args) > 0 && !sl.TypeArgsWritten {
			if sd, ok := c.info.Structs[sl.TypeName]; ok && len(sd.TypeParams) == len(hn.Args) {
				sub := map[string]ast.Type{}
				for i, tp := range sd.TypeParams {
					sub[tp] = hn.Args[i]
				}
				fieldT := map[string]ast.Type{}
				for _, f := range sd.Fields {
					fieldT[f.Name] = substituteType(f.Type, sub)
				}
				for _, f := range sl.Fields {
					if expected, present := fieldT[f.Name]; present && expected != nil {
						c.settleNumeric(f.Value, expected)
					}
				}
				sl.TypeArgs = append([]ast.Type{}, hn.Args...)
			}
		} else if call, ok := e.(*ast.Call); ok && isCellNew(call) {
			if hn.Name == "Cell" && len(hn.Args) == 1 && len(call.Args) == 1 && len(call.TypeArgs) == 1 &&
				isPolymorphicNumeric(call.TypeArgs[0]) && unsettledNumericShape(call.Args[0]) {
				c.settleNumeric(call.Args[0], hn.Args[0])
				call.TypeArgs[0] = c.postSettleType(call.Args[0], call.TypeArgs[0])
			}
		} else if call, ok := e.(*ast.Call); ok {
			c.settleGenericCallByHint(call, hint)
		} else if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
		} else if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.EnumType:
		if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
			return
		}
		if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
			return
		}
		construction, resolved := c.refineEnumConstruction(e, hn)
		call, ok := e.(*ast.Call)
		if !ok {
			return
		}
		if !resolved {
			c.settleGenericCallByHint(call, hint)
			return
		}
		for i, a := range call.Args {
			if i < len(construction.Payloads) {
				c.settleNumeric(a, construction.Payloads[i])
			}
		}
	}
}

func (c *checker) settleInt(e ast.Expr, hn ast.NumberType) {
	c.settleIntSigned(e, hn, false)
}

func (c *checker) settleIntSigned(e ast.Expr, hn ast.NumberType, negated bool) {
	width := hn.NormalWidth()
	isUnsigned := !hn.IsSigned()
	switch x := e.(type) {
	case *ast.NumberLit:
		if x.Width == 0 {
			x.Width = width
			x.IsUnsigned = isUnsigned
			c.checkLiteralFits(x, hn, negated)
		}
	case *ast.Ident:
		if ll := c.litIdents[x]; ll != nil {
			c.fixLitLocal(ll, hn, x.P)
		}
	case *ast.Unary:
		if x.Op == "-" {
			c.settleIntSigned(x.Operand, hn, !negated)
			x.CheckedType = c.postSettleType(x.Operand, x.CheckedType)
		} else if x.Op == "+" {
			c.settleIntSigned(x.Operand, hn, negated)
			x.CheckedType = c.postSettleType(x.Operand, x.CheckedType)
		}
	case *ast.Binary:
		switch x.Op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>", "+|", "-|", "*|", "<<|":
			if x.FloatWidth != 0 || x.IsFloat || x.IsStringConcat {
				return
			}
			if x.IntWidth == 0 {
				c.settleInt(x.Left, hn)
				c.settleInt(x.Right, hn)
				x.IntWidth = width
				x.IsUnsigned = isUnsigned
			}
		}
	case *ast.IfExpr:
		if x.Then != nil {
			c.settleInt(x.Then, hn)
		}
		if x.Else != nil {
			c.settleInt(x.Else, hn)
		}
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			c.settleInt(arm.Body, hn)
		}
	case *ast.BlockExpr:
		if x.Tail != nil {
			c.settleInt(x.Tail, hn)
		}
	case *ast.Call:
		c.settleGenericCallByHint(x, hn)
	case *ast.FieldAccess:
		if ll := c.litFields[x]; ll != nil {
			c.fixLitLocal(ll, hn, x.P)
		}
		c.settleGenericCallByHint(x, hn)
	}
}

func unsettledNumericShape(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NumberLit:
		return x.Width == 0 && !x.IsFloat
	case *ast.FloatLit:
		return x.Width == 0
	case *ast.Unary:
		return (x.Op == "-" || x.Op == "+") && unsettledNumericShape(x.Operand)
	case *ast.Binary:
		if x.IntWidth != 0 || x.FloatWidth != 0 || x.IsStringConcat {
			return false
		}
		return unsettledNumericShape(x.Left) && unsettledNumericShape(x.Right)
	case *ast.IfExpr:
		return x.Then != nil && x.Else != nil && unsettledNumericShape(x.Then) && unsettledNumericShape(x.Else)
	}
	return false
}

func (c *checker) elemSettleable(have, want ast.Type) bool {
	if have == nil || want == nil {
		return true
	}
	if _, escaped := paramTypeNotIn(want, c.typeParamsInScope()); escaped {
		return true
	}
	if h, ok := have.(ast.ArrayType); ok {
		if w, ok := want.(ast.ArrayType); ok {
			return c.elemSettleable(h.Elem, w.Elem)
		}
	}
	if h, ok := have.(ast.SliceType); ok {
		if w, ok := want.(ast.SliceType); ok {
			return c.elemSettleable(h.Elem, w.Elem)
		}
	}
	if h, ok := have.(ast.StructType); ok {
		if w, ok := want.(ast.StructType); ok && h.Name == w.Name && len(h.Args) == len(w.Args) && len(h.Args) > 0 {
			for i := range h.Args {
				if !c.elemSettleable(h.Args[i], w.Args[i]) {
					return false
				}
			}
			return true
		}
	}
	switch h := have.(type) {
	case ast.NumberType:
		if h.Polymorphic {
			switch want.(type) {
			case ast.NumberType, ast.FloatType:
				return true
			}
		}
	case ast.FloatType:
		if h.Polymorphic {
			if _, ok := want.(ast.FloatType); ok {
				return true
			}
		}
	}
	return c.assignable(want, have)
}

func isPolymorphicNumeric(t ast.Type) bool {
	if n, ok := t.(ast.NumberType); ok {
		return n.Polymorphic
	}
	if f, ok := t.(ast.FloatType); ok {
		return f.Polymorphic
	}
	return false
}

func (c *checker) settleFloat(e ast.Expr, hf ast.FloatType) {
	width := hf.NormalWidth()
	switch x := e.(type) {
	case *ast.FloatLit:
		if x.Width == 0 {
			x.Width = width
		}
	case *ast.NumberLit:
		if x.Width == 0 && !x.IsFloat {
			if x.ExceedsU64 {
				c.errfCode(x.P, "E047", "literal %s does not fit in any integer type; write it as a float for %s", x.Raw, hf)
			}
			x.IsFloat = true
			x.FloatWidth = width
		}
	case *ast.Unary:
		if x.Op == "-" || x.Op == "+" {
			c.settleFloat(x.Operand, hf)
			x.CheckedType = c.postSettleType(x.Operand, x.CheckedType)
		}
	case *ast.Binary:
		switch x.Op {
		case "+", "-", "*", "/":
			if x.FloatWidth == 0 && !x.IsStringConcat {
				c.settleFloat(x.Left, hf)
				c.settleFloat(x.Right, hf)
				x.FloatWidth = width
			}
		}
	case *ast.IfExpr:
		if x.Then != nil {
			c.settleFloat(x.Then, hf)
		}
		if x.Else != nil {
			c.settleFloat(x.Else, hf)
		}
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			c.settleFloat(arm.Body, hf)
		}
	case *ast.BlockExpr:
		if x.Tail != nil {
			c.settleFloat(x.Tail, hf)
		}
	case *ast.Call:
		if id, ok := x.Callee.(*ast.Ident); ok && !c.shadowedGenericCalls[x] {
			if fn, isGen := c.info.GenericFuncs[id.Name]; isGen {
				for i, p := range fn.Params {
					if i >= len(x.Args) {
						break
					}
					if _, ok := p.Type.(ast.ParamType); ok {
						c.settleFloat(x.Args[i], hf)
					}
				}
				if len(fn.TypeParams) == 1 && len(x.TypeArgs) == 1 {
					x.TypeArgs[0] = hf
				}
			}
		}
	case *ast.FieldAccess:
		c.settleGenericCallByHint(x, hf)
	}
}

func holdsUntypedFloat(t ast.Type) bool {
	switch x := t.(type) {
	case ast.FloatType:
		return x.Polymorphic
	case ast.ArrayType:
		return holdsUntypedFloat(x.Elem)
	case ast.TupleType:
		return slices.ContainsFunc(x.Elems, holdsUntypedFloat)
	case ast.EnumType:
		return slices.ContainsFunc(x.Args, holdsUntypedFloat)
	case ast.StructType:
		return slices.ContainsFunc(x.Args, holdsUntypedFloat)
	}
	return false
}

func f64Floats(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.FloatType:
		if x.Polymorphic {
			return ast.FloatType{Width: 64}
		}
	case ast.ArrayType:
		return ast.ArrayType{Elem: f64Floats(x.Elem)}
	case ast.TupleType:
		elems := make([]ast.Type, len(x.Elems))
		for i, el := range x.Elems {
			elems[i] = f64Floats(el)
		}
		return ast.TupleType{Elems: elems}
	case ast.EnumType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = f64Floats(a)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.StructType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = f64Floats(a)
		}
		return ast.StructType{Name: x.Name, Args: args}
	}
	return t
}

func (c *checker) promoteIntLiterals(e ast.Expr, t ast.Type) {
	if !holdsUntypedFloat(t) {
		return
	}
	switch x := e.(type) {
	case *ast.NumberLit:
		c.settleFloat(x, ast.FloatType{Width: 64})
	case *ast.Call:
		c.settleNumeric(x, f64Floats(t))
	case *ast.Binary:
		switch x.Op {
		case "+", "-", "*", "/":
			if !x.IsStringConcat {
				c.promoteIntLiterals(x.Left, t)
				c.promoteIntLiterals(x.Right, t)
			}
		}
	case *ast.Unary:
		if x.Op == "-" || x.Op == "+" {
			c.promoteIntLiterals(x.Operand, t)
			x.CheckedType = c.postSettleType(x.Operand, x.CheckedType)
		}
	case *ast.ArrayLit:
		if at, ok := t.(ast.ArrayType); ok {
			for _, el := range x.Elems {
				c.promoteIntLiterals(el, at.Elem)
			}
		}
	case *ast.TupleLit:
		if tt, ok := t.(ast.TupleType); ok && len(tt.Elems) == len(x.Elems) {
			for i, el := range x.Elems {
				c.promoteIntLiterals(el, tt.Elems[i])
			}
		}
	case *ast.IfExpr:
		c.promoteIntLiterals(x.Then, t)
		c.promoteIntLiterals(x.Else, t)
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm != nil {
				c.promoteIntLiterals(arm.Body, t)
			}
		}
	case *ast.BlockExpr:
		if x.Tail != nil {
			c.promoteIntLiterals(x.Tail, t)
		}
	}
}

func isVariantCall(c *ast.Call) bool { return c.IsVariantCall }

func (c *checker) postSettleType(e ast.Expr, prior ast.Type) ast.Type {
	if holdsPolymorphicNumeric(prior) {
		if t := c.settledProjectionType(e); t != nil {
			return t
		}
	}
	switch x := e.(type) {
	case *ast.Ident:
		if ll := c.litIdents[x]; ll != nil && ll.fixed {
			return ll.width
		}
		if t := c.litCompositeType(x); t != nil {
			return t
		}
	case *ast.FieldAccess:
		if ll := c.litFields[x]; ll != nil && ll.fixed {
			return ll.width
		}
	case *ast.NumberLit:
		if x.IsFloat {
			return ast.FloatType{Width: x.FloatWidth}
		}
		if x.Width != 0 {
			return ast.NumberType{Width: x.Width, Signed: !x.IsUnsigned}
		}
	case *ast.FloatLit:
		if x.Width != 0 {
			return ast.FloatType{Width: x.Width}
		}
	case *ast.Binary:
		switch x.Op {
		case "==", "!=", "<", "<=", ">", ">=":
			return ast.BoolType{}
		case "+?", "-?", "*?", "/?", "%?", "<<?", ">>?":
			return prior
		}
		if x.IntWidth != 0 {
			return ast.NumberType{Width: x.IntWidth, Signed: !x.IsUnsigned}
		}
		if x.FloatWidth != 0 {
			return ast.FloatType{Width: x.FloatWidth}
		}
	case *ast.Unary:
		return c.postSettleType(x.Operand, prior)
	case *ast.ArrayLit:
		if x.ElemType != nil {
			return ast.ArrayType{Elem: x.ElemType}
		}
	case *ast.TupleLit:
		if tt, ok := prior.(ast.TupleType); ok && len(tt.Elems) == len(x.Elems) {
			out := make([]ast.Type, len(x.Elems))
			for i, el := range x.Elems {
				out[i] = c.postSettleType(el, tt.Elems[i])
			}
			return ast.TupleType{Elems: out}
		}
	case *ast.IfExpr:
		if x.Then != nil {
			if t := c.postSettleType(x.Then, prior); t != nil {
				return t
			}
		}
		if x.Else != nil {
			if t := c.postSettleType(x.Else, prior); t != nil {
				return t
			}
		}
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			if t := c.postSettleType(arm.Body, prior); t != nil {
				return t
			}
		}
	case *ast.BlockExpr:
		if x.Tail != nil {
			if t := c.postSettleType(x.Tail, prior); t != nil {
				return t
			}
		}
	case *ast.Call:
		if isCellNew(x) && len(x.TypeArgs) == 1 {
			return ast.StructType{Name: "Cell", Args: []ast.Type{x.TypeArgs[0]}}
		}
		if et, ok := prior.(ast.EnumType); ok && len(et.Args) > 0 && isVariantCall(x) {
			if id, ok := x.Callee.(*ast.Ident); ok {
				if vr, isVar, _ := c.resolveVariant(id.Name, id.EnumName); isVar {
					if ed := c.info.Enums[vr.enumName]; ed != nil &&
						len(ed.TypeParams) == len(et.Args) &&
						len(x.Args) == len(vr.payloads) {
						priorSub := map[string]ast.Type{}
						for i, tp := range ed.TypeParams {
							priorSub[tp] = et.Args[i]
						}
						newSub := map[string]ast.Type{}
						for i, a := range x.Args {
							declared := vr.payloads[i]
							actual := c.postSettleType(a, substituteType(declared, priorSub))
							if actual != nil {
								c.unifyType(declared, actual, newSub)
							}
						}
						newArgs := make([]ast.Type, len(et.Args))
						complete := true
						for i, tp := range ed.TypeParams {
							if v, ok := newSub[tp]; ok {
								newArgs[i] = v
							} else {
								newArgs[i] = et.Args[i]
							}
							if newArgs[i] == nil {
								complete = false
							}
						}
						if complete {
							return ast.EnumType{Name: et.Name, Args: newArgs}
						}
					}
				}
			}
			return prior
		}
	case *ast.MapLit:
		if st, ok := prior.(ast.StructType); ok && st.Name == "Map" && len(st.Args) == 2 && len(x.Entries) > 0 {
			ent := x.Entries[0]
			newK := c.postSettleType(ent.Key, st.Args[0])
			newV := c.postSettleType(ent.Value, st.Args[1])
			x.KeyType = newK
			x.ValueType = newV
			return ast.StructType{Name: "Map", Args: []ast.Type{newK, newV}}
		}
		if len(x.Entries) == 0 && x.KeyType != nil && x.ValueType != nil {
			return ast.StructType{Name: "Map", Args: []ast.Type{x.KeyType, x.ValueType}}
		}
	case *ast.StructLit:
		if len(x.TypeArgs) > 0 {
			return ast.StructType{Name: x.TypeName, Args: append([]ast.Type{}, x.TypeArgs...)}
		}
	}
	return prior
}

func (c *checker) polymorphicIntDefault(es ...ast.Expr) ast.NumberType {
	for _, e := range es {
		if c.unsettledIntLitExceedsI32(e, false) {
			return ast.NumberType{Width: 64, Signed: true}
		}
	}
	return ast.NumberType{Width: 32, Signed: true}
}

func (c *checker) widenCompositeByLiterals(t ast.Type, es ...ast.Expr) ast.Type {
	switch tt := t.(type) {
	case ast.NumberType:
		if !tt.Polymorphic {
			return nil
		}
		if def := c.polymorphicIntDefault(es...); def.Width == 64 {
			return def
		}
	case ast.TupleType:
		elems := append([]ast.Type(nil), tt.Elems...)
		changed := false
		for i := range elems {
			if w := c.widenCompositeByLiterals(elems[i], tupleElemExprs(es, i)...); w != nil {
				elems[i], changed = w, true
			}
		}
		if changed {
			return ast.TupleType{Elems: elems}
		}
	case ast.ArrayType:
		if w := c.widenCompositeByLiterals(tt.Elem, arrayElemExprs(es)...); w != nil {
			return ast.ArrayType{Elem: w}
		}
	case ast.SliceType:
		if w := c.widenCompositeByLiterals(tt.Elem, arrayElemExprs(es)...); w != nil {
			return ast.SliceType{Elem: w}
		}
	case ast.StructType:
		sd, ok := c.info.Structs[tt.Name]
		if !ok || len(sd.TypeParams) != len(tt.Args) {
			return nil
		}
		args := append([]ast.Type(nil), tt.Args...)
		changed := false
		for i, tp := range sd.TypeParams {
			if w := c.widenCompositeByLiterals(args[i], c.structParamExprs(sd, tp, es)...); w != nil {
				args[i], changed = w, true
			}
		}
		if changed {
			return ast.StructType{Name: tt.Name, Args: args}
		}
	}
	return nil
}

func (c *checker) structParamExprs(sd *ast.StructDecl, tp string, es []ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, f := range sd.Fields {
		var vals []ast.Expr
		for _, e := range valueExprs(es) {
			sl, ok := e.(*ast.StructLit)
			if !ok || sl.TypeName != sd.Name || sl.TypeArgsWritten {
				continue
			}
			for _, lf := range sl.Fields {
				if lf.Name == f.Name {
					vals = append(vals, lf.Value)
				}
			}
		}
		out = append(out, c.paramExprs(f.Type, tp, vals)...)
	}
	return out
}

func (c *checker) paramExprs(t ast.Type, tp string, es []ast.Expr) []ast.Expr {
	if len(es) == 0 {
		return nil
	}
	switch tt := t.(type) {
	case ast.ParamType:
		if tt.Name == tp {
			return es
		}
	case ast.ArrayType:
		return c.paramExprs(tt.Elem, tp, arrayElemExprs(es))
	case ast.SliceType:
		return c.paramExprs(tt.Elem, tp, arrayElemExprs(es))
	case ast.TupleType:
		var out []ast.Expr
		for i, et := range tt.Elems {
			out = append(out, c.paramExprs(et, tp, tupleElemExprs(es, i))...)
		}
		return out
	case ast.StructType:
		sd, ok := c.info.Structs[tt.Name]
		if !ok || len(sd.TypeParams) != len(tt.Args) {
			return nil
		}
		var out []ast.Expr
		for i, inner := range sd.TypeParams {
			out = append(out, c.paramExprs(tt.Args[i], tp, c.structParamExprs(sd, inner, es))...)
		}
		return out
	}
	return nil
}

func valueExprs(es []ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, e := range es {
		switch x := e.(type) {
		case *ast.IfExpr:
			out = append(out, valueExprs([]ast.Expr{x.Then, x.Else})...)
		case *ast.MatchExpr:
			for _, arm := range x.Arms {
				if arm != nil {
					out = append(out, valueExprs([]ast.Expr{arm.Body})...)
				}
			}
		case *ast.BlockExpr:
			out = append(out, valueExprs([]ast.Expr{x.Tail})...)
		case nil:
		default:
			out = append(out, e)
		}
	}
	return out
}

func tupleElemExprs(es []ast.Expr, i int) []ast.Expr {
	var out []ast.Expr
	for _, e := range valueExprs(es) {
		if tl, ok := e.(*ast.TupleLit); ok && i < len(tl.Elems) {
			out = append(out, tl.Elems[i])
		}
	}
	return out
}

func arrayElemExprs(es []ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, e := range valueExprs(es) {
		if al, ok := e.(*ast.ArrayLit); ok {
			out = append(out, al.Elems...)
		}
	}
	return out
}

func (c *checker) widenGenericCallByLiterals(call *ast.Call) ast.Type {
	id, ok := call.Callee.(*ast.Ident)
	if !ok || c.shadowedGenericCalls[call] {
		return nil
	}
	fn, isGen := c.info.GenericFuncs[id.Name]
	if !isGen || len(call.TypeArgs) != len(fn.TypeParams) {
		return nil
	}
	i64 := ast.NumberType{Width: 64, Signed: true}
	changed := false
	for i, tp := range fn.TypeParams {
		if !isPolymorphicNumeric(call.TypeArgs[i]) {
			continue
		}
		var bound []ast.Expr
		wide, pinned := false, false
		for j, p := range fn.Params {
			if j >= len(call.Args) {
				break
			}
			if pt, ok := p.Type.(ast.ParamType); ok && pt.Name == tp {
				if !unsettledNumericShape(call.Args[j]) {
					pinned = true
				}
				bound = append(bound, call.Args[j])
				wide = wide || c.unsettledIntLitExceedsI32(call.Args[j], false)
			} else if typeMentionsParam(p.Type, tp) {
				pinned = true
			}
		}
		if pinned || !wide {
			continue
		}
		for _, a := range bound {
			c.settleInt(a, i64)
		}
		call.TypeArgs[i] = i64
		changed = true
	}
	if !changed {
		return nil
	}
	sub := make(map[string]ast.Type, len(fn.TypeParams))
	for i, tp := range fn.TypeParams {
		sub[tp] = call.TypeArgs[i]
	}
	return c.resolveProj(substituteType(fn.ReturnType, sub))
}

func isCellNew(call *ast.Call) bool {
	id, ok := call.Callee.(*ast.Ident)
	return ok && id.Name == "cell_new"
}

func (c *checker) settleGenericCallByHint(e ast.Expr, hint ast.Type) {
	call, fn, declared := c.genericCallProjection(e)
	if call == nil {
		return
	}
	for i, tp := range fn.TypeParams {
		want, ok := c.genericResultBinding(declared, hint, tp)
		if !ok || !isPolymorphicNumeric(call.TypeArgs[i]) {
			continue
		}
		var bound []ast.Expr
		pinned := false
		for j, p := range fn.Params {
			if j >= len(call.Args) {
				break
			}
			if pt, ok := p.Type.(ast.ParamType); ok && pt.Name == tp {
				if !unsettledNumericShape(call.Args[j]) && !c.openGenericCall(call.Args[j]) {
					pinned = true
				}
				bound = append(bound, call.Args[j])
			} else if typeMentionsParam(p.Type, tp) {
				pinned = true
			}
		}
		if pinned {
			continue
		}
		for _, a := range bound {
			c.settleNumeric(a, want)
		}
		call.TypeArgs[i] = want
	}
}

func (c *checker) openGenericCall(e ast.Expr) bool {
	call, _, _ := c.genericCallProjection(e)
	if call == nil {
		return false
	}
	for _, ta := range call.TypeArgs {
		if isPolymorphicNumeric(ta) {
			return true
		}
	}
	return false
}

func (c *checker) genericCallProjection(e ast.Expr) (*ast.Call, *ast.FuncDecl, ast.Type) {
	switch x := e.(type) {
	case *ast.Call:
		id, ok := x.Callee.(*ast.Ident)
		if !ok || c.shadowedGenericCalls[x] {
			return nil, nil, nil
		}
		fn, isGen := c.info.GenericFuncs[id.Name]
		if !isGen || len(x.TypeArgs) != len(fn.TypeParams) {
			return nil, nil, nil
		}
		return x, fn, fn.ReturnType
	case *ast.FieldAccess:
		call, fn, t := c.genericCallProjection(x.Target)
		if call == nil {
			return nil, nil, nil
		}
		if ft := c.declaredFieldType(t, x.Field); ft != nil {
			return call, fn, ft
		}
	}
	return nil, nil, nil
}

func (c *checker) declaredFieldType(t ast.Type, field string) ast.Type {
	switch tt := t.(type) {
	case ast.TupleType:
		if idx, err := strconv.Atoi(field); err == nil && idx >= 0 && idx < len(tt.Elems) {
			return tt.Elems[idx]
		}
	case ast.StructType:
		sd, ok := c.info.Structs[tt.Name]
		if !ok || len(sd.TypeParams) != len(tt.Args) {
			return nil
		}
		sub := make(map[string]ast.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			sub[tp] = tt.Args[i]
		}
		for _, f := range sd.Fields {
			if f.Name == field {
				return substituteType(f.Type, sub)
			}
		}
	}
	return nil
}

func (c *checker) settledProjectionType(e ast.Expr) ast.Type {
	call, fn, declared := c.genericCallProjection(e)
	if call == nil {
		return nil
	}
	sub := make(map[string]ast.Type, len(fn.TypeParams))
	for i, tp := range fn.TypeParams {
		if isPolymorphicNumeric(call.TypeArgs[i]) && typeMentionsParam(declared, tp) {
			return nil
		}
		sub[tp] = call.TypeArgs[i]
	}
	return c.resolveProj(substituteType(declared, sub))
}

func holdsPolymorphicNumeric(t ast.Type) bool {
	switch tt := t.(type) {
	case ast.TupleType:
		return slices.ContainsFunc(tt.Elems, holdsPolymorphicNumeric)
	case ast.ArrayType:
		return holdsPolymorphicNumeric(tt.Elem)
	case ast.SliceType:
		return holdsPolymorphicNumeric(tt.Elem)
	case ast.StructType:
		return slices.ContainsFunc(tt.Args, holdsPolymorphicNumeric)
	case ast.EnumType:
		return slices.ContainsFunc(tt.Args, holdsPolymorphicNumeric)
	}
	return isPolymorphicNumeric(t)
}

func typeMentionsParam(t ast.Type, tp string) bool {
	return !ast.Equal(substituteType(t, map[string]ast.Type{tp: ast.BoolType{}}), t)
}

func (c *checker) genericResultBinding(declared ast.Type, hint ast.Type, tp string) (ast.Type, bool) {
	sub := map[string]ast.Type{}
	if !c.unifyType(declared, hint, sub) {
		return nil, false
	}
	want, ok := sub[tp]
	if !ok || isPolymorphicNumeric(want) {
		return nil, false
	}
	switch want.(type) {
	case ast.NumberType, ast.FloatType:
		return want, true
	}
	return nil, false
}

func (c *checker) unsettledIntLitExceedsI32(e ast.Expr, negated bool) bool {
	switch x := e.(type) {
	case *ast.NumberLit:
		if x.IsFloat || x.Width != 0 {
			return false
		}
		return ast.IntLitOutOfRange(x, negated, ast.NumberType{Width: 32, Signed: true}) != ""
	case *ast.Unary:
		switch x.Op {
		case "-":
			return c.unsettledIntLitExceedsI32(x.Operand, !negated)
		case "+":
			return c.unsettledIntLitExceedsI32(x.Operand, negated)
		}
	case *ast.Binary:
		if x.IntWidth != 0 || x.FloatWidth != 0 || x.IsStringConcat {
			return false
		}
		switch x.Op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>", "+|", "-|", "*|", "<<|":
			return c.unsettledIntLitExceedsI32(x.Left, false) || c.unsettledIntLitExceedsI32(x.Right, false)
		}
	case *ast.IfExpr:
		return c.unsettledIntLitExceedsI32(x.Then, false) || c.unsettledIntLitExceedsI32(x.Else, false)
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm != nil && c.unsettledIntLitExceedsI32(arm.Body, false) {
				return true
			}
		}
	case *ast.BlockExpr:
		return c.unsettledIntLitExceedsI32(x.Tail, false)
	case *ast.Call, *ast.FieldAccess:
		call, fn, declared := c.genericCallProjection(x)
		if call == nil {
			return false
		}
		for i, p := range fn.Params {
			if i >= len(call.Args) {
				break
			}
			if pt, ok := p.Type.(ast.ParamType); ok && typeMentionsParam(declared, pt.Name) && c.unsettledIntLitExceedsI32(call.Args[i], false) {
				return true
			}
		}
	}
	return false
}

func (c *checker) markLiteralSign(e ast.Expr, negated bool) {
	switch x := e.(type) {
	case *ast.Unary:
		switch x.Op {
		case "-":
			c.markLiteralSign(x.Operand, !negated)
		case "+":
			c.markLiteralSign(x.Operand, negated)
		}
	case *ast.NumberLit:
		if c.negatedLits == nil {
			c.negatedLits = map[*ast.NumberLit]bool{}
		}
		if _, seen := c.negatedLits[x]; !seen {
			c.negatedLits[x] = negated
		}
	}
}

func (c *checker) checkLiteralFits(lit *ast.NumberLit, t ast.NumberType, negated bool) {
	if msg := ast.IntLitOutOfRange(lit, negated, t); msg != "" {
		c.errfCode(lit.P, "E047", "%s", msg)
	}
}

type wideLit struct {
	lit  *ast.NumberLit
	path string
}

func (c *checker) checkUnsettledWideLiterals() {
	for _, w := range c.wideLits {
		lit := w.lit
		if lit.Width != 0 || lit.IsFloat {
			continue
		}
		if msg := ast.IntLitOutOfRange(lit, c.negatedLits[lit], ast.NumberType{Width: 32, Signed: true}); msg != "" {
			c.report(w.path, lit.P, "E047", msg)
		}
	}
}

func commonFloatWidth(lt, rt ast.Type) (ast.FloatType, bool) {
	ln, lOk := lt.(ast.FloatType)
	rn, rOk := rt.(ast.FloatType)
	if !lOk || !rOk {
		return ast.FloatType{}, false
	}
	if ln.Polymorphic && !rn.Polymorphic {
		return rn, true
	}
	if rn.Polymorphic && !ln.Polymorphic {
		return ln, true
	}
	if ln.NormalWidth() != rn.NormalWidth() {
		return ast.FloatType{}, false
	}
	return ln, true
}

func (c *checker) widenIntOperand(slot *ast.Expr, srcT, targetT ast.NumberType) {
	if srcT.Polymorphic || targetT.Polymorphic {
		return
	}
	if srcT.NormalWidth() == targetT.NormalWidth() {
		return
	}
	if nl, ok := (*slot).(*ast.NumberLit); ok && nl.Width == targetT.NormalWidth() {
		return
	}
	pos := (*slot).Pos()
	*slot = &ast.CastExpr{
		P:         pos,
		Inner:     *slot,
		Target:    targetT,
		InnerType: srcT,
	}
}

func commonIntegerWidth(lt, rt ast.Type) (ast.NumberType, bool) {
	ln, lOk := lt.(ast.NumberType)
	rn, rOk := rt.(ast.NumberType)
	if !lOk || !rOk {
		return ast.NumberType{}, false
	}
	if ln.Polymorphic && !rn.Polymorphic {
		return rn, true
	}
	if rn.Polymorphic && !ln.Polymorphic {
		return ln, true
	}
	if ln.IsPointerWidth() || rn.IsPointerWidth() {
		if ln.IsPointerWidth() {
			return ln, true
		}
		return rn, true
	}
	if ln.IsSigned() != rn.IsSigned() {
		return ast.NumberType{}, false
	}
	if ln.NormalWidth() == rn.NormalWidth() {
		return ln, true
	}
	if ln.NormalWidth() > rn.NormalWidth() {
		return ln, true
	}
	return rn, true
}
func (c *checker) requireFloat(p ast.Position, t ast.Type, op string) {
	if t == nil {
		return
	}
	if _, ok := t.(ast.FloatType); !ok {
		c.errfCode(p, "E009", "operator %q requires float, got %s", op, t)
	}
}
func isFloat(t ast.Type) bool {
	_, ok := t.(ast.FloatType)
	return ok
}

var laneBuiltinVerbs = []string{"splat", "of", "lane", "bitmask", "mul_add", "sqrt", "abs", "floor", "round", "min", "max", "lt", "ne", "select", "load", "with"}

func (c *checker) registerLaneBuiltins() {
	i32 := ast.NumberType{}
	for _, lt := range []ast.LanesType{{Width: 64}, {Width: 32}} {
		p := "__" + lt.String() + "_"
		e := ast.FloatType{Width: lt.Width}
		arr := ast.ArrayType{Elem: e}
		of := make([]ast.Type, lt.Lanes())
		for i := range of {
			of[i] = e
		}
		sigs := map[string]*ast.FuncType{
			"splat":   {Params: []ast.Type{e}, Result: lt},
			"of":      {Params: of, Result: lt},
			"lane":    {Params: []ast.Type{lt, i32}, Result: e},
			"bitmask": {Params: []ast.Type{lt}, Result: i32},
			"mul_add": {Params: []ast.Type{lt, lt, lt}, Result: lt},
			"select":  {Params: []ast.Type{lt, lt, lt}, Result: lt},
			"load":    {Params: []ast.Type{arr, i32}, Result: lt},
			"with":    {Params: []ast.Type{arr, i32, lt}, Result: arr},
		}
		for _, v := range []string{"sqrt", "abs", "floor", "round"} {
			sigs[v] = &ast.FuncType{Params: []ast.Type{lt}, Result: lt}
		}
		for _, v := range []string{"min", "max", "lt", "ne"} {
			sigs[v] = &ast.FuncType{Params: []ast.Type{lt, lt}, Result: lt}
		}
		for _, v := range laneBuiltinVerbs {
			c.info.FuncSigs[p+v] = sigs[v]
		}
	}
}

func (c *checker) laneIndexArg(n *ast.Call) {
	id, ok := n.Callee.(*ast.Ident)
	if !ok || len(n.Args) != 2 {
		return
	}
	lanes := int64(0)
	switch id.Name {
	case "__f64x2_lane":
		lanes = 2
	case "__f32x4_lane":
		lanes = 4
	default:
		return
	}
	if lit, ok := n.Args[1].(*ast.NumberLit); ok && !lit.IsFloat && lit.Value >= 0 && lit.Value < lanes {
		return
	}
	c.errfCode(n.Args[1].Pos(), "E038", "argument 2 to %q: the lane index is an integer literal from 0 to %d", id.Name, lanes-1)
}

func lanesInside(t ast.Type) bool {
	switch x := t.(type) {
	case ast.ArrayType:
		return holdsLanes(x.Elem)
	case ast.SliceType:
		return holdsLanes(x.Elem)
	case ast.TupleType:
		return slices.ContainsFunc(x.Elems, holdsLanes)
	case *ast.FuncType:
		return slices.ContainsFunc(x.Params, holdsLanes) || holdsLanes(x.Result)
	case ast.StructType:
		return slices.ContainsFunc(x.Args, holdsLanes)
	case ast.EnumType:
		return slices.ContainsFunc(x.Args, holdsLanes)
	}
	return false
}

func holdsLanes(t ast.Type) bool {
	return isLanes(t) || lanesInside(t)
}

func lanesSignature(params []ast.Param, result ast.Type) bool {
	return isLanes(result) || slices.ContainsFunc(params, func(p ast.Param) bool { return isLanes(p.Type) })
}

func (c *checker) lanesContained(t ast.Type, pos ast.Position) {
	if t != nil && lanesInside(t) {
		c.errfCode(pos, "E085", "%s holds a lane value inside another type; a lane value lives only in a local, a parameter or a result", typeLabel(t))
	}
}

func (c *checker) lanesHeld(t ast.Type, pos ast.Position) {
	if t != nil && holdsLanes(t) {
		c.errfCode(pos, "E085", "a field or payload of type %s holds a lane value; a lane value lives only in a local, a parameter or a result", typeLabel(t))
	}
}

func isLanes(t ast.Type) bool {
	_, ok := t.(ast.LanesType)
	return ok
}

func (c *checker) lanesArith(n *ast.Binary, lt, rt ast.Type) ast.Type {
	if ast.Equal(lt, rt) {
		n.IsLanes = true
		return lt
	}
	what := "numeric"
	if n.Op == "+" {
		what = "numeric or string"
	}
	c.errfCode(n.P, "E009", "operator %q requires matching %s operands, got %s and %s", n.Op, what, lt, rt)
	return lt
}

func (c *checker) lanesCompare(n *ast.Binary, lt, rt ast.Type) {
	t := lt
	if !isLanes(t) {
		t = rt
	}
	c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: a lane value compares lane by lane, through __%s_lt and __%s_ne", t, n.Op, t, t)
}

func isInteger(t ast.Type) bool {
	_, ok := t.(ast.NumberType)
	return ok
}

func isStringLike(t ast.Type) bool {
	switch t.(type) {
	case ast.StringType, ast.StrType:
		return true
	}
	return false
}

func hasHandleDecl(prog *ast.Program) bool {
	for _, fn := range prog.Funcs {
		if fn.Name == "handle" {
			return true
		}
	}
	return false
}

func (c *checker) checkMainSignature(fn *ast.FuncDecl) {
	if len(fn.Params) > 0 {
		c.errfCode(fn.P, "E084", "main takes no parameters; read the command line with args()")
	}
	switch fn.ReturnType.(type) {
	case ast.NumberType, ast.BoolType, ast.VoidType:
	default:
		if !fn.ReturnUnannotated {
			c.errfCode(fn.P, "E084", "main returns an integer, a boolean or void, not %s", fn.ReturnType)
		}
	}
}

func hasMainDecl(prog *ast.Program) bool {
	for _, fn := range prog.Funcs {
		if fn.Name == "main" {
			return true
		}
	}
	return false
}

func (c *checker) checkHandlerStatePairing(prog *ast.Program) bool {
	init := findDecl(prog, "init")
	handle := findDecl(prog, "handle")
	if handle == nil {
		return true
	}
	if init != nil && !initTakesPlatformOnly(init) {
		c.errfCode(init.P, "E075",
			"`init` takes %d parameters; it takes none, or the platform alone", len(init.Params))
		return false
	}
	_, initReturnsState := initReturnShape(init)
	handleTakesState := len(handle.Params) == 3
	switch {
	case handleTakesState && !initReturnsState:
		c.errfCode(handle.P, "E075",
			"handler takes a state parameter, but no `init` produces the state to thread through it")
		return false
	case initReturnsState && !handleTakesState:
		c.errfCode(init.P, "E075",
			"`init` returns a value, but the handler takes no state parameter to thread it through")
		return false
	}
	if shutdown := findDecl(prog, "shutdown"); shutdown != nil {
		switch {
		case len(shutdown.Params) == 2 && !handleTakesState:
			c.errfCode(shutdown.P, "E075",
				"`shutdown` takes a state parameter, but no `init` produces the state to hand it")
			return false
		case len(shutdown.Params) == 1 && handleTakesState:
			c.errfCode(shutdown.P, "E075",
				"`shutdown` takes no state parameter, but the handler threads a state it would drop")
			return false
		}
	}
	return true
}

func findDecl(prog *ast.Program, name string) *ast.FuncDecl {
	for _, fn := range prog.Funcs {
		if fn.Name == name && fn.Receiver == nil {
			return fn
		}
	}
	return nil
}

func initTakesPlatformOnly(init *ast.FuncDecl) bool {
	if len(init.Params) == 0 {
		return true
	}
	return len(init.Params) == 1 && IsPlatformParam(init, init.Params[0])
}

func IsPlatformParam(fn *ast.FuncDecl, p ast.Param) bool {
	var name string
	switch t := p.Type.(type) {
	case ast.ParamType:
		name = t.Name
	case ast.StructType:
		name = t.Name
	default:
		return false
	}
	if name == "platform__Host" {
		return true
	}
	for _, b := range fn.Bounds[name] {
		if b == "platform__Platform" {
			return true
		}
	}
	return false
}

func isServeConfigType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "serve__Config"
}

func initReturnShape(init *ast.FuncDecl) (opts, state bool) {
	if init == nil || isVoidReturn(init.ReturnType) {
		return false, false
	}
	if isServeConfigType(init.ReturnType) {
		return true, false
	}
	if tt, ok := init.ReturnType.(ast.TupleType); ok && len(tt.Elems) == 2 && isServeConfigType(tt.Elems[0]) {
		return true, true
	}
	return false, true
}

const resultHandlerName = "__fern_handle_result"

func resultHandlerShape(handle *ast.FuncDecl) bool {
	isResult := func(t ast.Type) bool {
		e, ok := t.(ast.EnumType)
		if !ok || e.Name != "Result" || len(e.Args) != 2 {
			return false
		}
		st, ok := e.Args[0].(ast.StructType)
		return ok && st.Name == "HttpResponse" && len(st.Args) == 0
	}
	switch len(handle.Params) {
	case 2:
		return isResult(handle.ReturnType)
	case 3:
		tt, ok := handle.ReturnType.(ast.TupleType)
		return ok && len(tt.Elems) == 2 && isResult(tt.Elems[1])
	}
	return false
}

func adaptResultHandler(prog *ast.Program) {
	handle := findDecl(prog, "handle")
	if handle == nil || !resultHandlerShape(handle) {
		return
	}
	handle.Name = resultHandlerName
	pos := handle.P
	resolve := func(bare, mangled string) string {
		if findDecl(prog, bare) != nil {
			return bare
		}
		if findDecl(prog, mangled) != nil {
			return mangled
		}
		return bare
	}
	adapter := "respond"
	var ret ast.Type = ast.StructType{Name: "HttpResponse"}
	result := handle.ReturnType
	if len(handle.Params) == 3 {
		adapter = "respond_with"
		ret = ast.TupleType{Elems: []ast.Type{handle.Params[0].Type, ast.StructType{Name: "HttpResponse"}}}
		result = handle.ReturnType.(ast.TupleType).Elems[1]
	}
	dynErr := isDynErrorType(result.(ast.EnumType).Args[1])
	if dynErr {
		adapter = strings.Replace(adapter, "respond", "respond_error", 1)
	}
	params := make([]ast.Param, len(handle.Params))
	args := make([]ast.Expr, len(handle.Params))
	for i, p := range handle.Params {
		params[i] = ast.Param{Name: p.Name, Type: p.Type}
		args[i] = &ast.Ident{P: pos, Name: p.Name}
	}
	inner := &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: resultHandlerName}, Args: args}
	adapterArgs := []ast.Expr{inner}
	if dynErr {
		adapterArgs = append(adapterArgs, &ast.Ident{P: pos, Name: handle.Params[len(handle.Params)-1].Name})
	}
	call := &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: resolve(adapter, "http__"+adapter)}, Args: adapterArgs}
	prog.Funcs = append(prog.Funcs, &ast.FuncDecl{
		P:          pos,
		Name:       "handle",
		TypeParams: slices.Clone(handle.TypeParams),
		Bounds:     maps.Clone(handle.Bounds),
		BoundArgs:  maps.Clone(handle.BoundArgs),
		Params:     params,
		ReturnType: ret,
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{P: pos, Value: call}}},
	})
}

func isDynErrorType(t ast.Type) bool {
	d, ok := t.(ast.DynTraitType)
	if !ok || len(d.Traits) != 1 {
		return false
	}
	return d.Traits[0] == "error__Error"
}

func desugarTraitParams(prog *ast.Program) map[string]bool {
	traits := map[string]bool{}
	for _, td := range prog.Traits {
		traits[td.Name] = true
	}
	for _, fn := range prog.Funcs {
		if fn.ImplTrait != "" {
			continue
		}
		for i := range fn.Params {
			var name string
			var args []ast.Type
			switch t := fn.Params[i].Type.(type) {
			case ast.StructType:
				name, args = t.Name, t.Args
			case ast.EnumType:
				name, args = t.Name, t.Args
			default:
				continue
			}
			if !traits[name] || slices.Contains(fn.TypeParams, name) {
				continue
			}
			tp := freshTraitParam("T_"+fn.Params[i].Name, fn.TypeParams)
			fn.TypeParams = append(fn.TypeParams, tp)
			if fn.Bounds == nil {
				fn.Bounds = map[string][]string{}
			}
			fn.Bounds[tp] = []string{name}
			if len(args) > 0 {
				if fn.BoundArgs == nil {
					fn.BoundArgs = map[string][][]ast.Type{}
				}
				fn.BoundArgs[tp] = [][]ast.Type{args}
			}
			fn.Params[i].Type = ast.StructType{Name: tp}
		}
	}
	return traits
}

func freshTraitParam(want string, taken []string) string {
	name := want
	for n := 1; slices.Contains(taken, name); n++ {
		name = want + strconv.Itoa(n)
	}
	return name
}

const WasiHandleName = "__fern_wasi_handle"

func synthesiseWasiHandle() *ast.FuncDecl {
	pos := ast.Position{}
	call := &ast.Call{
		P:      pos,
		Callee: &ast.Ident{P: pos, Name: "handle"},
		Args: []ast.Expr{
			&ast.Ident{P: pos, Name: "req"},
			&ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "platform__host"}},
		},
	}
	return &ast.FuncDecl{
		P:          pos,
		Name:       WasiHandleName,
		Params:     []ast.Param{{Name: "req", Type: ast.StructType{Name: "HttpRequest"}}},
		ReturnType: ast.StructType{Name: "HttpResponse"},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{P: pos, Value: call}}},
	}
}

func handlerImportsServe(prog *ast.Program) bool {
	if fn := findDecl(prog, "serve__config"); fn != nil && fn.SourceModule == prog.EntryModule {
		return true
	}
	for path := range prog.DirectImports[prog.EntryModule] {
		path = strings.TrimSuffix(path, ".fern")
		if path == "stdlib://std/serve" || strings.HasSuffix(path, "/std/serve") {
			return true
		}
	}
	for _, imp := range prog.Imports {
		if strings.TrimSuffix(imp.Path, ".fern") == "std/serve" {
			return true
		}
	}
	return false
}

func synthesiseHandleMain(prog *ast.Program, supervised bool) *ast.FuncDecl {
	pos := ast.Position{}
	serveRef := func(name string, args ...ast.Expr) *ast.Call {
		return &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "serve__" + name}, Args: args}
	}
	ident := func(name string) *ast.Ident { return &ast.Ident{P: pos, Name: name} }
	portCall := serveRef("__port_from_env", &ast.StringLit{P: pos, Value: "PORT"}, &ast.NumberLit{P: pos, Value: 8080})
	init := findDecl(prog, "init")
	initCall := &ast.Call{P: pos, Callee: ident("init")}
	if init != nil && len(init.Params) == 1 {
		initCall.Args = []ast.Expr{serveRef("__init_platform")}
	}
	initOpts, initState := initReturnShape(init)

	var stmts []ast.Stmt
	var opts ast.Expr = serveRef("config")
	var state ast.Expr = initCall
	switch {
	case initOpts && initState:
		stmts = append(stmts, &ast.Destructure{P: pos, Names: []string{"__fern_opts", "__fern_state"}, Init: initCall})
		opts, state = ident("__fern_opts"), ident("__fern_state")
	case initOpts:
		opts = initCall
	case init != nil && !initState:
		stmts = append(stmts, &ast.ExprStmt{P: pos, Expr: initCall})
	}
	hasShutdown := findDecl(prog, "shutdown") != nil
	entry := func(supervisedName, singleName string) string {
		if supervised {
			return supervisedName
		}
		return singleName
	}
	var serveCall *ast.Call
	switch {
	case initState && hasShutdown:
		serveCall = serveRef(entry("supervise_with_shutdown", "run_with_shutdown"), portCall, opts, state, ident("handle"), ident("shutdown"))
	case initState:
		serveCall = serveRef(entry("supervise_with", "run_with"), portCall, opts, state, ident("handle"))
	case hasShutdown:
		serveCall = serveRef(entry("supervise_shutdown", "run_shutdown"), portCall, opts, ident("handle"), ident("shutdown"))
	default:
		serveCall = serveRef(entry("supervise", "run"), portCall, opts, ident("handle"))
	}
	stmts = append(stmts, &ast.Return{P: pos, Value: serveCall})
	return &ast.FuncDecl{
		P:                        pos,
		Name:                     "main",
		Params:                   nil,
		ReturnType:               ast.NumberType{Width: 32, Signed: true},
		Body:                     &ast.Block{Stmts: stmts},
		IsSynthesisedHandlerMain: true,
	}
}

func (c *checker) requireBool(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.BoolType{}) {
		c.errfCode(p, "E009", "operator %q requires boolean, got %s", op, t)
	}
}

func (c *checker) castOperandInt(inner ast.Type, e ast.Expr) ast.NumberType {
	if in, ok := inner.(ast.NumberType); ok && !in.Polymorphic {
		return in
	}
	return c.polymorphicIntDefault(e)
}

func isBareNumericLiteral(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NumberLit:
		return true
	case *ast.Unary:
		return x.Op == "-" && isBareNumericLiteral(x.Operand)
	}
	return false
}

func (c *checker) checkVariantQualifier(p ast.Position, qualifier string, ed *ast.EnumDecl) bool {
	if qualifier == "" {
		return false
	}
	if _, qualIsEnum := c.info.Enums[qualifier]; qualIsEnum {
		if qualifier != ed.Name {
			c.errfCode(p, "E029", "variant pattern qualifier %q does not match scrutinee enum %s",
				c.enumHintName(qualifier), c.enumHintName(ed.Name))
			return true
		}
		return false
	}
	if ed.SourceModule != "" && qualifier != ed.SourceModule {
		c.errfCode(p, "E029", "variant pattern qualifier names module %q, but enum %s lives in module %q",
			qualifier, c.enumHintName(ed.Name), ed.SourceModule)
		return true
	}
	return false
}
