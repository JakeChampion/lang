package gates

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/check/ambient"
	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/oracle/treeshake"
	"github.com/jakechampion/lang/internal/pkg/embed"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

type Warning struct {
	Pos ast.Position
	Msg string
}

func (w Warning) Format(name string) string {
	if w.Pos.Line == 0 {
		return "warning: " + w.Msg
	}
	return fmt.Sprintf("%s:%d:%d: warning: %s", name, w.Pos.Line, w.Pos.Col, w.Msg)
}

func Check(entry string, prog *ast.Program, target string, assets *embed.Set) ([]Warning, error) {
	targetOS, targetArch := "", ""
	if d := platforms.ForTarget(target); d != nil {
		targetOS, targetArch = d.Environment, d.ISA
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{Assets: assets, TargetOS: targetOS, TargetArch: targetArch}); err != nil {
		return nil, err
	}
	info, err := checker.CheckTarget(prog, target)
	if err != nil {
		return nil, err
	}
	constfold.PruneDeadArms(prog)
	var warns []Warning
	if entry != "-" {
		ws, err := Capabilities(entry, prog)
		warns = ws
		if err != nil {
			return warns, err
		}
	}
	if errs := Ambient(entry, prog); errs != nil {
		return warns, errs
	}
	if err := monomorph.Run(prog, info); err != nil {
		return warns, err
	}
	for _, site := range prog.TodoSites {
		warns = append(warns, Warning{Pos: site, Msg: "`todo` stub remaining"})
	}
	if target != "" {
		if errs := Target(entry, prog, info, target, false, ""); errs != nil {
			return warns, errs
		}
	}
	return warns, nil
}

func Ambient(entry string, prog *ast.Program) diag.Errors {
	vs := ambient.Enforce(prog)
	if len(vs) == 0 {
		return nil
	}
	entry = entryModule(entry)
	var errs diag.Errors
	for _, v := range vs {
		ce := &checker.Error{Pos: v.Pos, Msg: v.Message(entry), ErrCode: "E080"}
		if v.FuncModule != "" && v.FuncModule != entry {
			ce.Pos = ast.Position{}
		}
		errs = append(errs, ce)
	}
	return errs
}

func Target(entry string, prog *ast.Program, info *checker.Info, target string, shared bool, export string) diag.Errors {
	if platforms.ForTarget(target) == nil {
		return nil
	}
	if target == "wasm32-wasi-http" {
		kept := prog.Funcs[:0]
		for _, fn := range prog.Funcs {
			if fn.IsSynthesisedHandlerMain {
				continue
			}
			kept = append(kept, fn)
		}
		prog.Funcs = kept
	}
	extras := append([]string(nil), treeshake.DropImplMethods(info)...)
	if shared && export != "" {
		extras = append(extras, strings.Split(export, ",")...)
	}
	if target == "wasm32-wasi-http" {
		extras = append(extras, checker.WasiHandleName, "__method_HeaderMap_append", "__method_HttpResponse_body_bytes")
	}
	for _, fn := range prog.Funcs {
		if fn.ExportIface != "" || fn.ExportWITName != "" || (fn.Async && fn.ImportIface == "") {
			extras = append(extras, fn.Name)
		}
	}
	var preShake []*ast.FuncDecl
	if ast.CoverEnabled {
		preShake = append([]*ast.FuncDecl(nil), prog.Funcs...)
		defer func() { prog.Funcs = preShake }()
	}
	treeshake.Run(prog, info, extras...)
	vs := platforms.Enforce(prog, target)
	if len(vs) == 0 {
		return nil
	}
	entry = entryModule(entry)
	var errs diag.Errors
	for _, v := range vs {
		ce := &checker.Error{Pos: v.Pos, Msg: v.Message(entry), ErrCode: "E066"}
		if v.FuncModule != "" && v.FuncModule != entry {
			ce.Pos = ast.Position{}
		}
		errs = append(errs, ce)
	}
	return errs
}

func entryModule(entry string) string {
	if entry == "-" {
		return modload.SourceEntry
	}
	if abs, err := filepath.Abs(entry); err == nil {
		return abs
	}
	return entry
}
