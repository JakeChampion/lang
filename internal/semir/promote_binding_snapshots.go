package semir

import "github.com/jakechampion/lang/internal/ssa"

// promoteBindingSnapshots runs only for explicitly observed places, after
// semantic and initialization verification. Ordinary reads keep their separate
// must-initialized promotion path. No AST or inferred source-name fact is used.
func promoteBindingSnapshots(f *Func, needed map[BindingID]bool, ends map[*ssa.Block][]uint64) {
	type key struct {
		block *ssa.Block
		id    BindingID
	}
	type definition struct {
		block *ssa.Block
		op    *ssa.Op
	}
	type snapshot struct {
		block *ssa.Block
		op    *ssa.Op
		local definition
	}
	last := make(map[key]definition)
	original := make(map[*ssa.Block][]*ssa.Op)
	var snapshots []snapshot
	for _, block := range f.graph.Blocks {
		original[block] = block.Ops
		for _, op := range block.Ops {
			id := BindingID(op.Imm)
			if !needed[id] {
				continue
			}
			switch op.Kind {
			case ssa.OpBindingInit, ssa.OpBindingReplace:
				last[key{block, id}] = definition{block, op}
			case ssa.OpBindingSnapshot:
				snapshots = append(snapshots, snapshot{block, op, last[key{block, id}]})
			}
		}
	}
	// Detach before adding phis: AddPhi can otherwise overwrite the recorded
	// instruction slices. Rebuild once with definitions at their exact positions.
	for _, block := range f.graph.Blocks {
		block.Ops = nil
	}
	absences := make(map[BindingID]ssa.Value)
	var initial []*ssa.Op
	absent := func(id BindingID) ssa.Value {
		if value := absences[id]; value.IsValid() {
			return value
		}
		decl := f.bindings[id-1]
		value := f.addState(f.graph.Entry, ssa.OpStateAbsent, decl.typ, decl.pos)
		initial = append(initial, f.graph.Entry.Ops[len(f.graph.Entry.Ops)-1])
		absences[id] = value
		return value
	}
	wrapped := make(map[*ssa.Op]*ssa.Op)
	present := func(def definition) ssa.Value {
		if op := wrapped[def.op]; op != nil {
			return op.Result
		}
		value := f.addState(def.block, ssa.OpStatePresent, f.bindings[def.op.Imm-1].typ, f.effectPositions[def.op], def.op.Args[0])
		wrapped[def.op] = def.block.Ops[len(def.block.Ops)-1]
		return value
	}
	entries := make(map[key]ssa.Value)
	var readEntry func(*ssa.Block, BindingID) ssa.Value
	readEnd := func(block *ssa.Block, id BindingID) ssa.Value {
		if mask := ends[block]; mask != nil && mask[(id-1)/64]&(uint64(1)<<((id-1)%64)) != 0 {
			return absent(id)
		}
		if def := last[key{block, id}]; def.op != nil {
			return present(def)
		}
		return readEntry(block, id)
	}
	readEntry = func(block *ssa.Block, id BindingID) ssa.Value {
		k := key{block, id}
		if value := entries[k]; value.IsValid() {
			return value
		}
		if len(block.Preds) == 0 {
			return absent(id)
		}
		if len(block.Preds) == 1 {
			value := readEnd(block.Preds[0], id)
			entries[k] = value
			return value
		}
		decl := f.bindings[id-1]
		// These instruction lists are detached and rebuilt below. Append the
		// new phi directly instead of repeatedly scanning/splicing the prefix.
		value := f.addState(block, ssa.OpPhi, decl.typ, decl.pos)
		phi := block.Ops[len(block.Ops)-1]
		entries[k] = value // Seal the identity before following any backedge.
		for _, pred := range block.Preds {
			phi.Args = append(phi.Args, readEnd(pred, id))
		}
		return value
	}
	replacements := make(map[int32]ssa.Value)
	for _, snap := range snapshots {
		value := ssa.Value{}
		if snap.local.op != nil {
			value = present(snap.local)
		} else {
			value = readEntry(snap.block, BindingID(snap.op.Imm))
		}
		replacements[snap.op.Result.ID] = value
	}
	rewrite := func(value ssa.Value) ssa.Value {
		if replacement, ok := replacements[value.ID]; ok {
			return replacement
		}
		return value
	}
	for _, block := range f.graph.Blocks {
		generated := block.Ops
		block.Ops = nil
		for _, op := range generated {
			if op.Kind == ssa.OpPhi {
				block.Ops = append(block.Ops, op)
			}
		}
		for _, op := range original[block] {
			if op.Kind == ssa.OpPhi {
				block.Ops = append(block.Ops, op)
			}
		}
		if block == f.graph.Entry {
			block.Ops = append(block.Ops, initial...)
		}
		for _, op := range original[block] {
			if op.Kind == ssa.OpPhi {
				continue
			}
			if op.Kind == ssa.OpBindingSnapshot {
				delete(f.values, op.Result.ID)
			} else {
				block.Ops = append(block.Ops, op)
			}
			if wrapper := wrapped[op]; wrapper != nil {
				block.Ops = append(block.Ops, wrapper)
			}
		}
		for _, op := range block.Ops {
			for i, arg := range op.Args {
				op.Args[i] = rewrite(arg)
			}
		}
		block.Term.Value = rewrite(block.Term.Value)
		block.Term.Cond = rewrite(block.Term.Cond)
	}
}
