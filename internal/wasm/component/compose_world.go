package component

import "github.com/jakechampion/lang/internal/wasm/componenttype"

// compose_world.go is P2's payoff (docs/WIT-BRING-YOUR-OWN.md): build a real
// component whose import surface is declared from the full decoded WIT world —
// every interface, emitted by componenttype.EmitWorldImports — instead of the
// hand-written minimized type bodies. It reuses the existing
// gComposer.lower/finish suffix wiring unchanged; the only difference is that
// the composer is *seeded* with the world prefix and the world's component
// index layout rather than building a minimal prefix via the ensure* methods.
// The native Compose path is untouched.

// ComposeFromWorld wraps `core` into a component whose top-level imports are
// the full `world` (decoded from the embedded component-type binary), lifting
// `coreExportName` as wasi:cli/run. `imports` are the preview-2 imports the
// core actually uses (the rest of the world is imported but unused).
func ComposeFromWorld(core []byte, world, coreExportName string, imports []gImport) ([]byte, error) {
	w, err := componenttype.DecodeWorld(world)
	if err != nil {
		return nil, err
	}
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
	g.add(imports...)
	return g.finish(core, coreExportName, ""), nil
}

// ComposeStdoutFromWorld wraps a stdout core (imports get-stdout +
// output-stream.blocking-write-and-flush, exports _lang_run) into a
// wasi:cli/run component whose imports are declared from the full WIT world.
func ComposeStdoutFromWorld(core []byte, world string) ([]byte, error) {
	return ComposeFromWorld(core, world, "_lang_run", []gImport{
		{iface: "wasi:io/streams@0.2.0", name: composeBlockWriteName, kind: gMem, params: composeBlockWriteParams},
		{iface: "wasi:cli/stdout@0.2.0", name: "get-stdout", kind: gNoOpt},
	})
}
