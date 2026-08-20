# Notes, as verdande builds them

A description of one working notes system: the data model, the syntax, the search,
the rendering contract, the import and export formats, and — more useful than any
of it — the decisions that look arbitrary until you know what they cost.

It is written to be lifted into another program. Nothing here depends on Go,
SQLite or Svelte except where it says so; where an answer was forced by the tool,
that is called out so you can pick a different one on purpose rather than by
accident.

---

## Read this part first: what to ask before you build

**This document is not a drop-in. It describes a notes system that lives inside a
task app with projects, roles, a trash and a search box, and about a third of its
design is inherited from that.** Lifting it into a program with different
surroundings without deciding which parts to keep produces something that works
for a week and then fights you.

**If you are an assistant working from this file: ask these questions and get
answers before writing code.** Do not guess a default. Each one changes what you
build, not just how you build it.

### About the surroundings

1. **Does the host app already have a place to file things** — projects, folders,
   notebooks, tags? verdande deliberately gave notes no hierarchy of their own and
   filed them under the projects that already existed. If your app has nothing,
   you need an answer for where a note lives, and "a folder tree" is the expensive
   one.
2. **Is there more than one person?** Sharing, roles and "who wrote this" run
   through the whole model below. Single-user changes the schema and removes about
   half the permission surface.
3. **Is there already a trash, and does it expire?** Notes lean on one. Without
   it, deleting is either permanent or you are building a trash.
4. **Is there an existing full-text index?** The search here is FTS5-shaped. On
   Postgres it is `tsvector`, on a client-side app it is probably a scan, and the
   ranking section below changes accordingly.

### About the notes themselves

5. **Markdown, rich text, or blocks?** verdande stores Markdown and renders it.
   That decision is defended below; it is also the one that most constrains
   everything else.
6. **Is the first line the title, or is there a title field?** Both work. Deriving
   it means one less thing to keep in step and one less thing a person can leave
   blank, and it forces the rule "the first line is a heading" on the editor.
7. **What links to what?** `#project`, `[[note]]` and task references exist here
   because the app has projects and tasks. Yours may have none of them, or others.
8. **Do notes carry files?** Attachments bring content-type handling, a store, an
   authorisation path and an XSS surface. Skipping them is a legitimate answer.

### About scale and language

9. **How many notes, realistically?** Several choices below flip at around ten
   thousand: the list payload, the search fallback, rendering the whole list.
10. **Which languages?** The folding described under *Search* is Danish. Every
    language has its own version of that problem and the generic answer (strip
    diacritics) is wrong for several of them.
11. **Where does the text end up besides the screen?** Search indexes, AI
    features, exports and backups each read it. Plaintext makes all of them cheap
    and makes a stolen backup expensive — see *What is still open*.

### About integration, specifically

12. **Who owns the note's identity — you or the other system?** If notes will sync
    with something else, ids and timestamps are the whole problem, and the
    `updated_at` rule below becomes the first thing you have to change.
13. **Is this a read of somebody else's notes, or the store of record?** A copy
    needs conflict rules; a store of record needs export.
14. **What happens to a note whose project, task or link target is deleted?**
    Answered here as "the link goes dead and the note is untouched". If your host
    app cascades instead, say so before the first delete.

---

## The shape of a note

```
id           an opaque string, unique
project_id   nullable — where it is filed, or loose
title        derived, never set by a caller
body         Markdown, the whole note
created_by   nullable — survives the account being deleted
pinned       favourite; the list puts these first
archived_at  nullable timestamp — put away, not thrown away
deleted_at   nullable timestamp — the trash
created_at
updated_at
```

### Markdown in a column

Not rich text, and not a format of our own.

Export was a requirement before a line was written, and **if the stored form is
the exported form there is no conversion left to lose anything in.** A note stays
readable by any editor on the day the program is gone. It is also what makes
search and anything an AI does over notes cheap: both read text.

The cost is real and you should know it before choosing: everything the editor can
do has to survive a round trip through Markdown, and anything Markdown cannot say,
the editor must not offer. There is no place to hide a feature.

### The title is the first line

Always, and re-derived on every save — not "when it is empty".

