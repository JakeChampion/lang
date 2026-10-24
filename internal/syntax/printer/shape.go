package printer

import (
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func ASTShape(prog *ast.Program) string {
	var b strings.Builder
	shapeValue(&b, reflect.ValueOf(prog))
	return b.String()
}

var positionType = reflect.TypeOf(ast.Position{})

var synthPos = regexp.MustCompile(`_\d+_\d+`)

func normalizeSynthName(s string) string {
	if !strings.HasPrefix(s, "__") {
		return s
	}
	return synthPos.ReplaceAllString(s, "_#_#")
}

var skippedShapeFields = map[string]bool{"TypeRefs": true, "Comments": true, "BlankLines": true, "MemberLines": true}

func shapeValue(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Invalid:
		b.WriteString("nil")
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		shapeValue(b, v.Elem())
	case reflect.Struct:
		if v.Type() == positionType {
			return
		}
		b.WriteString(v.Type().Name())
		b.WriteByte('{')
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Type == positionType || skippedShapeFields[f.Name] {
				continue
			}
			b.WriteString(f.Name)
			b.WriteByte(':')
			shapeValue(b, v.Field(i))
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	case reflect.Slice, reflect.Array:
		b.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			shapeValue(b, v.Index(i))
			b.WriteByte(' ')
		}
		b.WriteByte(']')
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		byKey := map[string]reflect.Value{}
		for _, k := range v.MapKeys() {
			var kb strings.Builder
			shapeValue(&kb, k)
			keys = append(keys, kb.String())
			byKey[kb.String()] = v.MapIndex(k)
		}
		sort.Strings(keys)
		b.WriteString("map[")
		for _, k := range keys {
			b.WriteString(k)
			b.WriteByte(':')
			shapeValue(b, byKey[k])
			b.WriteByte(' ')
		}
		b.WriteByte(']')
	case reflect.String:
		b.WriteString(strconv.Quote(normalizeSynthName(v.String())))
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		b.WriteString(v.Kind().String())
	}
}

func FirstShapeDiff(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 200
	if lo < 0 {
		lo = 0
	}
	clip := func(s string) string {
		hi := i + 200
		if hi > len(s) {
			hi = len(s)
		}
		return s[lo:hi]
	}
	return "--- as written ---\n" + clip(want) + "\n--- after -fmt ---\n" + clip(got)
}
