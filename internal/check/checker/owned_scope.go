package checker

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// ownedBindingScope restores the outer values that a pattern shadows.
type ownedBindingScope struct {
	names                 map[string]bool
	owned, borrowed       map[string]bool
	wasOwned, wasBorrowed map[string]bool
	moved, deferred       map[string]ast.Position
	wasMoved, wasDeferred map[string]ast.Position
}

func shadowOwnedBindings(names []string, owned, borrowed map[string]bool, moved, deferred map[string]ast.Position) *ownedBindingScope {
	s := &ownedBindingScope{
		names: map[string]bool{}, owned: owned, borrowed: borrowed,
		wasOwned: map[string]bool{}, wasBorrowed: map[string]bool{},
		moved: moved, deferred: deferred,
	}
	for _, name := range names {
		if name == "" || name == "_" || s.names[name] {
			continue
		}
		s.names[name] = true
		s.wasOwned[name], s.wasBorrowed[name] = owned[name], borrowed[name]
		delete(owned, name)
		delete(borrowed, name)
	}
	s.wasMoved = s.hideMoves(moved)
	s.wasDeferred = s.hideMoves(deferred)
	return s
}

func (s *ownedBindingScope) binds(path string) bool {
	if s.names[path] {
		return true
	}
	if root, _, ok := strings.Cut(path, "."); ok {
		return s.names[root]
	}
	return false
}

func (s *ownedBindingScope) hideMoves(moves map[string]ast.Position) map[string]ast.Position {
	hidden := map[string]ast.Position{}
	for path, at := range moves {
		if s.binds(path) {
			hidden[path] = at
			delete(moves, path)
		}
	}
	return hidden
}

// settle gives moves, a moved set taken inside the scope, the moves that it
// has once the scope ends.
func (s *ownedBindingScope) settle(moves map[string]ast.Position) {
	s.hideMoves(moves)
	for path, at := range s.wasMoved {
		moves[path] = at
	}
}

func (s *ownedBindingScope) restore() {
	for name := range s.names {
		delete(s.owned, name)
		delete(s.borrowed, name)
		if s.wasOwned[name] {
			s.owned[name] = true
		}
		if s.wasBorrowed[name] {
			s.borrowed[name] = true
		}
	}
	s.hideMoves(s.moved)
	for path, at := range s.wasMoved {
		s.moved[path] = at
	}
	s.hideMoves(s.deferred)
	for path, at := range s.wasDeferred {
		s.deferred[path] = at
	}
}