Derived once and then left alone, a title goes stale the moment somebody rewrites
the opening of a note, and the list ends up calling a note by a name that is
nowhere in it. **The column is a cache of the body, not a field beside it**, and no
caller can set it, so every caller gets the same answer.

This forces one editor rule: a new note opens on its title, and the first line is
styled as a heading. Otherwise a note whose first line is a sentence is a note
named by its first sentence.

### Archive and trash are two different bits

The trash says *this was a mistake, and in thirty days it is gone*. The archive
says *this is finished, and I do not want to read past it.*

A twelve-hundred-note import is exactly the case where most notes are worth
keeping and almost none are worth looking at. Without an archive, the list is the
whole archive forever, and finding this week's note means reading past a decade.

Both are nullable timestamps rather than booleans: *when was this put away* answers
questions a flag cannot, and it costs the same byte.

---

## Filing: no hierarchy of its own

A note belongs to a project, or to nobody.

The reasoning is worth repeating because it is easy to talk yourself out of:
projects already exist, are already shared, already have roles, already have a
trash. **A second hierarchy to file things in is one too many**, and a note filed
under a project inherits every one of those answers instead of needing its own.

- A note with a project: everybody who can read the project can read the note; an
  editor can change it.
- A note without one: the author's, and nobody else's.

Sharing a note *is* filing it in a project. There is no access list of its own —
a second way to give somebody access is a second place to get it wrong, and the
two would disagree the day one of them changed.

---

## Links: three kinds, parsed out of the text

The body is the truth. Links are an index over it, rebuilt on every save.

| Syntax | Means | Notes |
|---|---|---|
| `#project` | a project | Same characters the task quick-add parser uses, on purpose |
| `[[a note]]` | another note, by title | The spelling every note app has settled on |
| `/opgave/<uuid>` or `task:<uuid>` | a task | What a link pasted out of the app carries |

Patterns as used here:

```
project   (?:^|\s)#([\p{L}\p{N}_-]{1,64})
note      \[\[([^\]\n]{1,200})\]\]
task      (?:/opgave/|task:)([0-9a-fA-F-]{36})
```

Three decisions inside that table:

- **`#Firma` in a note means the same project as `#Firma` in the task box.** Not a
  second kind of tag that happens to look alike. That is the whole of "tag a note
  and see it in the project".
- **The link table is torn down and rebuilt on save, not diffed.** A body is small
  and the set is small; a diff is a second description of the same fact, and it
  can disagree with the text. That is the one thing this must never do.
- **The target is not a foreign key.** A link to something deleted is a fact about
  the note. It should read as a dead link rather than vanish, and it must not
  block the delete.

The index exists so the question can be asked **backwards**: *what has been written
about this task?* Answering that by searching every note's prose would be too slow
to leave a panel open on.

---

## Search

Three layers, in the order they answer.

### 1. Full-text, ranked

FTS5 over `title`, `body` and a folded copy, with `unicode61 remove_diacritics 2`.

Ranking is bm25 with **the title weighted ten times the body**. A word in the title
is what the note is *about*; the same word once in a long body is a mention.

This matters more than it sounds. Ordering by "last touched" is invisible with ten
notes and useless with twelve hundred imported the same evening: searching "Tesla"
answered with a note about nginx first and the note actually called "Tesla Model Y"
second — which reads as a search that does not work, because from where the person
is sitting it is one.

### 2. Folding, for the language you are in

`remove_diacritics` does not touch `ø`, `æ` or `å` — Unicode considers them
letters in their own right, not accented forms. So a Dane cannot find "grøn" by
typing "gron", which is how people type when the keyboard is not theirs.

The answer here is a generated column carrying a transliterated copy:

```
ø,Ø → o     æ,Æ → ae     å,Å → aa
```

indexed alongside the body and **weighted the same as the body** — otherwise a
folded match outranks an exact one.

> **Ask, do not copy.** This mapping is Danish. German wants `ü → ue`, Swedish
> disagrees with Danish about `ä`, and Turkish has a dotless `ı` that breaks
> naive lowercasing entirely. Get the mapping from somebody who types the
> language.

### 3. A word inside a word

