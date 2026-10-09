package redis

import (
	"testing"

	"orm"
)

func TestMatchesWhere_SingleEquals(t *testing.T) {
	record := map[string]any{"email": "ivan@example.com", "age": float64(30)}
	wheres := []orm.Condition{{Bool: "AND", Column: "email", Op: "=", Value: "ivan@example.com"}}
	if !matchesWhere(record, wheres) {
		t.Fatal("expected a match on email")
	}
}

func TestMatchesWhere_AndCombination(t *testing.T) {
	record := map[string]any{"email": "ivan@example.com", "age": float64(30)}
	wheres := []orm.Condition{
		{Bool: "AND", Column: "email", Op: "=", Value: "ivan@example.com"},
		{Bool: "AND", Column: "age", Op: ">", Value: float64(18)},
	}
	if !matchesWhere(record, wheres) {
		t.Fatal("expected a match on the AND condition")
	}

	wheres[1].Value = float64(40)
	if matchesWhere(record, wheres) {
		t.Fatal("unexpected match: age=30 is not > 40")
	}
}

func TestMatchesWhere_OrCombination(t *testing.T) {
	record := map[string]any{"email": "other@example.com", "age": float64(30)}
	wheres := []orm.Condition{
		{Bool: "AND", Column: "email", Op: "=", Value: "ivan@example.com"},
		{Bool: "OR", Column: "age", Op: "=", Value: float64(30)},
	}
	if !matchesWhere(record, wheres) {
		t.Fatal("expected a match via OR on age")
	}
}

func TestMatchesWhere_MissingColumn(t *testing.T) {
	record := map[string]any{"email": "ivan@example.com"}
	wheres := []orm.Condition{{Bool: "AND", Column: "nonexistent", Op: "=", Value: "x"}}
	if matchesWhere(record, wheres) {
		t.Fatal("a missing field must not match")
	}
}

func TestMatchLike(t *testing.T) {
	cases := []struct {
		value, pattern string
		want           bool
	}{
		{"ivan@example.com", "%@example.com", true},
		{"ivan@example.com", "%@other.com", false},
		{"ivan@example.com", "ivan%", true},
		{"ivan@example.com", "i_an@example.com", true},
		{"ivan.petrov@example.com", "%.%", true},
	}
	for _, c := range cases {
		if got := matchLike(c.value, c.pattern); got != c.want {
			t.Errorf("matchLike(%q, %q) = %v, want %v", c.value, c.pattern, got, c.want)
		}
	}
}

func TestMatchIn(t *testing.T) {
	list := []any{"a", "b", "c"}
	if !matchIn("b", list) {
		t.Fatal("expected 'b' to match in the list")
	}
	if matchIn("z", list) {
		t.Fatal("did not expect 'z' to match in the list")
	}
}

func TestSortRecords_Numeric(t *testing.T) {
	records := []map[string]any{
		{"id": float64(3)},
		{"id": float64(1)},
		{"id": float64(2)},
	}
	sortRecords(records, []string{"id ASC"})
	if records[0]["id"] != float64(1) || records[1]["id"] != float64(2) || records[2]["id"] != float64(3) {
		t.Fatalf("wrong order after ASC sort: %+v", records)
	}

	sortRecords(records, []string{"id DESC"})
	if records[0]["id"] != float64(3) || records[2]["id"] != float64(1) {
		t.Fatalf("wrong order after DESC sort: %+v", records)
	}
}

func TestApplyLimitOffset(t *testing.T) {
	records := []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}, {"id": 4}, {"id": 5}}

	got := applyLimitOffset(records, 2, 1)
	if len(got) != 2 || got[0]["id"] != 2 || got[1]["id"] != 3 {
		t.Fatalf("wrong result for LIMIT 2 OFFSET 1: %+v", got)
	}

	got = applyLimitOffset(records, 0, 0)
	if len(got) != 5 {
		t.Fatalf("without LIMIT/OFFSET all records should be returned, got %d", len(got))
	}

	got = applyLimitOffset(records, 10, 10)
	if got != nil {
		t.Fatalf("OFFSET past the end of the set should give an empty result, got %+v", got)
	}
}
