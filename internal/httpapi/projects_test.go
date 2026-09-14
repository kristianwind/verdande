package httpapi

import (
	"net/http"
	"testing"
)

// Restoring has been possible since the trash existed. Finding what to restore
// has not: the endpoint takes an id, and the id is exactly what you no longer
// have once the project has gone from the interface.
func TestTheTrashCanBeListedAndRestored(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Ferie"})
	id := created["id"].(string)
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "book fly", "project_id": id})

	if resp, _ := ts.do(t, "DELETE", "/api/v1/projects/"+id, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}

	// Gone from the ordinary listing.
	_, live := ts.do(t, "GET", "/api/v1/projects", nil)
	for _, raw := range live["projects"].([]any) {
		if raw.(map[string]any)["id"] == id {
			t.Fatal("a deleted project is still in the project list")
		}
	}

	// And findable in the trash, with enough to decide whether to bring it back.
	_, trash := ts.do(t, "GET", "/api/v1/trash/projects", nil)
	list, _ := trash["projects"].([]any)
	if len(list) != 1 {
		t.Fatalf("want one project in the trash, got %v", list)
	}
	entry := list[0].(map[string]any)
	if entry["name"] != "Ferie" {
		t.Errorf("name = %v", entry["name"])
	}
	if entry["task_count"] != float64(1) {
		t.Errorf("task_count = %v, want 1 — the count is what says whether it matters",
			entry["task_count"])
	}
	if entry["purge_after"] == nil || entry["deleted_at"] == nil {
		t.Errorf("no deadline was reported: %v", entry)
	}

	if resp, _ := ts.do(t, "POST", "/api/v1/trash/projects/"+id+"/restore", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("restore: status %d", resp.StatusCode)
	}

	// Back, with its task.
	_, tasks := ts.do(t, "GET", "/api/v1/tasks?project_id="+id, nil)
	if got, _ := tasks["tasks"].([]any); len(got) != 1 {
		t.Errorf("the task did not come back with the project: %v", got)
	}
	_, trash = ts.do(t, "GET", "/api/v1/trash/projects", nil)
	if list, _ := trash["projects"].([]any); len(list) != 0 {
		t.Errorf("still in the trash after restoring: %v", list)
	}
}

// Somebody else's deleted project is not in your trash, and you cannot restore it.
func TestTheTrashIsPerOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Privat"})
	id := created["id"].(string)
	ts.do(t, "DELETE", "/api/v1/projects/"+id, nil)

	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, trash := other.do(t, "GET", "/api/v1/trash/projects", nil)
	if list, _ := trash["projects"].([]any); len(list) != 0 {
		t.Errorf("somebody else's deleted project is visible: %v", list)
	}
	resp, _ := other.do(t, "POST", "/api/v1/trash/projects/"+id+"/restore", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

func TestReorderProjectsWritesTheGivenOrder(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var ids []string
	for _, name := range []string{"Alfa", "Bravo", "Charlie"} {
		_, body := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": name})
		ids = append(ids, body["id"].(string))
	}

	// Back to front.
	reversed := []string{ids[2], ids[1], ids[0]}
	resp, _ := ts.do(t, "POST", "/api/v1/projects/reorder", map[string]any{"ids": reversed})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder: status %d", resp.StatusCode)
	}

	_, list := ts.do(t, "GET", "/api/v1/projects", nil)
	projects, _ := list["projects"].([]any)

	var got []string
	for _, raw := range projects {
		p := raw.(map[string]any)
		// The Inbox sorts ahead of everything by its own rule; it is not part of
		// what was reordered.
		if p["is_inbox"] == true {
			continue
		}
		got = append(got, p["id"].(string))
	}
	if len(got) != 3 || got[0] != reversed[0] || got[1] != reversed[1] || got[2] != reversed[2] {
		t.Errorf("order = %v, want %v", got, reversed)
	}
}