An index is built of tokens, and a token is whatever the tokenizer decided was a
word. `gamerule keepInventory true` is three of them, and the middle one is
`keepinventory` — so a search for "keep inventory" asks for two words that are
nowhere and gets nothing, while the note sits right there.

A prefix query gets half of it: `keep*` matches, `inventory*` cannot, and the two
are ANDed, so the half that works is thrown away by the half that cannot.

**This is not exotic in a notes app.** A decade of notes is full of commands,
identifiers and file names, and every one of them is several words the index
thinks is one.

So: the index answers first; when it has nothing, the text is read, and every term
must appear somewhere in the note, in any word. A `LIKE '%term%'` over the folded
column covers title, body and language folding at once.

What it costs: a leading `%` cannot use an index, so it scans. At twelve hundred
notes that is a few megabytes and a millisecond or two, and it only runs when the
fast path already came back empty — which is exactly the search that was about to
disappoint somebody. **At a hundred thousand notes it is the wrong shape**, and the
answer then is a trigram index, not a bigger scan.

It does not rank. There is no match to rank — that is the whole situation.

---

## The list, and why the body is empty in it

A list showing a title and ninety characters of preview must not fetch whole
notes. Twelve hundred notes came to 3.9 MB, of which 98 % was body nobody could
see.

The important half is *how* it is trimmed: **the body is emptied, not shortened.**

A field called `body` holding a *piece* of the body is a trap. The editor opens
it, the autosave pause writes it back, and the note is now cut down to its own
excerpt — destroyed by nothing more than having been looked at. Send `preview` as
its own field and leave `body` empty; the client sees the empty body and fetches
the whole note.

That costs one more separation, and it is not optional:

> **Which row is selected and which note is loaded are two different things.**

The editor loads its text when the note's id changes. Hand it the excerpt first
and the whole note second and the id is the same both times — so the second one
never renders, and somebody sits looking at a note that had a title a moment ago.

Highlight the row immediately from the list; give the editor the note only when it
is whole.

Rendering cost, separately: with twelve hundred rows, first paint took 2.1 s. CSS
`content-visibility: auto` with an intrinsic-size guess took it to 0.95 s with no
virtual list, no scroll maths, and no list that can fall out of step with itself.
Try that before building windowing.

---

## The editor contract

Rich text on top, Markdown underneath, and a source view that shows the file.

The supported subset — deliberately small, because everything in it has to survive
the round trip:

| Block | Markdown |
|---|---|
| Title | `# ` |
| Heading | `## ` |
| Subheading | `### ` |
| Quote | `> ` |
| Code block | ```` ``` ```` with an optional language |
| Bullet and numbered lists | `- `, `1. `, nested |

Inline: `**bold**`, `*italic*`, `~~strikethrough~~`, `` `code` ``, `<u>underline</u>`,
and images as `![alt](url)`.

Five rules that were each learned the hard way:

1. **Every line becomes exactly one block.** Markdown normally joins consecutive
   lines into a paragraph. Here it must not: the person typed a line and expects
   to get it back, and an editor that silently reflows what somebody wrote is an
   editor they stop trusting.
2. **Load on id change, never while typing.** Writing to the editor's HTML puts
   the caret back at the start, which mid-sentence is unusable.
3. **One definition of what a code block contains.** A copy button inside the
   `<pre>` and a syntax highlighter reading `textContent` will disagree, and the
   difference ends up in the file — a click on "Copy" wrote the word *Copied* into
   the code and the next save put it on disk. Have one function that says what a
   block holds, used by the highlighter and the Markdown serialiser both.
4. **A note that cannot be rendered must not take the pane with it.** One note
   arriving in an unexpected shape threw inside the editor's reactive effect, and
   from then on *no* note would open — the previous one just stayed on screen. Put
   a guard around the render, fall back to showing the raw text, and let the rest
   of the pane keep working.
5. **Escaping between tags and escaping inside an attribute are different jobs.**
   An escaper written for text between two tags handles `&`, `<`, `>` — and not
   quotes, because between tags they are harmless. Inside an attribute they close
   it. `![" onerror="alert(1)](…)` is the whole exploit.

---

## Attachments

Images pasted or dragged into a note, stored content-addressed by hash, referenced
from the body as ordinary Markdown images.

The security shape, which is the part worth copying:

- **Served as `application/octet-stream` with `nosniff` by default.** Attachments
  are the one place a user supplies content another user opens. An uploaded SVG or
  HTML file rendered inline runs its own script on your origin with the session
  cookie attached.
- **One exception, as a named allowlist:** a short list of raster image types
  served inline as their exact type — `png`, `jpeg`, `gif`, `webp`, `avif`, `heic`.
  A note carries its pictures in the text, and served as octet-stream every one of
  them is a broken image.
- **The list is named one type at a time, never matched on an `image/` prefix.**
  The prefix lets `image/svg+xml` through, and an SVG is a document that can run
  script.
- **The stored path is checked against the files directory again at read time.** A
  traversal that somehow got into the database must not become one that reads
  `/etc/passwd`.
- **Reaching an attachment follows the note, which follows its project.** Same
  chain as everything else. Do not let a client hand you the parent id and take it
  on trust.

---

## Import and export

The same format both ways: **a zip of Markdown files**, one per note, with a YAML
front matter block.

```markdown
---
created: 2019-04-02T10:15:00Z
modified: 2024-11-30T21:04:00Z
---

