// Package filterquery parses the saved-filter language: the small expression
// somebody types to describe a slice of their tasks.
//
//	today & p1
//	#Firma & @regnskab
//	overdue | (today & assigned to: me)
//	7 days & !@venter
//
// It compiles to SQL rather than filtering in Go, so a filter over ten thousand
// tasks is one indexed query instead of ten thousand rows crossing the boundary.
//
// The grammar is deliberately Todoist's, because that is the one people already
// know and the import path from it should not require relearning anything:
//
//	expression := term (('&' | '|' | ',') term)*
//	term       := '!' term | '(' expression ')' | atom
//	atom       := #project | @label | pN | date-word | 'assigned to:' name | text
//
// '&' binds tighter than '|', and ',' is Todoist's spelling of '|'.
package filterquery

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Compiled is a WHERE fragment and its arguments, ready to be dropped into the
// task query. The fragment references the task table as `t`.
type Compiled struct {
	SQL  string
	Args []any
}

// Context is what the query needs to know that is not in the text: who is asking,
// and what "today" means where they are.
type Context struct {
	UserID   string
	Now      time.Time
	Location *time.Location
}

func (c Context) today() time.Time {
	loc := c.Location
	if loc == nil {
		loc = time.UTC
	}
	now := c.Now.In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// Parse compiles a filter expression.
//
// An error names what went wrong in the same terms the person typed, because this
// is a language they are writing by hand in a small box with no autocomplete.
func Parse(query string, ctx Context) (Compiled, error) {
	p := &parser{tokens: tokenize(query), ctx: ctx}
	if len(p.tokens) == 0 {
		return Compiled{}, fmt.Errorf("filteret er tomt")
	}

	node, err := p.parseExpression()
	if err != nil {
		return Compiled{}, err
	}
	if !p.done() {
		return Compiled{}, fmt.Errorf("kunne ikke læse %q", p.peek().text)
	}
	return node, nil
}

// --- tokens -------------------------------------------------------------------

type tokenKind int

const (
	tokWord tokenKind = iota
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
}

func tokenize(query string) []token {
	var out []token
	runes := []rune(query)

	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '&':
			out = append(out, token{kind: tokAnd, text: "&"})
			i++
		case r == '|', r == ',':
			out = append(out, token{kind: tokOr, text: string(r)})
			i++
		case r == '!':
			out = append(out, token{kind: tokNot, text: "!"})
			i++
		case r == '(':
			out = append(out, token{kind: tokLParen, text: "("})
			i++
		case r == ')':
			out = append(out, token{kind: tokRParen, text: ")"})
			i++
		default:
			// A word runs to the next operator. That is what lets "assigned to:
			// anders" and "7 days" arrive as single atoms rather than as three
			// fragments the parser would then have to reassemble.
			start := i
			for i < len(runes) && !isOperator(runes[i]) {
				i++
			}
			text := strings.TrimSpace(string(runes[start:i]))
			if text != "" {
				out = append(out, token{kind: tokWord, text: text})
			}
		}
	}
	return out
}

func isOperator(r rune) bool {
	switch r {
	case '&', '|', ',', '!', '(', ')':
		return true
	}
	return false
}

// --- parser -------------------------------------------------------------------

type parser struct {
	tokens []token
	pos    int
	ctx    Context
}

func (p *parser) done() bool  { return p.pos >= len(p.tokens) }
func (p *parser) peek() token { return p.tokens[p.pos] }
func (p *parser) next() token { t := p.tokens[p.pos]; p.pos++; return t }

// parseExpression handles '|' — the loosest binding, so it is the outermost level.
func (p *parser) parseExpression() (Compiled, error) {
	left, err := p.parseAnd()
	if err != nil {
		return Compiled{}, err
	}
	for !p.done() && p.peek().kind == tokOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return Compiled{}, err
		}
		left = combine("OR", left, right)
	}
	return left, nil
}