// sort_order is a column on the project, not a per-viewer preference, so
// reordering must not rearrange a project somebody else owns.
func TestReorderProjectsLeavesSomebodyElsesAlone(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	other := ts.newUser(t, "anden@example.dk", "Anden")
	_, theirs := other.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Deres"})
	theirID := theirs["id"].(string)

	_, before := other.do(t, "GET", "/api/v1/projects", nil)
	var wanted float64
	for _, raw := range before["projects"].([]any) {
		if p := raw.(map[string]any); p["id"] == theirID {
			wanted = p["sort_order"].(float64)
		}
	}

	// Ask, as somebody else, to put their project first.
	resp, _ := ts.do(t, "POST", "/api/v1/projects/reorder", map[string]any{
		"ids": []string{theirID},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder: status %d", resp.StatusCode)
	}

	_, after := other.do(t, "GET", "/api/v1/projects", nil)
	for _, raw := range after["projects"].([]any) {
		if p := raw.(map[string]any); p["id"] == theirID {
			if p["sort_order"] != wanted {
				t.Errorf("sort_order changed from %v to %v — somebody else moved it",
					wanted, p["sort_order"])
			}
		}
	}
}

// The member list errored on every project — the ORDER BY named an expression,
// which SQLite refuses across a UNION — so the share panel had never shown
// anybody, not even the owner.
func TestListMembersWorksAtAll(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Firma"})

	resp, body := ts.do(t, "GET", "/api/v1/projects/"+project["id"].(string)+"/members", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
	members, _ := body["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("a project with no invites lists %d members, want its owner", len(members))
	}
	if m := members[0].(map[string]any); m["role"] != "owner" {
		t.Errorf("role = %v, want owner", m["role"])
	}
}

// And the Inbox, which is the one every task drawer asks about.
func TestListMembersWorksForTheInbox(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, list := ts.do(t, "GET", "/api/v1/projects", nil)
	var inboxID string
	for _, raw := range list["projects"].([]any) {
		if p := raw.(map[string]any); p["is_inbox"] == true {
			inboxID = p["id"].(string)
		}
	}

	resp, body := ts.do(t, "GET", "/api/v1/projects/"+inboxID+"/members", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
}

// A role sent by mistake used to be uncorrectable: the only way back was to remove
// the person and invite them again, which unassigns every task they were
// responsible for. Fixing a dropdown should not cost somebody their work.
func TestAMembersRoleCanBeChangedWithoutRemovingThem(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	member := ts.newUser(t, "anden@example.dk", "Anden")

	memberUser, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "anden@example.dk", "role": "viewer",
	})

	// A viewer cannot write. That is the rule, not the bug.
	if resp, _ := member.do(t, "POST", "/api/v1/tasks",
		map[string]any{"content": "prøv", "project_id": projectID}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a viewer created a task: status %d", resp.StatusCode)
	}

	resp, _ := ts.do(t, "PATCH", "/api/v1/projects/"+projectID+"/members/"+memberUser.ID,
		map[string]any{"role": "editor"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("promote to editor: status %d", resp.StatusCode)
	}

	if resp, _ := member.do(t, "POST", "/api/v1/tasks",
		map[string]any{"content": "nu kan jeg", "project_id": projectID}); resp.StatusCode != http.StatusCreated {
		t.Errorf("an editor could not create a task: status %d", resp.StatusCode)
	}
	_, members := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/members", nil)
	for _, raw := range members["members"].([]any) {
		if m := raw.(map[string]any); m["user_id"] == memberUser.ID && m["role"] != "editor" {
			t.Errorf("role = %v after the change, want editor", m["role"])
		}
	}
}

// The owner is not a member row, and ownership is not a role you can hand out.
func TestTheOwnersStandingCannotBeChanged(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	member := ts.newUser(t, "anden@example.dk", "Anden")

	owner, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	memberUser, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "anden@example.dk", "role": "editor",
	})

	// Demoting the owner would leave a project nobody can administer.
	if resp, _ := ts.do(t, "PATCH", "/api/v1/projects/"+projectID+"/members/"+owner.ID,
		map[string]any{"role": "viewer"}); resp.StatusCode != http.StatusConflict {
		t.Errorf("changing the owner's role: status %d, want 409", resp.StatusCode)
	}
	// And no member may promote themselves to owner.
	if resp, _ := ts.do(t, "PATCH", "/api/v1/projects/"+projectID+"/members/"+memberUser.ID,
		map[string]any{"role": "owner"}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("granting ownership: status %d, want 422", resp.StatusCode)
	}
	// Nor may an editor change anybody's role: this is the owner's to do.
	if resp, _ := member.do(t, "PATCH", "/api/v1/projects/"+projectID+"/members/"+memberUser.ID,
		map[string]any{"role": "viewer"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an editor changed a role: status %d, want 404", resp.StatusCode)
	}
	// Somebody with no membership at all is not quietly given one.
	stranger := ts.newUser(t, "tredje@example.dk", "Tredje")
	_ = stranger
	strangerUser, _ := ts.db.UserByEmail(t.Context(), "tredje@example.dk")
	if resp, _ := ts.do(t, "PATCH", "/api/v1/projects/"+projectID+"/members/"+strangerUser.ID,
		map[string]any{"role": "editor"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a non-member was given a role: status %d, want 404", resp.StatusCode)
	}
}

// A project can have more than one section.
//
// Reported from use: sections "have no function, because you cannot create more
// than one". Every test here made exactly one, which is the shape of test that
// cannot see this — and so is every smoke test.
func TestAProjectCanHaveSeveralSections(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Ombygning"})
	id := project["id"].(string)

	names := []string{"Planlægning", "I gang", "Til gennemsyn"}
	for _, name := range names {
		resp, body := ts.do(t, "POST", "/api/v1/projects/"+id+"/sections",
			map[string]any{"name": name})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create section %q: status %d, body %v", name, resp.StatusCode, body)
		}
	}

	_, listed := ts.do(t, "GET", "/api/v1/projects/"+id+"/sections", nil)
	got, _ := listed["sections"].([]any)
	if len(got) != len(names) {
		t.Fatalf("the project has %d sections, want %d", len(got), len(names))
	}
	// In the order they were made, and each with its own place: a sort_order that
	// came out the same for all three would put them in an arbitrary order that
	// changes between reads.
	seen := map[float64]bool{}
	for i, raw := range got {
		s := raw.(map[string]any)
		if s["name"] != names[i] {
			t.Errorf("section %d is %v, want %q", i, s["name"], names[i])
		}
		order, _ := s["sort_order"].(float64)
		if seen[order] {
			t.Errorf("two sections share sort_order %v", order)
		}
		seen[order] = true
	}
}

// Sections can be dragged into another order, and the order is the project's.
//
// The list is what a project's plan *is* — "Planlægning" before "I gang" is the
// order the work happens in — and until this endpoint existed the only way to
// change it was to delete a section and make it again, which moves its tasks to
// the unsectioned area on the way past.
func TestSectionsCanBeReordered(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Ombygning"})
	id := project["id"].(string)

	ids := map[string]string{}
	names := []string{"Planlægning", "I gang", "Til gennemsyn"}
	for _, name := range names {
		_, body := ts.do(t, "POST", "/api/v1/projects/"+id+"/sections", map[string]any{"name": name})
		ids[name] = body["id"].(string)
	}

	want := []string{"Til gennemsyn", "Planlægning", "I gang"}
	order := make([]string, len(want))
	for i, name := range want {
		order[i] = ids[name]
	}

	resp, body := ts.do(t, "POST", "/api/v1/projects/"+id+"/sections/reorder",
		map[string]any{"ids": order})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder: status %d, body %v", resp.StatusCode, body)
	}

	_, listed := ts.do(t, "GET", "/api/v1/projects/"+id+"/sections", nil)
	got, _ := listed["sections"].([]any)
	if len(got) != len(want) {
		t.Fatalf("the project has %d sections, want %d", len(got), len(want))
	}
	for i, raw := range got {
		if name := raw.(map[string]any)["name"]; name != want[i] {
			t.Errorf("section %d is %v, want %q", i, name, want[i])
		}
	}

	// A section belonging to somebody else's project is not moved by naming it
	// here. The ids in the body are not checked one by one — the WHERE clause is
	// what refuses them — so this is the assertion that it actually does.
	_, other := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Sommerhus"})
	otherID := other["id"].(string)
	_, foreign := ts.do(t, "POST", "/api/v1/projects/"+otherID+"/sections",
		map[string]any{"name": "Havearbejde"})
	foreignID := foreign["id"].(string)

	resp, _ = ts.do(t, "POST", "/api/v1/projects/"+id+"/sections/reorder",
		map[string]any{"ids": []string{foreignID}})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder with a foreign id: status %d", resp.StatusCode)
	}

	_, stillThere := ts.do(t, "GET", "/api/v1/projects/"+otherID+"/sections", nil)
	elsewhere, _ := stillThere["sections"].([]any)
	if len(elsewhere) != 1 || elsewhere[0].(map[string]any)["name"] != "Havearbejde" {
		t.Errorf("the other project's section did not survive: %v", elsewhere)
	}
}

