package fernsmith

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

const swarmBytes = (2*int(numFeatures) + 2*int(numTypes) + 7) / 8

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

func (g *Generator) on(f feature) bool { return g.feats&(1<<f) != 0 }

func (g *Generator) typeOn(t gtype) bool { return t >= numTypes || g.types&(1<<t) != 0 }

func (g *Generator) swarmed(pool []gtype) []gtype {
	out := pool[:0:0]
	for _, t := range pool {
		if g.typeOn(t) {
			out = append(out, t)
		}
	}
	return out
}