func (p *parser) parseAnd() (Compiled, error) {
	left, err := p.parseTerm()
	if err != nil {
		return Compiled{}, err
	}
	for !p.done() && p.peek().kind == tokAnd {
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return Compiled{}, err
		}
		left = combine("AND", left, right)
	}
	return left, nil
}

func (p *parser) parseTerm() (Compiled, error) {
	if p.done() {
		return Compiled{}, fmt.Errorf("filteret slutter midt i et udtryk")
	}

	switch t := p.peek(); t.kind {
	case tokNot:
		p.next()
		inner, err := p.parseTerm()
		if err != nil {
			return Compiled{}, err
		}
		return Compiled{SQL: "NOT (" + inner.SQL + ")", Args: inner.Args}, nil

	case tokLParen:
		p.next()
		inner, err := p.parseExpression()
		if err != nil {
			return Compiled{}, err
		}
		if p.done() || p.peek().kind != tokRParen {
			return Compiled{}, fmt.Errorf("der mangler en afsluttende parentes")
		}
		p.next()
		return Compiled{SQL: "(" + inner.SQL + ")", Args: inner.Args}, nil

	case tokRParen:
		return Compiled{}, fmt.Errorf("uventet )")

	case tokAnd, tokOr:
		return Compiled{}, fmt.Errorf("%q står hvor der skulle stå et søgeord", t.text)

	default:
		p.next()
		return p.atom(t.text)
	}
}

func combine(op string, left, right Compiled) Compiled {
	return Compiled{
		SQL:  "(" + left.SQL + " " + op + " " + right.SQL + ")",
		Args: append(append([]any{}, left.Args...), right.Args...),
	}
}

// --- atoms --------------------------------------------------------------------

// atom compiles one search term.
//
// Every branch produces parameterised SQL. No user text is ever concatenated into
// the statement — this is a language people type, so it is exactly the place where
// string-building would become an injection.
func (p *parser) atom(text string) (Compiled, error) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return Compiled{}, fmt.Errorf("tomt søgeord")
	}

	switch {
	case strings.HasPrefix(lower, "#"):
		return p.project(strings.TrimPrefix(text, "#"))

	case strings.HasPrefix(lower, "@"):
		return p.label(strings.TrimPrefix(text, "@"))

	case strings.HasPrefix(lower, "assigned to:"), strings.HasPrefix(lower, "tildelt:"):
		_, value, _ := strings.Cut(text, ":")
		return p.assignee(strings.TrimSpace(value))

	case lower == "assigned", lower == "tildelt":
		return Compiled{SQL: "t.assignee_id IS NOT NULL"}, nil

	case lower == "no date", lower == "ingen dato", lower == "no due date":
		return Compiled{SQL: "t.due_date IS NULL"}, nil

	case lower == "overdue", lower == "forsinket", lower == "over due":
		return Compiled{
			SQL:  "(t.due_date IS NOT NULL AND t.due_date < ?)",
			Args: []any{p.day(0)},
		}, nil

	case lower == "today", lower == "i dag", lower == "idag":
		return Compiled{SQL: "t.due_date = ?", Args: []any{p.day(0)}}, nil

	case lower == "tomorrow", lower == "i morgen", lower == "imorgen":
		return Compiled{SQL: "t.due_date = ?", Args: []any{p.day(1)}}, nil

	case lower == "yesterday", lower == "i går", lower == "igar":
		return Compiled{SQL: "t.due_date = ?", Args: []any{p.day(-1)}}, nil

	case lower == "recurring", lower == "gentagen", lower == "gentagne":
		return Compiled{SQL: "t.recurrence_rule IS NOT NULL"}, nil

	case lower == "completed", lower == "færdig", lower == "afsluttet":
		return Compiled{SQL: "t.completed_at IS NOT NULL"}, nil

	case lower == "subtask", lower == "underopgave":
		return Compiled{SQL: "t.parent_id IS NOT NULL"}, nil
	}

	// pN — priority. Todoist numbers them the way the interface does, p1 highest.
	if len(lower) == 2 && lower[0] == 'p' && lower[1] >= '1' && lower[1] <= '4' {
		return Compiled{SQL: "t.priority = ?", Args: []any{int(lower[1] - '0')}}, nil
	}

	// "7 days" / "7 dage" — everything due between now and then, which is how
	// Todoist reads it: a window, not a single day.
	if n, unit, ok := cutNumberWord(lower); ok {
		switch unit {
		case "days", "dage", "day", "dag":
			return Compiled{
				SQL:  "(t.due_date IS NOT NULL AND t.due_date >= ? AND t.due_date <= ?)",
				Args: []any{p.day(0), p.day(n)},
			}, nil
		case "weeks", "uger", "week", "uge":
			return Compiled{
				SQL:  "(t.due_date IS NOT NULL AND t.due_date >= ? AND t.due_date <= ?)",
				Args: []any{p.day(0), p.day(n * 7)},
			}, nil
		}
	}

	// An explicit date.
	if _, err := time.Parse("2006-01-02", lower); err == nil {
		return Compiled{SQL: "t.due_date = ?", Args: []any{lower}}, nil
	}

	// Anything left is free text, matched against the task's own words. Falling
	// back to a search rather than an error is what keeps the box forgiving: a
	// filter that finds too much is fixable, one that refuses to run is not.
	return Compiled{
		SQL:  "(t.content LIKE ? OR t.description LIKE ?)",
		Args: []any{"%" + text + "%", "%" + text + "%"},
	}, nil
}

