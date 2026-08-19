package store

import (
	"context"
	"strings"
	"testing"
)

// The point of the whole thing: a #tag in a note is the same tag as in a task.
// Not a second kind that looks alike and has to be kept in step by hand.
func TestATagInANoteIsTheSameTagAsInATask(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	n := &Note{
		CreatedBy: userID,
		Body:      "Mødet med #Firma gik fint. Følg op sammen med #Regnskab.",
	}
	if err := db.SaveNote(ctx, n); err != nil {
		t.Fatal(err)
	}

	var projects []string
	for _, l := range n.Links {
		if l.Kind == "project" {
			projects = append(projects, l.TargetID)
		}
	}
	if len(projects) != 2 {
		t.Fatalf("read %v out of the note, want the two projects", n.Links)
	}
	if projects[0] != "Firma" || projects[1] != "Regnskab" {
		t.Errorf("got %v", projects)
	}
}

// Backwards is the direction that makes it a second brain rather than a folder.
func TestANoteIsFoundFromWhatItPointsAt(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	const taskID = "01a01694-a25c-7000-a50f-d72ab33f9b6a"
	notes := []*Note{
		{CreatedBy: userID, Body: "Aftalt på mødet, se /opgave/" + taskID},
		{CreatedBy: userID, Body: "Intet at gøre med den sag."},
	}
	for _, n := range notes {
		if err := db.SaveNote(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	found, err := db.NotesLinking(ctx, "task", taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("%d notes point at the task, want 1", len(found))
	}
	if !strings.HasPrefix(found[0].Title, "Aftalt") {
		t.Errorf("the wrong note came back: %q", found[0].Title)
	}
}

// The links are an index over the text, so editing the text must move them.
// A stale link is worse than none: it says a connection exists that does not.
func TestEditingANoteRewritesWhatItPointsAt(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	n := &Note{CreatedBy: userID, Body: "Handler om #Firma"}
	if err := db.SaveNote(ctx, n); err != nil {
		t.Fatal(err)
	}
	if before, _ := db.NotesLinking(ctx, "project", "Firma"); len(before) != 1 {
		t.Fatal("the first save did not record the link")
	}

	n.Body = "Handler om #Regnskab i stedet"
	if err := db.SaveNote(ctx, n); err != nil {
		t.Fatal(err)
	}

	stale, err := db.NotesLinking(ctx, "project", "Firma")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("the note still points at Firma after the mention was removed")
	}
	if fresh, _ := db.NotesLinking(ctx, "project", "Regnskab"); len(fresh) != 1 {
		t.Errorf("the new mention was not recorded")
	}
}

// A list of untitled notes is a list nobody can read.
func TestANoteTakesItsTitleFromItsFirstLine(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	for _, c := range []struct{ body, want string }{
		{"# Møde med Anders\n\nHan ville gerne...", "Møde med Anders"},
		{"\n\n  Løse tanker  \nmere", "Løse tanker"},
		{"", ""},
	} {
		n := &Note{CreatedBy: userID, Body: c.body}
		if err := db.SaveNote(ctx, n); err != nil {
			t.Fatal(err)
		}
		if n.Title != c.want {
			t.Errorf("body %q gave title %q, want %q", c.body, n.Title, c.want)
		}
	}
}

// Danish spelling, both ways round — the same generosity tasks already get.
func TestNotesAreFoundInEitherSpelling(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	if err := db.SaveNote(ctx, &Note{
		CreatedBy: userID,
		Title:     "Grønt regnskab",
		Body:      "Målt på årsbasis for Århus.",
	}); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"grønt", "gront", "århus", "aarhus", "regnskab"} {
		found, err := db.SearchNotes(ctx, q, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 {
			t.Errorf("%q found %d notes, want 1", q, len(found))
		}
	}
}

// Deleting is the trash, the way it is everywhere else in this program. A note is
// the one place somebody can lose an hour of writing to one keystroke.
func TestADeletedNoteCanComeBack(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	n := &Note{CreatedBy: userID, Body: "Vigtigt"}
	if err := db.SaveNote(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteNote(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if gone, _ := db.Note(ctx, n.ID); gone != nil {
		t.Error("a deleted note is still returned")
	}
	if err := db.RestoreNote(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	back, err := db.Note(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back == nil || back.Body != "Vigtigt" {
		t.Error("the note did not come back whole")
	}
}

func TestWhatIsReadOutOfABody(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       []NoteLink
	}{
		{"a hash inside a word is not a tag", "C#Sharp og x#y", nil},
		{"a note by title", "se [[Møde med Anders]]", []NoteLink{{"note", "Møde med Anders"}}},
		{"the same tag twice is one link", "#Firma og #Firma igen",
			[]NoteLink{{"project", "Firma"}}},
		{"a heading is not a tag", "# Overskrift", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := LinksIn(c.body)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("got %v, want %v", got[i], c.want[i])
				}
			}
		})
	}
}

// The number beside a project is what is left to do: not finished, not deleted.
// It replaced a count of people, which said 2 on an empty project.
func TestTheProjectCountIsWhatIsLeft(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	p := &Project{Name: "Tallene", OwnerID: userID}
	if err := db.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}

	count := func() int {
		list, err := db.ListProjects(ctx, userID, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range list {
			if x.ID == p.ID {
				return x.OpenCount
			}
		}
		t.Fatal("the project is not in the list")
		return -1
	}

	if n := count(); n != 0 {
		t.Errorf("an empty project counts %d", n)
	}

	var ids []string
	for _, c := range []string{"en", "to", "tre"} {
		task := &Task{ProjectID: p.ID, Content: c, CreatedBy: userID}
		if err := db.CreateTask(ctx, task, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	if n := count(); n != 3 {
		t.Errorf("three tasks count %d", n)
	}

	if _, err := db.CompleteTask(ctx, ids[0], userID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Errorf("after finishing one, the count is %d, want 2", n)
	}

	if err := db.DeleteTask(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Errorf("after deleting one, the count is %d, want 1", n)
	}
}
