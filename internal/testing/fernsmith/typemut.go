package fernsmith

import (
	"math/rand"
	"regexp"
	"strings"
)

type TypeMutant struct {
	Erased bool
	Ann    string
	Repl   string
	Src    string
}

var typeMutantRepls = []string{"i32", "i64", "u8", "boolean", "string", "i32[]", "Option[i32]"}

var (
	letAnnotation = regexp.MustCompile(`let [A-Za-z_][A-Za-z0-9_]*: `)
	fnHeader      = regexp.MustCompile(`function (\([^)]*\) )?[A-Za-z_][A-Za-z0-9_]*(\[[^\]]*\])?\(`)
	structHeader  = regexp.MustCompile(`struct [A-Za-z_][A-Za-z0-9_]*(\[[^\]]*\])? \{ `)
	typeArgCall   = regexp.MustCompile(`(^|[^A-Za-z0-9_.])(id|pick)\[`)
	fieldName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

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

func typeArgSpans(src string) [][2]int {
	var out [][2]int
	for _, m := range typeArgCall.FindAllStringIndex(src, -1) {
		depth := 1
		for i := m[1]; i < len(src); i++ {
			if src[i] == '[' {
				depth++
			} else if src[i] == ']' {
				depth--
				if depth == 0 {
					if i+1 < len(src) && src[i+1] == '(' {
						out = append(out, [2]int{m[1], i})
					}
					break
				}
			}
		}
	}
	return out
}

func TypeMutants(src string, seed uint64) []TypeMutant {
	lets := letAnnotationSpans(src)
	sigs := signatureTypeSpans(src)
	rng := rand.New(rand.NewSource(int64(seed)))
	var out []TypeMutant
	for k := 0; k < 2 && len(lets) > 0; k++ {
		sp := lets[rng.Intn(len(lets))]
		ann := src[sp[0]:sp[1]]
		out = append(out, TypeMutant{Erased: true, Ann: ann, Src: src[:sp[0]-2] + src[sp[1]:]})
	}
	targs := typeArgSpans(src)
	for k := 0; k < 2 && len(targs) > 0; k++ {
		sp := targs[rng.Intn(len(targs))]
		out = append(out, TypeMutant{Erased: true, Ann: src[sp[0]:sp[1]], Src: src[:sp[0]-1] + src[sp[1]+1:]})
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