// A project can be created straight into a group.
//
// `group_id` was declared on the request type, documented on it, and applied by
// the update handler — but the create handler ignored it, so a client that asked
// for a project in a group got a 201 and a project in no group. A field that is
// accepted and thrown away is worse than one that is refused: nothing reports it.
func TestAProjectCanBeCreatedIntoAGroup(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	groupID := group["id"].(string)

	resp, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{
		"name": "Sæsonstart", "group_id": groupID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d, body %v", resp.StatusCode, project)
	}
	if project["group_id"] != groupID {
		t.Errorf("the response says group_id = %v, want %q", project["group_id"], groupID)
	}

	// And it is really filed there, not just claimed in the reply.
	_, listed := ts.do(t, "GET", "/api/v1/projects", nil)
	var found map[string]any
	for _, raw := range listed["projects"].([]any) {
		if p := raw.(map[string]any); p["id"] == project["id"] {
			found = p
		}
	}
	if found == nil {
		t.Fatal("the new project is not in the list")
	}
	if found["group_id"] != groupID {
		t.Errorf("the stored project has group_id = %v, want %q", found["group_id"], groupID)
	}
}

// A section holds as many tasks as you put in it.
//
// Reported from use: "sections can only have one task". Driven through the same
// endpoint the interface uses, with the same `after_id` the drop handler sends —
// the second task goes after the first, which is the case that would fail if
// positioning inside a section were wrong.
func TestASectionHoldsMoreThanOneTask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Skovvænget"})
	projectID := project["id"].(string)
	_, section := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/sections",
		map[string]any{"name": "Håndværker"})
	sectionID := section["id"].(string)

	var previous string
	for _, content := range []string{"knager på badeværelset", "plade på opvaskemaskine", "fuge om vinduet"} {
		_, created := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
			"content": content, "project_id": projectID,
		})
		id := created["id"].(string)

		resp, moved := ts.do(t, "POST", "/api/v1/tasks/"+id+"/move", map[string]any{
			"project_id": projectID, "section_id": sectionID, "after_id": previous,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("move %q: status %d, body %v", content, resp.StatusCode, moved)
		}
		if moved["section_id"] != sectionID {
			t.Fatalf("%q did not land in the section: %v", content, moved["section_id"])
		}
		previous = id
	}

	_, listed := ts.do(t, "GET", "/api/v1/tasks?project_id="+projectID, nil)
	tasks, _ := listed["tasks"].([]any)
	inSection := []string{}
	for _, raw := range tasks {
		task := raw.(map[string]any)
		if task["section_id"] == sectionID {
			inSection = append(inSection, task["content"].(string))
		}
	}
	if len(inSection) != 3 {
		t.Fatalf("the section holds %d tasks, want 3: %v", len(inSection), inSection)
	}
	// And in the order they were placed, since each went after the one before it.
	want := []string{"knager på badeværelset", "plade på opvaskemaskine", "fuge om vinduet"}
	for i, content := range want {
		if inSection[i] != content {
			t.Errorf("position %d is %q, want %q — the whole list: %v", i, inSection[i], content, inSection)
		}
	}
}

