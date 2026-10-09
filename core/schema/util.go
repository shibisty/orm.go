package schema

import "strings"

func firstOr(vals []int, def int) int {
	if len(vals) > 0 {
		return vals[0]
	}
	return def
}

func firstOrStr(vals []string, def string) string {
	if len(vals) > 0 {
		return vals[0]
	}
	return def
}

func trimIDSuffix(s string) string {
	return strings.TrimSuffix(s, "_id")
}

// pluralize/singularize implement naive pluralization of English words,
// sufficient for Constrained()/ForeignIDFor() in typical cases
// (company -> companies, category -> categories, tag -> tags).
// Irregular plurals (person -> people, etc.) are not supported;
// pass the table name explicitly in those cases.
func pluralize(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && len(s) > 1 && !isVowel(rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	default:
		return s + "s"
	}
}

func singularize(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses"), strings.HasSuffix(s, "xes"),
		strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s"):
		return s[:len(s)-1]
	default:
		return s
	}
}

func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	default:
		return false
	}
}

// sqlQuoteLiteral escapes single quotes for DEFAULT literals.
func sqlQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func quoteAll(d Dialect, cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = d.Quote(c)
	}
	return out
}