func (p *parser) day(offset int) string {
	return p.ctx.today().AddDate(0, 0, offset).Format("2006-01-02")
}

// project matches by name, case-insensitively, and only among projects the asking
// user can see — the outer query restricts that, but naming a project here must not
// widen it.
func (p *parser) project(name string) (Compiled, error) {
	name = strings.TrimSpace(strings.Trim(name, `"`))
	if name == "" {
		return Compiled{}, fmt.Errorf("#: der mangler et projektnavn")
	}
	return Compiled{
		SQL: `t.project_id IN (
			SELECT id FROM projects WHERE lower(name) = lower(?) AND deleted_at IS NULL)`,
		Args: []any{name},
	}, nil
}

// label matches the asking user's own labels. Labels are personal, so the same word
// on somebody else's task is a different label and must not match.
func (p *parser) label(name string) (Compiled, error) {
	name = strings.TrimSpace(strings.Trim(name, `"`))
	if name == "" {
		return Compiled{}, fmt.Errorf("@: der mangler et etiketnavn")
	}
	return Compiled{
		SQL: `EXISTS (
			SELECT 1 FROM task_labels tl JOIN labels l ON l.id = tl.label_id
			WHERE tl.task_id = t.id AND l.user_id = ? AND lower(l.name) = lower(?))`,
		Args: []any{p.ctx.UserID, name},
	}, nil
}

// assignee resolves "me" to the asking user and anything else to a name or address.
func (p *parser) assignee(who string) (Compiled, error) {
	who = strings.TrimSpace(strings.Trim(who, `"`))
	switch strings.ToLower(who) {
	case "":
		return Compiled{}, fmt.Errorf("assigned to: der mangler et navn")
	case "me", "mig":
		return Compiled{SQL: "t.assignee_id = ?", Args: []any{p.ctx.UserID}}, nil
	case "none", "ingen", "nobody":
		return Compiled{SQL: "t.assignee_id IS NULL"}, nil
	}
	return Compiled{
		SQL: `t.assignee_id IN (
			SELECT id FROM users WHERE lower(email) = lower(?) OR lower(name) = lower(?))`,
		Args: []any{who, who},
	}, nil
}

// cutNumberWord reads "7 days" into (7, "days").
func cutNumberWord(s string) (int, string, bool) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 0 || n > 3650 {
		return 0, "", false
	}
	return n, fields[1], true
}
