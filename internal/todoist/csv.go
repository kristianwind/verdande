// Package todoist reads and writes Todoist's own CSV template format.
//
// The format is what Todoist's "export as template" produces and what its "import
// from template" accepts, so a file written here opens there and a file exported
// from there opens here. That is the whole point: leaving a hosted product should
// not mean leaving the data behind, and it should not be a one-way door either.
//
// The shape of a Todoist CSV is unusual and worth stating, because it explains most
// of this file. Every row has the same eight columns, but the TYPE column decides
// what a row *is*:
//
//	task   — a task; INDENT gives its depth, so 2 is a sub-task of the task above
//	section — a section heading; only CONTENT is meaningful
//	note   — a comment on the task above it
//	""     — a blank spacer row, which Todoist emits between sections
//
// Nesting is therefore positional: a row's parent is the nearest preceding row with
// a smaller indent. Reordering the rows changes the tree, which is why the writer
// walks the tree in order rather than sorting by anything.
package todoist

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Columns, in Todoist's order. The header has to match exactly or Todoist refuses
// the file.
var header = []string{
	"TYPE", "CONTENT", "DESCRIPTION", "PRIORITY", "INDENT", "AUTHOR", "RESPONSIBLE", "DATE", "DATE_LANG", "TIMEZONE",
}

type Row struct {
	Type        string
	Content     string
	Description string
	Priority    int // Todoist's own numbering: 4 is highest, 1 is none
	Indent      int
	Author      string
	Responsible string
	Date        string
	DateLang    string
	Timezone    string
}

const (
	TypeTask    = "task"
	TypeSection = "section"
	TypeNote    = "note"
)

// Parse reads a Todoist CSV.
//
// It is lenient about what it accepts and strict about what it produces: a file
// that has been through a spreadsheet — reordered columns, a BOM, semicolons
// instead of commas, missing trailing fields — still reads, because that is what
// actually arrives when somebody exports and then opens the file "just to look".
func Parse(r io.Reader) ([]Row, error) {
	reader := csv.NewReader(newBOMStripper(r))
	// Rows genuinely vary in length once a spreadsheet has touched them.
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	records, err := reader.ReadAll()
	if err != nil {
		// Retry with semicolons: that is what Excel writes in a Danish locale, and
		// it is the single most common reason a re-saved export will not read.
		if semi, semiErr := readSemicolon(r); semiErr == nil {
			records = semi
		} else {
			return nil, fmt.Errorf("todoist: read csv: %w", err)
		}
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("todoist: the file is empty")
	}

	index, err := mapColumns(records[0])
	if err != nil {
		return nil, err
	}

	var rows []Row
	for _, record := range records[1:] {
		get := func(name string) string {
			i, ok := index[name]
			if !ok || i >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[i])
		}

		row := Row{
			Type:        strings.ToLower(get("TYPE")),
			Content:     get("CONTENT"),
			Description: get("DESCRIPTION"),
			Author:      get("AUTHOR"),
			Responsible: get("RESPONSIBLE"),
			Date:        get("DATE"),
			DateLang:    get("DATE_LANG"),
			Timezone:    get("TIMEZONE"),
		}
		row.Priority, _ = strconv.Atoi(get("PRIORITY"))
		row.Indent, _ = strconv.Atoi(get("INDENT"))

		// Blank spacer rows carry nothing; keeping them would produce empty tasks.
		if row.Type == "" && row.Content == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readSemicolon(r io.Reader) ([][]string, error) {
	seeker, ok := r.(io.Seeker)
	if !ok {
		return nil, fmt.Errorf("cannot re-read the input")
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	reader := csv.NewReader(newBOMStripper(r))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	return reader.ReadAll()
}

// mapColumns finds each column by name rather than by position, so a file whose
// columns have been reordered still reads.
func mapColumns(headerRow []string) (map[string]int, error) {
	index := map[string]int{}
	for i, name := range headerRow {
		index[strings.ToUpper(strings.TrimSpace(strings.Trim(name, `"`)))] = i
	}
	if _, ok := index["CONTENT"]; !ok {
		return nil, fmt.Errorf("todoist: this does not look like a Todoist export — there is no CONTENT column")
	}
	return index, nil
}

// Write emits a Todoist-compatible CSV.
func Write(w io.Writer, rows []Row) error {
	writer := csv.NewWriter(w)
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{
			row.Type, row.Content, row.Description,
			// Todoist writes an empty priority for a row that is not a task, and
			// so must this: a "4" on a section makes its own importer complain.
			priorityField(row), indentField(row),
			row.Author, row.Responsible, row.Date, row.DateLang, row.Timezone,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func priorityField(row Row) string {
	if row.Type != TypeTask || row.Priority == 0 {
		return ""
	}
	return strconv.Itoa(row.Priority)
}

func indentField(row Row) string {
	if row.Type != TypeTask {
		return ""
	}
	if row.Indent == 0 {
		return "1"
	}
	return strconv.Itoa(row.Indent)
}

// --- priority ------------------------------------------------------------------

// Todoist numbers priorities the opposite way round to how it displays them: the
// CSV says 4 for what the interface calls P1. verdande stores what the interface
// means, so the two have to be converted rather than copied — and getting this
// backwards would silently invert the urgency of every imported task.
func PriorityToVerdande(todoist int) int {
	switch todoist {
	case 4:
		return 1
	case 3:
		return 2
	case 2:
		return 3
	default:
		return 4
	}
}

func PriorityFromVerdande(verdande int) int {
	switch verdande {
	case 1:
		return 4
	case 2:
		return 3
	case 3:
		return 2
	default:
		return 1
	}
}

// --- dates ---------------------------------------------------------------------

// ParseDate reads the DATE column, which Todoist fills with whatever the person
// typed — "tomorrow", "every Monday", "2026-03-15", "15 Mar" — because it stores
// the natural-language string rather than a date.
//
// Only unambiguous machine formats are converted here. Anything else is handed back
// as text for the caller's own natural-language parser, which already understands
// far more of it and does so in the user's timezone.
func ParseDate(value string) (date string, remaining string, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}

	for _, layout := range []string{
		"2006-01-02", "2006-01-02 15:04", "2006-01-02T15:04:05Z07:00",
		"02-01-2006", "02/01/2006", "Jan 2 2006", "2 Jan 2006",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format("2006-01-02"), "", true
		}
	}
	return "", value, false
}

// --- BOM ------------------------------------------------------------------------

// newBOMStripper drops a UTF-8 byte order mark. Excel writes one, and left in place
// it becomes part of the first column's name — so "TYPE" is not found and the file
// reads as having no recognisable columns at all.
func newBOMStripper(r io.Reader) io.Reader {
	return &bomStripper{r: r}
}

type bomStripper struct {
	r       io.Reader
	checked bool
	buf     []byte
}

func (b *bomStripper) Read(p []byte) (int, error) {
	if !b.checked {
		b.checked = true
		head := make([]byte, 3)
		n, err := io.ReadFull(b.r, head)
		if n > 0 {
			head = head[:n]
			if n == 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF {
				head = nil
			}
			b.buf = head
		}
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return 0, err
		}
	}
	if len(b.buf) > 0 {
		n := copy(p, b.buf)
		b.buf = b.buf[n:]
		return n, nil
	}
	return b.r.Read(p)
}
