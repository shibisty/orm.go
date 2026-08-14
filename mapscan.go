package core

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// FillStruct заполняет dest (указатель на структуру с тегами `db:"..."`)
// значениями из m. Используется NoSQL-драйверами (mongodb, redis), которые
// получают данные в виде map[string]any вместо строк SQL-курсора —
// это ровно то, что нужно, чтобы одна и та же модель работала одинаково
// на SQL и NoSQL драйверах.
func FillStruct(dest any, m map[string]any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("core: FillStruct ожидает указатель на структуру, получено %T", dest)
	}
	return fillStructValue(v.Elem(), m)
}

// FillSlice заполняет dest (указатель на срез структур) значениями из maps.
func FillSlice(dest any, maps []map[string]any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("core: FillSlice ожидает указатель на срез, получено %T", dest)
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
			return fmt.Errorf("core: поле %q (db:%q): %w", field.Name, tag, err)
		}
	}
	return nil
}

// assignValue присваивает "сырое" значение (из JSON/BSON) в reflect.Value
// поля, аккуратно обрабатывая типовые несовпадения (например JSON-числа
// всегда float64, а поле в структуре — int64).
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
			return fmt.Errorf("ожидался bool, получено %T", raw)
		}
		field.SetBool(b)
	default:
		if rv.Type().ConvertibleTo(field.Type()) {
			field.Set(rv.Convert(field.Type()))
			return nil
		}
		return fmt.Errorf("не удалось присвоить значение типа %T полю типа %s", raw, field.Type())
	}
	return nil
}

func toInt64(raw any) (int64, error) {
	switch n := raw.(type) {
	case int64:
		return n, nil
	case int32:
		return int64(n), nil
	case int:
		return int64(n), nil
	case float64:
		return int64(n), nil
	case float32:
		return int64(n), nil
	case json.Number:
		return n.Int64()
	default:
		return 0, fmt.Errorf("ожидалось число, получено %T", raw)
	}
}

func toFloat64(raw any) (float64, error) {
	switch n := raw.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	default:
		return 0, fmt.Errorf("ожидалось число, получено %T", raw)
	}
}