// Et projekt kunne kun deles ved at stave en e-mailadresse. Det er den forkerte
// vej at spørge om en kollega, man kender navnet på og sidder i samme rum som — og
// på en instans, hvor konti kun bliver til ved invitation, er der ikke noget at
// beskytte ved at lade være med at vise dem.
func TestAProjectCanBeSharedWithSomebodyWhoIsAlreadyHere(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.newUser(t, "anden@example.dk", "Anden")

	anden, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	projectID := project["id"].(string)

	// Først står de i listen over dem, der kan tilføjes.
	_, before := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/members", nil)
	if !hasPerson(before["candidates"], anden.ID) {
		t.Fatalf("Anden er ikke blandt kandidaterne: %v", before["candidates"])
	}

	// Delt ved at pege, ikke ved at stave.
	resp, added := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"user_id": anden.ID, "role": "editor"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, added)
	}
	// Personen kommer med retur, så fladen kan tegne rækken uden at spørge igen.
	if user, _ := added["user"].(map[string]any); user["name"] != "Anden" {
		t.Errorf("user = %v, want Anden", added["user"])
	}

	// Og bagefter er de et medlem og ikke længere en kandidat: en liste, der
	// tilbyder at tilføje en, der allerede er der, er en liste, man gør det i.
	_, after := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/members", nil)
	if hasPerson(after["candidates"], anden.ID) {
		t.Errorf("Anden står stadig som kandidat efter at være tilføjet")
	}
	found := false
	for _, raw := range after["members"].([]any) {
		if m := raw.(map[string]any); m["user_id"] == anden.ID {
			found = true
			if m["role"] != "editor" {
				t.Errorf("role = %v, want editor", m["role"])
			}
		}
	}
	if !found {
		t.Errorf("Anden blev ikke medlem: %v", after["members"])
	}
}

