package store

import (
	"strings"
	"unicode"
)

// FoldDanish transliterates the Danish letters that Unicode does not treat as
// accented forms of anything, so they survive into a form FTS5 can fold further.
//
// This has to be applied to the query with exactly the same mapping the `fold`
// column on `tasks` uses. The two are a pair: change one and searching quietly
// stops finding half the words in the user's own language.
func FoldDanish(s string) string {
	r := strings.NewReplacer(
		"ø", "o", "Ø", "o",
		"æ", "ae", "Æ", "ae",
		"å", "aa", "Å", "aa",
	)
	return r.Replace(s)
}

// MatchExpr turns what someone typed into the search box into an FTS5 MATCH
// expression.
//
// User input is never passed to FTS5 as-is. The MATCH grammar has its own operators
// (`AND`, `NEAR`, `*`, `^`, `:`, `-`), so a perfectly ordinary task title like
// "budget: q3 - final" parses as a query and fails with a syntax error rather than
// finding anything. Every term is quoted, which makes it a literal.
//
// Each term must match somewhere in the row, and may match either the text as
// written or its folded form — so "gron" finds "grøn" and "grøn" still finds
// itself. Terms are prefix-matched, which is what makes the Cmd+K box feel like it
// is searching as you type.
//
// Returns "" when there is nothing searchable in the input; callers should treat
// that as "no query" rather than as "match everything".
func MatchExpr(query string) string {
	terms := tokenize(query)
	if len(terms) == 0 {
		return ""
	}

	var b strings.Builder
	for i, term := range terms {
		if i > 0 {
			b.WriteString(" AND ")
		}
		folded := FoldDanish(term)
		b.WriteString(`({content description} : `)
		b.WriteString(quote(term))
		b.WriteString(`* OR fold : `)
		b.WriteString(quote(folded))
		b.WriteString(`*)`)
	}
	return b.String()
}

// tokenize splits on everything that is not a letter or a digit. Splitting on
// punctuation rather than whitespace alone means "q3/2026" searches for "q3" and
// "2026" instead of for a term FTS5's own tokenizer would never have indexed.
func tokenize(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			terms = append(terms, f)
		}
	}
	return terms
}

// quote wraps a term as an FTS5 string literal. Inside one, the only character
// with meaning is the double quote, escaped by doubling it.
func quote(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}
