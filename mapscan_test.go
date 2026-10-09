package orm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"orm"
)

type Doc struct {
	orm.BaseModel
	Name    string  `db:"name"`
	Age     int     `db:"age"`
	Count   uint32  `db:"count"`
	Score   float32 `db:"score"`
	Active  bool    `db:"active"`
	Label   string  `db:"label"`
	Kind    Kind    `db:"kind"`
	Ignored string  `db:"-"`
	NoTag   string
}

type Kind string

func TestFillStructConversions(t *testing.T) {
	var d Doc
	err := orm.FillStruct(&d, map[string]any{
		"id":      float64(5),        // JSON numbers arrive as float64
		"name":    "Ann",             // matching type
		"age":     json.Number("31"), // json.Number → int
		"count":   int64(3),          // int64 → uint32
		"score":   int(2),            // int → float32
		"active":  true,
		"label":   123,     // number → string
		"kind":    "admin", // string → named string type (Convert)
		"missing": "ignored",
		"Ignored": "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Doc{BaseModel: orm.BaseModel{ID: 5}, Name: "Ann", Age: 31, Count: 3, Score: 2, Active: true, Label: "123", Kind: "admin"}
	if d != want {
		t.Fatalf("got %+v\nwant %+v", d, want)
	}

	// nil and missing keys leave the field untouched.
	d2 := Doc{Name: "keep"}
	if err := orm.FillStruct(&d2, map[string]any{"name": nil}); err != nil || d2.Name != "keep" {
		t.Fatalf("nil value: %+v, %v", d2, err)
	}
}

func TestFillStructNumberVariants(t *testing.T) {
	for _, v := range []any{int64(7), int32(7), int8(7), int(7), uint(7), uint64(7), float64(7), float32(7), json.Number("7")} {
		var d Doc
		if err := orm.FillStruct(&d, map[string]any{"age": v, "score": v}); err != nil || d.Age != 7 || d.Score != 7 {
			t.Errorf("%T: %+v, %v", v, d, err)
		}
	}
}

func TestFillStructErrors(t *testing.T) {
	cases := []struct {
		m    map[string]any
		want string
	}{
		{map[string]any{"age": "x"}, "expected a number"},
		{map[string]any{"count": "x"}, "expected a number"},
		{map[string]any{"score": "x"}, "expected a number"},
		{map[string]any{"active": "yes"}, "expected bool"},
		{map[string]any{"age": json.Number("1.5")}, "invalid syntax"},
		{map[string]any{"score": json.Number("abc")}, "invalid syntax"},
	}
	for _, tc := range cases {
		var d Doc
		err := orm.FillStruct(&d, tc.m)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want %q, got %v", tc.m, tc.want, err)
		}
	}

	type Weird struct {
		M map[string]int `db:"m"`
	}
	var w Weird
	if err := orm.FillStruct(&w, map[string]any{"m": []int{1}}); err == nil {
		t.Error("unconvertible type should be an error")
	}
	if err := orm.FillStruct(Doc{}, nil); err == nil {
		t.Error("non-pointer should be an error")
	}
}

func TestFillSlice(t *testing.T) {
	var docs []Doc
	err := orm.FillSlice(&docs, []map[string]any{{"name": "a"}, {"name": "b", "age": 2}})
	if err != nil || len(docs) != 2 || docs[1].Age != 2 {
		t.Fatalf("FillSlice = %+v, %v", docs, err)
	}
	if err := orm.FillSlice(docs, nil); err == nil {
		t.Error("non-pointer should be an error")
	}
	if err := orm.FillSlice(&docs, []map[string]any{{"age": "x"}}); err == nil {
		t.Error("element error should be returned")
	}
}
