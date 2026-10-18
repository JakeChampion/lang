package fernsmith

// Swarm testing (Groce et al., ISSTA 2012): every program is generated
// under its own random subset of features and types instead of one fixed
// distribution over all of them. Features suppress each other — a program
// that may draw thirty types rarely holds two values of the same one, so
// the productions that need a pair (a functional update, a projection
// through a nested value, an alias) almost never fire — and leaving some
// out lets the rest go deeper. Csmith found 42% more distinct crash bugs
// under swarm than under its hand-tuned default.
//
// The mask is the first decisions a program makes, so it is drawn from the
// byte stream like every other one: the fuzzer mutates it, the shrinker
// sees it, and an exhausted stream switches everything off, which is the
// minimal program the exhaustion convention asks for.

// feature is one generator production a swarm mask can switch off.
type feature uint

const (
	featWhile feature = iota
	featForEach
	featLocalFn
	featCow
	featControl
	featCall
	featProjection
	featIfExpr
	featGeneric
	featHelpers
	featNominals
	featNested
	featRecursion
	featClosure
	featTraits
	featGenericDecl
	featOwnRecv
	numFeatures
)

// swarmBytes is how many decisions the mask takes: two bits per feature and
// two per swarmable type.
const swarmBytes = (2*int(numFeatures) + 2*int(numTypes) + 7) / 8

// drawSwarm draws this program's mask. A feature or a type is kept unless
// both of its bits are clear, so three programs in four keep it: several
// features only fire in combination (a nested read needs program-declared
// types, nested fields AND the projection production), and at one in two
// each their conjunction is too rare to matter. tI32 is never masked: main
// returns one, and every other production can fall back to it.
func (g *Generator) drawSwarm() {
	var lo, hi uint64
	for i := 0; i < swarmBytes; i++ {
		b := uint64(g.ch.intN(256))
		if i < 8 {
			lo |= b << (8 * i)
		} else {
			hi |= b << (8 * (i - 8))
		}
	}
	bit := func(n int) uint64 {
		if n < 64 {
			return lo >> n & 1
		}
		return hi >> (n - 64) & 1
	}
	for f := 0; f < int(numFeatures); f++ {
		if bit(2*f)|bit(2*f+1) != 0 {
			g.feats |= 1 << f
		}
	}
	base := 2 * int(numFeatures)
	for t := 0; t < int(numTypes); t++ {
		if bit(base+2*t)|bit(base+2*t+1) != 0 {
			g.types |= 1 << t
		}
	}
	g.types |= 1 << tI32
}

// on reports whether this program may use f.
func (g *Generator) on(f feature) bool { return g.feats&(1<<f) != 0 }

// typeOn reports whether this program may declare a value of builtin type
// t. Program-declared types are gated as a group by featNominals.
func (g *Generator) typeOn(t gtype) bool { return t >= numTypes || g.types&(1<<t) != 0 }

// swarmed filters pool down to the types this program may declare. Never
// empty when pool holds tI32.
func (g *Generator) swarmed(pool []gtype) []gtype {
	out := pool[:0:0]
	for _, t := range pool {
		if g.typeOn(t) {
			out = append(out, t)
		}
	}
	return out
}
