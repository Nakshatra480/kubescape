package evidence

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

type pathSegment struct {
	key   string
	index int
}

func SplitPath(path string) []pathSegment {
	path = strings.TrimPrefix(path, ".")
	if i := strings.Index(path, "="); i >= 0 {
		path = path[:i]
	}

	var segments []pathSegment
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		seg := pathSegment{index: -1}
		if i := strings.Index(part, "["); i >= 0 {
			seg.key = part[:i]
			tail := part[i+1:]
			if j := strings.Index(tail, "]"); j >= 0 {
				if n, err := strconv.Atoi(tail[:j]); err == nil {
					seg.index = n
				}
			}
		} else {
			seg.key = part
		}
		segments = append(segments, seg)
	}
	return segments
}

func ScalarToString(v any) (string, bool) {
	switch val := v.(type) {
	case nil:
		return "null", true
	case bool:
		return strconv.FormatBool(val), true
	case string:
		if val == "" {
			return `""`, true
		}
		return val, true
	case float64:
		return formatFloat(val), true
	case int:
		return strconv.Itoa(val), true
	case int32:
		return strconv.FormatInt(int64(val), 10), true
	case int64:
		return strconv.FormatInt(val, 10), true
	case uint:
		return strconv.FormatUint(uint64(val), 10), true
	case uint32:
		return strconv.FormatUint(uint64(val), 10), true
	case uint64:
		return strconv.FormatUint(val, 10), true
	case json.Number:
		return val.String(), true
	}
	return namedScalarToString(v)
}

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Kubernetes types wrap scalars in named types (corev1.PullPolicy) and in
// structs with custom marshalling (intstr.IntOrString, resource.Quantity),
// neither of which a plain type switch matches.
func namedScalarToString(v any) (string, bool) {
	if s, ok := v.(fmt.Stringer); ok {
		return s.String(), true
	}
	return reflectScalarToString(reflect.ValueOf(v))
}

func reflectScalarToString(rv reflect.Value) (string, bool) {
	switch rv.Kind() {
	case reflect.Invalid:
		return "", false
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return "null", true
		}
		return reflectScalarToString(rv.Elem())
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true
	case reflect.String:
		if rv.Len() == 0 {
			return `""`, true
		}
		return rv.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return formatFloat(rv.Float()), true
	case reflect.Struct:
		// Quantity and IntOrString declare String on the pointer receiver, so
		// the value held in an interface needs an addressable copy to reach it.
		p := reflect.New(rv.Type())
		p.Elem().Set(rv)
		if s, ok := p.Interface().(fmt.Stringer); ok {
			return s.String(), true
		}
		return "", false
	default:
		return "", false
	}
}

func ResolvePath(obj map[string]any, path string) (string, bool) {
	found, ok := ResolveRawPath(obj, path)
	if !ok {
		return "", false
	}
	return ScalarToString(found)
}

func ResolveRawPath(obj map[string]any, path string) (any, bool) {
	if len(obj) == 0 || path == "" {
		return nil, false
	}
	segments := SplitPath(path)
	if len(segments) == 0 {
		return nil, false
	}

	var cur any = obj
	for _, seg := range segments {
		next, ok := fieldOf(cur, seg.key)
		if !ok {
			return nil, false
		}
		if seg.index >= 0 {
			if next, ok = elementAt(next, seg.index); !ok {
				return nil, false
			}
		}
		cur = next
	}
	return cur, true
}

// Resources reaching a printer are a mix of plain maps and typed Kubernetes
// values: removeData writes containers back as []corev1.Container. The plain
// cases are handled directly and everything else falls back to reflection.
func fieldOf(v any, key string) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		found, ok := m[key]
		return found, ok
	}

	rv := deref(reflect.ValueOf(v))
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		found := rv.MapIndex(reflect.ValueOf(key).Convert(rv.Type().Key()))
		if !found.IsValid() {
			return nil, false
		}
		return found.Interface(), true
	case reflect.Struct:
		i, ok := fieldIndex(rv.Type(), key)
		if !ok {
			return nil, false
		}
		return rv.Field(i).Interface(), true
	default:
		return nil, false
	}
}

func elementAt(v any, index int) (any, bool) {
	if index < 0 {
		return nil, false
	}
	if list, ok := v.([]any); ok {
		if index >= len(list) {
			return nil, false
		}
		return list[index], true
	}

	rv := deref(reflect.ValueOf(v))
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if index >= rv.Len() {
			return nil, false
		}
		return rv.Index(index).Interface(), true
	default:
		return nil, false
	}
}

func deref(rv reflect.Value) reflect.Value {
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}
		}
		rv = rv.Elem()
	}
	return rv
}

var fieldIndexCache sync.Map

func fieldIndex(t reflect.Type, key string) (int, bool) {
	cached, ok := fieldIndexCache.Load(t)
	if !ok {
		cached, _ = fieldIndexCache.LoadOrStore(t, buildFieldIndex(t))
	}
	i, ok := cached.(map[string]int)[strings.ToLower(key)]
	return i, ok
}

func buildFieldIndex(t reflect.Type) map[string]int {
	index := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			if comma := strings.Index(tag, ","); comma >= 0 {
				tag = tag[:comma]
			}
			if tag == "-" {
				continue
			}
			if tag != "" {
				name = tag
			}
		}
		index[strings.ToLower(name)] = i
	}
	return index
}

func TrimValue(path string) string {
	if i := strings.Index(path, "="); i >= 0 {
		return path[:i]
	}
	return path
}

func LastKey(path string) string {
	segments := SplitPath(path)
	if len(segments) == 0 {
		return ""
	}
	return segments[len(segments)-1].key
}

func ParentPath(path string) (string, bool) {
	i := strings.LastIndex(path, ".")
	if i <= 0 {
		return "", false
	}
	return path[:i], true
}
