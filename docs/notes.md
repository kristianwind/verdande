# Notes

Markdown in, Markdown out. Nothing in between.

A note is written as rich text — headings, lists, quotations, code — and stored as
the Markdown it looks like. That is not a detail about the database: it is what
makes a note yours. Export gives you the same text you were looking at, and on the
day this program is gone, every note opens in any editor.

## Writing

Start typing. **The first line is the title** — always, and re-derived every time
you save, so a note you rewrite the top of is called by what it now says rather
than by what it once did.

| Style | Written as | Shortcut |
|---|---|---|
| Title | `# ` at the start of the line | pick it in **Aa** |
| Heading | `## ` | |
| Subheading | `### ` | |
| Quote | `> ` | |
| Code block | three backticks, with an optional language | |
| Bullet and numbered lists | `- ` and `1. `, nested with Tab | |
| Bold | two asterisks around the words | ⌘B |
| Italic | one asterisk around the words | ⌘I |
| Underline | a `u` tag around the words | ⌘U |
| Strikethrough | two tildes around the words | |
| Code | one backtick around the words | |

Every line becomes exactly one block. Markdown would normally join consecutive
lines into a paragraph; here it does not, because you typed a line and expect to
get it back.

Press the source button in the toolbar to see the note as the file it is. Edits there are edits to the
note.

### Pictures

Paste one, or drag it onto the note. It is stored with the note and travels with
it in an export. Every raster format works; SVG is stored but not shown inline —
it is a document that can run script, and a note is not a place to run somebody
else's.

### Code blocks

A fenced block is coloured for the language it names, and guesses when it does not.
Each one carries a **Copy** button in its corner.

## Linking

Three kinds of reference, parsed out of the text as you write it:

| You type | It means |
|---|---|
| `#Firma` | The project — the same `#Firma` quick add understands, not a second kind of tag |
| `[[Møde med Anders]]` | Another note, by its title |
| A pasted task link | That task |

Links are an index over the text, rebuilt every time the note is saved. The text
stays the truth.

**The interesting direction is backwards.** A task shows what has been written
about it, and a project shows the notes filed in it — without anybody having to
remember to attach anything. Write `#Firma` in a note and it appears on that
project's page.

A link to something that has been deleted reads as a dead link. It is a fact about
the note, and it should not vanish because something it mentioned did.

## Filing and sharing

A note belongs to a project, or to nobody.

- **In a project:** everybody who can read the project can read the note, and an
  editor can change it. Choose it under **Share in** at the foot of the note.
- **In no project:** yours, and nobody else's.

There is no folder tree. Projects, groups and labels already exist, are already
shared and already have roles — a second hierarchy to file things in is one too
many, and a note in a project inherits every one of those answers.

## Finding

The list groups itself: favourites first, then **Today**, **Yesterday**, **This
week**, and then by month. Sort by created, last touched or name, and the grouping
follows — sorted by name it groups by first letter instead.

Search finds a word wherever it is:

- **`gron` finds `grøn`.** Unicode does not consider `ø` an accented `o`, so
  stripping diacritics is not enough for Danish. A folded copy of every note is
  indexed beside the original.
- **`inventory` finds `keepInventory`.** An index is built of whole words, and a
  decade of notes is full of commands and identifiers that are several words in
  one. When the index has nothing, the text itself is read.
- Results are ranked by how well they match, with the title weighted ten times the
  body. A word in the title is what the note is about; the same word once in a long
  body is a mention.

## Putting notes away

Two different things, and the difference matters:

- **Archive** — this is finished and I do not want to read past it. It stays
  findable and comes back whenever you ask.
- **Trash** — this was a mistake. It is gone in thirty days.

Select several with ⌘-click and shift-click to archive or delete them together.

## In and out

**Export** gives you a zip of Markdown files, one per note, with the dates in a
YAML front matter block that Obsidian and Bear also read:

```markdown
---
created: 2019-04-02T10:15:00Z
modified: 2024-11-30T21:04:00Z
---

# Møde med Anders

Han vil gerne have kaffe hver uge.
```

**Import** takes the same shape back. Pictures travel in the zip beside the notes,
and the links in the text are relative — so a note and its pictures must be in the
same archive, which matters when a large export is split into parts.

Two things to know before importing:

- **Nothing is merged.** Every run creates new notes. Importing the same archive
  twice gives you two of everything.
- **The dates are read from the front matter.** Without them, a move of a thousand
  notes is a pile that was all written the same evening — and the order they were
  written in is half of what an archive is.

### From Apple Notes

`tools/apple-notes-til-markdown.py` in the repository exports Apple Notes to
exactly this shape, front matter and pictures included. It is macOS-only, because
Apple Notes is.

For a large library, `tools/pak-noter-i-portioner.py` splits the export into
uploadable parts, keeping each note with its own pictures.

## What is not encrypted

Note bodies are stored as plain text. **A note holding a password or an API key is
holding it in plain text in every backup you can download.** The mailbox
credentials, OAuth tokens and AI keys are sealed; note bodies are not, and neither
are task titles, descriptions or comments.

Encrypting them is on the list and is bigger than it sounds — the full-text index
and the folded search copy are both derived from the body, so both would have to go
and search would have to be rewritten. Until then: keep secrets somewhere built for
them.
