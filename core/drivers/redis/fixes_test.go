package redis

import (
	"encoding/json"
	"testing"

	"orm"
)

// JSON yields ids as float64: 1e6 must not turn into "1e+06".
func TestIDString(t *testing.T) {
	cases := map[any]string{
		float64(1000000):   "1000000",
		float64(42):        "42",
		float64(123456789): "123456789",
		json.Number("7"):   "7",
		int64(5):           "5",
		"abc":              "abc",
	}
	for in, want := range cases {
		if got := idString(in); got != want {
			t.Errorf("idString(%#v) = %q, want %q", in, got, want)
		}
	}
}

func TestNotInScalarInAndCase(t *testing.T) {
	rec := map[string]any{"status": "active", "n": float64(3)}
	cond := func(op string, v any) []orm.Condition {
		return []orm.Condition{{Bool: "AND", Column: "status", Op: op, Value: v}}
	}
	if !matchesWhere(rec, cond("not in", []string{"banned"})) {
		t.Error("NOT IN should match a value outside the list")
	}
	if matchesWhere(rec, cond("NOT IN", []string{"active"})) {
		t.Error("NOT IN should not match a value in the list")
	}
	if !matchesWhere(rec, cond("in", "active")) {
		t.Error("IN with a scalar should compare equal")
	}
	if !matchesWhere(rec, cond("IN", [2]string{"x", "active"})) {
		t.Error("IN with an array should work")
	}
	if !matchesWhere(map[string]any{"n": float64(3)}, []orm.Condition{{Column: "n", Op: "in", Value: []int{1, 3}}}) {
		t.Error("IN should compare numbers across types")
	}
}

func TestUnsupportedOperatorIsAnError(t *testing.T) {
	c := &Conn{}
	_, err := c.scanMatching(nil, "t", []orm.Condition{{Bool: "AND", Column: "a", Op: "BETWEEN", Value: 1}})
	if err == nil {
		t.Fatal("unknown operator should be an error, not an empty result")
	}
}