// Ejeren skal ikke kunne tilbydes som en, projektet kan deles med — de har det.
func TestTheOwnerIsNotOfferedAsSomebodyToShareWith(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	me, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Mit"})

	_, body := ts.do(t, "GET", "/api/v1/projects/"+project["id"].(string)+"/members", nil)
	if hasPerson(body["candidates"], me.ID) {
		t.Errorf("ejeren står som kandidat til sit eget projekt")
	}
}

// En invitation, der venter, står ved siden af medlemmerne — og kan tages tilbage.
//
// Uden den er den usynlig: personen har ingen konto, så de er hverken medlem eller
// kandidat, og så bliver invitationen sendt igen. Det andet link virker lige så
// godt som det første, og så er der to at trække tilbage i stedet for et.
func TestAPendingInviteIsVisibleAndCanBeWithdrawn(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	projectID := project["id"].(string)

	resp, _ := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"email": "fremmed@example.dk", "role": "viewer"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invitation: status %d", resp.StatusCode)
	}

	_, body := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/members", nil)
	pending, _ := body["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("%d ventende invitationer, want 1: %v", len(pending), body["pending"])
	}
	first := pending[0].(map[string]any)
	if first["email"] != "fremmed@example.dk" {
		t.Errorf("email = %v", first["email"])
	}

	// Et andet link til den samme indbakke hjælper ingen.
	if resp, _ := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"email": "fremmed@example.dk", "role": "viewer"}); resp.StatusCode != http.StatusConflict {
		t.Errorf("en gentaget invitation gav %d, want 409", resp.StatusCode)
	}

	resp, _ = ts.do(t, "DELETE",
		"/api/v1/projects/"+projectID+"/invites/"+first["id"].(string), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("tilbagetrækning: status %d", resp.StatusCode)
	}

	_, after := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/members", nil)
	if p, _ := after["pending"].([]any); len(p) != 0 {
		t.Errorf("invitationen står der stadig: %v", after["pending"])
	}
}

