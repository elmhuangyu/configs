package configlib

import (
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/goccy/go-yaml"
)

// Bind maps a flat KV tree onto target, which must be a pointer to a struct.
// Mapping is driven exclusively by the `key:"..."` struct tag; the field's Go
// type selects the parsing strategy:
//
//   - string / []byte            -> scalar leaf, exact key match
//   - map[string]T (T a struct)  -> dynamic prefix, the next path segment
//     becomes the map key and the remainder is bound recursively
//   - struct with `key` tags     -> namespace prefix, children are resolved
//     relative to this path
//   - struct without `key` tags  -> structured leaf, the exact key's payload is
//     unmarshalled as YAML into the struct
//
// A `key`-tagged field whose type is none of the above (for example an int or a
// pointer) is rejected with an error rather than silently ignored, so a typo in
// a field type cannot leave a value permanently unset.
func Bind(kvs FlattenKVs, target any) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("target must be a pointer to a struct")
	}
	return bindStruct(kvs, "", v.Elem())
}

func bindStruct(kvs FlattenKVs, currentPrefix string, val reflect.Value) error {
	t := val.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldVal := val.Field(i)

		// Unexported fields cannot be set through reflection; skip them
		// instead of panicking.
		if !fieldVal.CanSet() {
			continue
		}

		keyTag := field.Tag.Get("key")
		if keyTag == "" {
			continue
		}

		fullKey := path.Clean(path.Join(currentPrefix, keyTag))

		switch {
		case field.Type.Kind() == reflect.Map:
			if field.Type.Key().Kind() != reflect.String {
				return fmt.Errorf("field %s: map key must be a string, got %s", field.Name, field.Type.Key())
			}
			// Dynamic prefix: every entry below fullKey/ becomes a map entry.
			if err := bindMap(kvs, fullKey, fieldVal); err != nil {
				return err
			}

		case field.Type.Kind() == reflect.String:
			if raw, ok := kvs[fullKey]; ok {
				data, err := DecryptIfNeeded(raw)
				if err != nil {
					return fmt.Errorf("decrypt %s: %w", fullKey, err)
				}
				fieldVal.SetString(string(data))
			}

		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Uint8:
			if raw, ok := kvs[fullKey]; ok {
				data, err := DecryptIfNeeded(raw)
				if err != nil {
					return fmt.Errorf("decrypt %s: %w", fullKey, err)
				}
				fieldVal.SetBytes(data)
			}

		case field.Type.Kind() == reflect.Struct:
			if hasKeyTags(field.Type) {
				// Namespace prefix: descend into the sub-scope.
				if err := bindStruct(kvs, fullKey, fieldVal); err != nil {
					return err
				}
				continue
			}
			// Structured leaf: unmarshal the exact key's payload as YAML.
			if raw, ok := kvs[fullKey]; ok {
				data, err := DecryptIfNeeded(raw)
				if err != nil {
					return fmt.Errorf("decrypt %s: %w", fullKey, err)
				}
				if err := yaml.Unmarshal(data, fieldVal.Addr().Interface()); err != nil {
					return fmt.Errorf("unmarshal %s: %w", fullKey, err)
				}
			}

		default:
			return fmt.Errorf("field %s: unsupported type %s for key %q", field.Name, field.Type, keyTag)
		}
	}
	return nil
}

func bindMap(kvs FlattenKVs, prefix string, mapVal reflect.Value) error {
	elemType := mapVal.Type().Elem()
	if elemType.Kind() != reflect.Struct {
		return fmt.Errorf("map %s: unsupported element type %s (only structs are supported)", prefix, elemType)
	}

	if mapVal.IsNil() {
		mapVal.Set(reflect.MakeMap(mapVal.Type()))
	}

	// Normalise the prefix so segment matching is unambiguous.
	searchPrefix := prefix
	if !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	// Collect the first path segment of every entry below the prefix.
	subKeys := make(map[string]struct{})
	for k := range kvs {
		if !strings.HasPrefix(k, searchPrefix) {
			continue
		}
		rel := strings.TrimPrefix(k, searchPrefix)
		segments := strings.SplitN(rel, "/", 2)
		if len(segments) > 0 && segments[0] != "" {
			subKeys[segments[0]] = struct{}{}
		}
	}

	for subKey := range subKeys {
		newElem := reflect.New(elemType).Elem()
		subPrefix := path.Join(prefix, subKey)

		if err := bindStruct(kvs, subPrefix, newElem); err != nil {
			return err
		}
		mapVal.SetMapIndex(reflect.ValueOf(subKey), newElem)
	}
	return nil
}

// hasKeyTags reports whether any direct field of t carries a `key` tag, which
// marks the struct as a namespace prefix rather than a structured leaf.
func hasKeyTags(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get("key") != "" {
			return true
		}
	}
	return false
}
