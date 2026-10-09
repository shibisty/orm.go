package orm

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// FillStruct fills dest (a pointer to a struct with `db:"..."` tags)
// with values from m. It is used by NoSQL drivers (mongodb, redis), which
// receive data as map[string]any instead of SQL cursor rows;
// this is exactly what lets the same model work the same way
// on SQL and NoSQL drivers.
func FillStruct(dest any, m map[string]any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orm: FillStruct expects a pointer to a struct, got %T", dest)
	}
	return fillStructValue(v.Elem(), m)
}

// FillSlice fills dest (a pointer to a slice of structs) with values from maps.
func FillSlice(dest any, maps []map[string]any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("orm: FillSlice expects a pointer to a slice, got %T", dest)
	}
	sliceVal := v.Elem()
	elemType := sliceVal.Type().Elem()

	out := reflect.MakeSlice(sliceVal.Type(), 0, len(maps))
	for _, m := range maps {
		itemPtr := reflect.New(elemType)
		if err := fillStructValue(itemPtr.Elem(), m); err != nil {
			return err
		}
		out = reflect.Append(out, itemPtr.Elem())
	}
	sliceVal.Set(out)
	return nil
}

func fillStructValue(v reflect.Value, m map[string]any) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		if field.Anonymous {
			if err := fillStructValue(v.Field(i), m); err != nil {
				return err
			}
			continue
		}

		tag := field.Tag.Get("db")
		if tag == "" || tag == "-" {
			continue
		}
		raw, ok := m[tag]
		if !ok || raw == nil {
			continue
		}
		if err := assignValue(v.Field(i), raw); err != nil {
			return fmt.Errorf("orm: field %q (db:%q): %w", field.Name, tag, err)
		}
	}
	return nil
}

// assignValue assigns a "raw" value (from JSON/BSON) to a field's reflect.Value,
// carefully handling common type mismatches (for example, JSON numbers
// are always float64 while the struct field is int64).
func assignValue(field reflect.Value, raw any) error {
	if !field.CanSet() {
		return nil
	}

	rv := reflect.ValueOf(raw)
	if rv.Type().AssignableTo(field.Type()) {
		field.Set(rv)
		return nil
	}

	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := toInt64(raw)
		if err != nil {
			return err
		}
		field.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := toInt64(raw)
		if err != nil {
			return err
		}
		field.SetUint(uint64(n))
	case reflect.Float32, reflect.Float64:
		f, err := toFloat64(raw)
		if err != nil {
			return err
		}
		field.SetFloat(f)
	case reflect.String:
		field.SetString(fmt.Sprint(raw))
	case reflect.Bool:
		b, ok := raw.(bool)
		if !ok {
			return fmt.Errorf("expected bool, got %T", raw)
		}
		field.SetBool(b)
	default:
		if rv.Type().ConvertibleTo(field.Type()) {
			field.Set(rv.Convert(field.Type()))
			return nil
		}
		return fmt.Errorf("cannot assign a value of type %T to a field of type %s", raw, field.Type())
	}
	return nil
}

func toInt64(raw any) (int64, error) {
	if n, ok := raw.(json.Number); ok {
		return n.Int64()
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return int64(rv.Float()), nil
	default:
		return 0, fmt.Errorf("expected a number, got %T", raw)
	}
}

func toFloat64(raw any) (float64, error) {
	if n, ok := raw.(json.Number); ok {
		return n.Float64()
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	default:
		return 0, fmt.Errorf("expected a number, got %T", raw)
	}
}