// Et id, der ikke findes, må ikke blive til en medlemsrække, der peger på ingen.
func TestSharingWithSomebodyWhoDoesNotExistIsRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	resp, _ := ts.do(t, "POST", "/api/v1/projects/"+project["id"].(string)+"/invites",
		map[string]any{"user_id": "0198f000-0000-7000-8000-00000000dead", "role": "editor"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

// hasPerson siger, om en liste af personer indeholder et bestemt id.
func hasPerson(raw any, id string) bool {
	list, _ := raw.([]any)
	for _, item := range list {
		if p, ok := item.(map[string]any); ok && p["id"] == id {
			return true
		}
	}
	return false
}

// Man bliver lukket ind i et projekt uden at blive spurgt. Så skal man i det
// mindste vide det — og kunne gå ud igen uden at bede ejeren om lov.
func TestBeingAddedToAProjectIsToldAndCanBeUndone(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	anden := ts.newUser(t, "anden@example.dk", "Anden")

	andenUser, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Fælleshuset"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"user_id": andenUser.ID, "role": "editor"})

	// Beskeden. Før stod projektet bare i sidebjælken næste gang, de kiggede.
	_, inbox := anden.do(t, "GET", "/api/v1/notifications", nil)
	var shared map[string]any
	for _, raw := range inbox["notifications"].([]any) {
		if n := raw.(map[string]any); n["kind"] == "project.shared" {
			shared = n
		}
	}
	if shared == nil {
		t.Fatalf("ingen besked om delingen: %v", inbox["notifications"])
	}
	if shared["project_id"] != projectID {
		t.Errorf("project_id = %v, want %s — uden det fører beskeden ingen steder hen",
			shared["project_id"], projectID)
	}

	// Udgangen.
	resp, _ := anden.do(t, "DELETE", "/api/v1/projects/"+projectID+"/members/"+andenUser.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("forlad projektet: status %d", resp.StatusCode)
	}
	if resp, _ := anden.do(t, "GET", "/api/v1/projects/"+projectID, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("projektet er stadig synligt efter at være forladt: status %d", resp.StatusCode)
	}
}

// En udgang for en selv er ikke en dør til de andres medlemskaber.
func TestAMemberCannotRemoveAnotherMember(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	en := ts.newUser(t, "en@example.dk", "En")
	ts.newUser(t, "to@example.dk", "To")

	enUser, err := ts.db.UserByEmail(t.Context(), "en@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	toUser, err := ts.db.UserByEmail(t.Context(), "to@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	for _, u := range []string{enUser.ID, toUser.ID} {
		ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
			map[string]any{"user_id": u, "role": "editor"})
	}

	// 404 og ikke 403: "det må du ikke" bekræfter, at medlemskabet findes.
	resp, _ := en.do(t, "DELETE", "/api/v1/projects/"+projectID+"/members/"+toUser.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
	_, members := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/members", nil)
	if len(members["members"].([]any)) != 3 {
		t.Errorf("%d medlemmer tilbage, want 3", len(members["members"].([]any)))
	}
}

// Ejeren kan ikke gå fra sit eget projekt. Det ville være en overdragelse.
func TestTheOwnerCannotLeaveTheirOwnProject(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	me, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Mit"})

	resp, _ := ts.do(t, "DELETE",
		"/api/v1/projects/"+project["id"].(string)+"/members/"+me.ID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status %d, want 409", resp.StatusCode)
	}
}
