package todoist

import (
	"sort"
	"strings"
)

// Project is the shape both sides of the conversion work in: a project with
// sections and a tree of tasks. It is deliberately not verdande's store.Task —
// keeping the CSV layer ignorant of the database is what lets it be tested without
// one, and what makes the round trip a pure function.
type Project struct {
	Name     string
	Sections []Section
	Tasks    []Task
}

type Section struct {
	Name  string
	Tasks []Task
}

type Task struct {
	Content     string
	Description string
	Priority    int // verdande's numbering: 1 highest, 4 none
	Date        string
	Assignee    string
	Author      string
	Comments    []string
	Children    []Task
}

// ToRows flattens a project into CSV rows.
//
// Order is the data here — a task's parent is whichever row above it has a smaller
// indent — so this walks the tree depth-first and never sorts.
func ToRows(p Project) []Row {
	var rows []Row

	appendTask := func(t Task, indent int) {}
	appendTask = func(t Task, indent int) {
		rows = append(rows, Row{
			Type:        TypeTask,
			Content:     t.Content,
			Description: t.Description,
			Priority:    PriorityFromVerdande(t.Priority),
			Indent:      indent,
			Author:      t.Author,
			Responsible: t.Assignee,
			Date:        t.Date,
			DateLang:    dateLang(t.Date),
			Timezone:    "",
		})
		// A comment belongs to the task above it, so notes go immediately after
		// their task and before any of its children.
		for _, note := range t.Comments {
			rows = append(rows, Row{Type: TypeNote, Content: note})
		}
		for _, child := range t.Children {
			// Todoist caps display nesting at four levels; deeper tasks are still
			// written, flattened to the deepest level it will show.
			next := indent + 1
			if next > 4 {
				next = 4
			}
			appendTask(child, next)
		}
	}

	for _, t := range p.Tasks {
		appendTask(t, 1)
	}

	for _, section := range p.Sections {
		// Todoist separates sections with a blank row; its own exports have one
		// and its importer is happier with it.
		rows = append(rows, Row{})
		rows = append(rows, Row{Type: TypeSection, Content: section.Name})
		for _, t := range section.Tasks {
			appendTask(t, 1)
		}
	}
	return rows
}

// FromRows rebuilds the project from CSV rows.
//
// The indent stack is the whole algorithm: each task attaches to the last task seen
// at one level shallower, and a task at indent 1 starts a new top-level branch.
func FromRows(rows []Row, projectName string) Project {
	p := Project{Name: projectName}

	// Pointers into the tree at each depth, so a child can be appended to whoever
	// is currently open one level up.
	var stack []*Task
	var currentSection *Section

	// The most recently added task, which is what a note attaches to.
	var lastTask *Task

	addTask := func(t Task, indent int) *Task {
		if indent < 1 {
			indent = 1
		}
		if indent > len(stack)+1 {
			// A file that jumps from indent 1 to indent 3 — possible after
			// hand-editing. Treat it as one level deeper than what is open,
			// rather than dropping the task.
			indent = len(stack) + 1
		}
		stack = stack[:indent-1]

		if indent == 1 {
			if currentSection != nil {
				currentSection.Tasks = append(currentSection.Tasks, t)
				added := &currentSection.Tasks[len(currentSection.Tasks)-1]
				stack = append(stack, added)
				return added
			}
			p.Tasks = append(p.Tasks, t)
			added := &p.Tasks[len(p.Tasks)-1]
			stack = append(stack, added)
			return added
		}

		parent := stack[indent-2]
		parent.Children = append(parent.Children, t)
		added := &parent.Children[len(parent.Children)-1]
		stack = append(stack, added)
		return added
	}

	for _, row := range rows {
		switch row.Type {
		case TypeSection:
			p.Sections = append(p.Sections, Section{Name: row.Content})
			currentSection = &p.Sections[len(p.Sections)-1]
			// A new section resets the tree: nothing above it can be a parent of
			// anything below.
			stack = nil
			lastTask = nil

		case TypeTask:
			if strings.TrimSpace(row.Content) == "" {
				continue
			}
			lastTask = addTask(Task{
				Content:     row.Content,
				Description: row.Description,
				Priority:    PriorityToVerdande(row.Priority),
				Date:        row.Date,
				Assignee:    row.Responsible,
				Author:      row.Author,
			}, row.Indent)

		case TypeNote:
			if lastTask != nil && strings.TrimSpace(row.Content) != "" {
				lastTask.Comments = append(lastTask.Comments, row.Content)
			}
		}
	}
	return p
}

// dateLang is the language Todoist should parse the DATE string in. Only set when
// there is a date to parse; an empty column with a language beside it confuses its
// importer.
func dateLang(date string) string {
	if strings.TrimSpace(date) == "" {
		return ""
	}
	return "en"
}

// SortSections puts sections in a stable order for export, so two exports of an
// unchanged project produce identical bytes.
func SortSections(p *Project) {
	sort.SliceStable(p.Sections, func(i, j int) bool { return p.Sections[i].Name < p.Sections[j].Name })
}
