package fernsmith

import (
	"math/rand"
	"regexp"
	"strings"
)

// TypeMutant is a generated program with one `let` annotation erased, or one
// `let`, parameter, result or struct-field type replaced (Hephaestus, PLDI
// 2022). Neither kind is guaranteed to check: an erased annotation may have
// been the coercion site that boxed a value into a `dyn`, or the only thing
// pinning a generic variant's type argument, and a replaced one may still
// accept every value that flows into it. What both checkers must do
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

var (
	letAnnotation = regexp.MustCompile(`let [A-Za-z_][A-Za-z0-9_]*: `)
	// A function header up to its parameter list's `(`, past any receiver
	// and type parameters.
	fnHeader = regexp.MustCompile(`function (\([^)]*\) )?[A-Za-z_][A-Za-z0-9_]*(\[[^\]]*\])?\(`)
	// A struct declaration up to its field list's `{ `.
	structHeader = regexp.MustCompile(`struct [A-Za-z_][A-Za-z0-9_]*(\[[^\]]*\])? \{ `)
	fieldName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

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

// signatureTypeSpans returns the [start, end) of each parameter, result and
// struct-field type in src's declarations. A receiver's type is left out:
// replacing it moves the method rather than retyping it.
func signatureTypeSpans(src string) [][2]int {
	var out [][2]int
	for _, m := range fnHeader.FindAllStringIndex(src, -1) {
		end, fields := typedFields(src, m[1], ')')
		out = append(out, fields...)
		if strings.HasPrefix(src[end:], "): ") {
			if ret, ok := typeEnd(src, end+3, " {"); ok {
				out = append(out, [2]int{end + 3, ret})
			}
		}
	}
	for _, m := range structHeader.FindAllStringIndex(src, -1) {
		_, fields := typedFields(src, m[1], '}')
		out = append(out, fields...)
	}
	return out
}

// typedFields reads `name: T, name: T` from i up to the closing byte, and
// returns where that byte is and the span of each T.
func typedFields(src string, i int, closing byte) (int, [][2]int) {
	var out [][2]int
	for i < len(src) && src[i] != closing {
		colon := strings.Index(src[i:], ": ")
		if colon < 0 || !fieldName.MatchString(src[i:i+colon]) {
			return len(src), out
		}
		start := i + colon + 2
		end, ok := typeEnd(src, start, "")
		if !ok {
			return len(src), out
		}
		out = append(out, [2]int{start, end})
		i = end
		if strings.HasPrefix(src[i:], ", ") {
			i += 2
		} else if strings.HasPrefix(src[i:], " }") {
			i++
		}
	}
	return i, out
}

// typeEnd returns where the type starting at i ends: at the first `,` or
// unmatched closing bracket at depth 0, or, given stop, at the first stop at
// depth 0.
func typeEnd(src string, i int, stop string) (int, bool) {
	depth := 0
	for j := i; j < len(src); j++ {
		if depth == 0 && stop != "" && strings.HasPrefix(src[j:], stop) {
			return j, j > i
		}
		switch src[j] {
		case '(', '[':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return trimRight(src, i, j), stop == "" && j > i
			}
			depth--
		case ',':
			if depth == 0 && stop == "" {
				return j, j > i
			}
		case ';', '\n', '{':
			if depth == 0 {
				return j, false
			}
		}
	}
	return len(src), false
}

func trimRight(src string, i, j int) int {
	for j > i && src[j-1] == ' ' {
		j--
	}
	return j
}

// TypeMutants returns up to two erasures and two overwrites of src's `let`
// annotations and up to two overwrites of its declaration types, chosen from
// seed.
func TypeMutants(src string, seed uint64) []TypeMutant {
	lets := letAnnotationSpans(src)
	sigs := signatureTypeSpans(src)
	rng := rand.New(rand.NewSource(int64(seed)))
	var out []TypeMutant
	for k := 0; k < 2 && len(lets) > 0; k++ {
		sp := lets[rng.Intn(len(lets))]
		ann := src[sp[0]:sp[1]]
		// The span starts after `: `, which goes with the type.
		out = append(out, TypeMutant{Erased: true, Ann: ann, Src: src[:sp[0]-2] + src[sp[1]:]})
	}
	overwrite := func(spans [][2]int) {
		for k := 0; k < 2 && len(spans) > 0; k++ {
			sp := spans[rng.Intn(len(spans))]
			ann := src[sp[0]:sp[1]]
			repl := typeMutantRepls[rng.Intn(len(typeMutantRepls))]
			if repl == ann {
				continue
			}
			out = append(out, TypeMutant{Ann: ann, Repl: repl, Src: src[:sp[0]] + repl + src[sp[1]:]})
		}
	}
	overwrite(lets)
	overwrite(sigs)
	return out
}
