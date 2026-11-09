package orm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
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
	return fillValue(v.Elem(), m)
}

// FillSlice fills dest (a pointer to a slice of structs) with values from maps.
func FillSlice(dest any, maps []map[string]any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("orm: FillSlice expects a pointer to a slice, got %T", dest)
	}
	sliceVal := v.Elem()
	if sliceVal.Type().Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orm: FillSlice expects a pointer to a slice of structs, got %T", dest)
	}
	out := reflect.MakeSlice(sliceVal.Type(), len(maps), len(maps))
	for i, m := range maps {
		if err := fillValue(out.Index(i), m); err != nil {
			return err
		}
	}
	sliceVal.Set(out)
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
	if field.Kind() == reflect.Ptr { // *time.Time, *string: a value is set through a new pointer
		p := reflect.New(field.Type().Elem())
		if err := assignValue(p.Elem(), raw); err != nil {
			return err
		}
		field.Set(p)
		return nil
	}
	if field.Type() == timeType {
		t, err := toTime(raw)
		if err != nil {
			return err
		}
		field.Set(reflect.ValueOf(t))
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

var timeType = reflect.TypeOf(time.Time{})

// toTime reads times as NoSQL drivers return them: RFC 3339 strings (JSON in
// Redis), time.Time, or a value with a Time() method (bson.DateTime).
func toTime(raw any) (time.Time, error) {
	switch v := raw.(type) {
	case time.Time:
		return v, nil
	case string:
		return time.Parse(time.RFC3339Nano, v)
	case interface{ Time() time.Time }:
		return v.Time(), nil
	}
	return time.Time{}, fmt.Errorf("expected a time, got %T", raw)
}
