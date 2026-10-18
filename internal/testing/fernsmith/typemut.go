package fernsmith

import (
	"math/rand"
	"regexp"
	"strings"
)

// TypeMutant is a generated program with one `let` annotation erased or
// replaced (Hephaestus, PLDI 2022). Neither kind is guaranteed to check: an
// erased annotation may have been the coercion site that boxed a value into
// a `dyn`, or the only thing pinning a generic variant's type argument, and a
// replaced one may still accept the initialiser. What both checkers must do
// is reach the same verdict on it, and an erased program that checks must
// compute what the original did.
type TypeMutant struct {
	Erased bool   // true: the annotation is gone; false: Repl replaces it
	Ann    string // the annotation's type as written
	Repl   string
	Src    string
}

// typeMutantRepls are the types an overwrite substitutes. Each is in scope in
// every generated program.
var typeMutantRepls = []string{"i32", "i64", "u8", "boolean", "string", "i32[]", "Option[i32]"}

var letAnnotation = regexp.MustCompile(`let [A-Za-z_][A-Za-z0-9_]*: `)

// letAnnotationSpans returns the [start, end) of each `let` annotation's
// type in src.
func letAnnotationSpans(src string) [][2]int {
	var out [][2]int
	for _, m := range letAnnotation.FindAllStringIndex(src, -1) {
		depth := 0
	scan:
		for i := m[1]; i < len(src); i++ {
			switch src[i] {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			case ';', '\n':
				break scan
			case ' ':
				if depth == 0 && strings.HasPrefix(src[i:], " = ") {
					out = append(out, [2]int{m[1], i})
					break scan
				}
			}
		}
	}
	return out
}

// TypeMutants returns up to two erasures and two overwrites of src's `let`
// annotations, chosen from seed.
func TypeMutants(src string, seed uint64) []TypeMutant {
	spans := letAnnotationSpans(src)
	if len(spans) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(int64(seed)))
	var out []TypeMutant
	for k := 0; k < 2; k++ {
		sp := spans[rng.Intn(len(spans))]
		ann := src[sp[0]:sp[1]]
		// The span starts after `: `, which goes with the type.
		out = append(out, TypeMutant{Erased: true, Ann: ann, Src: src[:sp[0]-2] + src[sp[1]:]})
	}
	for k := 0; k < 2; k++ {
		sp := spans[rng.Intn(len(spans))]
		ann := src[sp[0]:sp[1]]
		repl := typeMutantRepls[rng.Intn(len(typeMutantRepls))]
		if repl == ann {
			continue
		}
		out = append(out, TypeMutant{Ann: ann, Repl: repl, Src: src[:sp[0]] + repl + src[sp[1]:]})
	}
	return out
}
