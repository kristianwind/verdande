-- Foldable groups over the projects in the sidebar.
--
-- A table rather than a `group_name` column on the project: a group has to be
-- renamable in one write, has to survive being emptied, and has to remember
-- whether it is folded. A repeated string on every project row can do none of
-- those — renaming would touch N rows, and an empty group would simply cease to
-- exist the moment its last project left it.
CREATE TABLE project_groups (
    id         TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- Folded state lives on the row, not in localStorage.
    --
    -- The sidebar's *width* is a property of the screen you are sitting at, which
    -- is why that one is stored per browser. A folded group is not: it is a
    -- statement about that group — "this is the part of my work I am not in right
    -- now" — and it is true on the laptop and the desktop alike. A group is also
    -- owned by exactly one person, so there is nobody else's answer to disagree
    -- with.
    collapsed  INTEGER NOT NULL DEFAULT 0,
    sort_order REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_project_groups_owner ON project_groups (owner_id);

-- ON DELETE SET NULL: deleting a group is a decision about the heading, not about
-- the projects underneath it. They fall back to ungrouped, in the order they
-- already had.
ALTER TABLE projects ADD COLUMN group_id TEXT REFERENCES project_groups (id) ON DELETE SET NULL;

CREATE INDEX idx_projects_group ON projects (group_id) WHERE deleted_at IS NULL;