# Møde med Anders

Han vil gerne have kaffe hver uge.
```

- `created` / `created_at` / `date` and `modified` / `updated` / `updated_at` are
  all accepted on the way in. Obsidian and Bear write this block; so should you.
- Images travel in the zip beside the notes, and the links in the text are
  relative — **a note and its pictures must end up in the same archive**, which
  matters the moment an export is split into parts.
- File names collide: two notes called "Møde" are two notes, and a zip with one
  entry twice is a zip that loses one.

Limits that a real import taught, all of them cheap to add and expensive to omit:

- A ceiling on the upload, on the number of entries, and on the **unpacked** size.
  64 MB of zip expanded to 66 GB is a normal shape for an attack.
- A per-file limit as well as a whole-archive budget: one enormous photograph and
  ten thousand small ones are different attacks with the same ending.
- The entry name is never used as a path. Store by hash; the name is only a key
  for rewriting links.
- **Count what you skip, and say so.** A note whose name contained an ellipsis was
  dropped silently by a filter looking for `..` — one out of 1141, found only by
  counting at both ends.

### Timestamps on the way in

An importer needs to write `created_at` and `updated_at` from the file, and the
normal save path must refuse to let it.

`updated_at` means *when did this program last write this note*. Let any caller set
it and the column means whatever the last writer felt like. Give the importer its
own narrow entry point instead, and say in the comment why it exists.

Without those dates, a move of twelve hundred notes is a pile that was all written
the same evening — and the order they were written in is half of what an archive
is.

---

## What is still open here

Written down because a specification that only lists what works is a specification
that lies by omission.

- **Note bodies are plaintext at rest.** They hold API keys and passwords, and
  they are in every backup anybody can download. Encrypting them is bigger than it
  sounds: the body is in the full-text index and the folded column is generated
  from it, so both are a second plaintext copy in the same database. Encrypt and
  both have to go, and search has to be rewritten to decrypt in application code —
  fine at a thousand notes, a full scan per keystroke at ten thousand, at which
  point the answer is a keyed index (an HMAC per word).
- **Import never merges.** Every run creates new notes. Say so out loud in the UI.
- **There is no sync.** Export and the API are the way out.

---

## A short list of the traps, for the person in a hurry

1. A `body` field holding an excerpt will be saved over the real one.
2. Selected row and loaded note are two different pieces of state.
3. The title must be re-derived on every save or it goes stale.
4. Diacritic stripping is not folding, and folding is language-specific.
5. A tokenizer cannot see a word inside a word; decide what you do about it.
6. An attachment served inline is a script on your origin unless you allowlist by
   exact type and send `nosniff`.
7. Escaping for text and escaping for attributes are different functions.
8. One render failure must not be allowed to freeze the whole pane.
9. An unpacked-size limit is not optional on an archive import.
10. Let the importer set timestamps; let nobody else.
