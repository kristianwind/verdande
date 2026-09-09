# AI features

Optional, off by default, and bring your own key. verdande works completely without
them — this is a convenience on top of a to-do app, not something the app depends
on.

## Setting up a provider

**Settings → AI**. Four kinds:

=== "Anthropic"

    - **Provider**: Anthropic
    - **Model**: `claude-sonnet-5`
    - **API key**: from [console.anthropic.com](https://console.anthropic.com)

=== "OpenAI"

    - **Provider**: OpenAI
    - **Model**: `gpt-4o`
    - **API key**: from platform.openai.com

=== "Google"

    - **Provider**: Google
    - **Model**: `gemini-2.0-flash`
    - **API key**: from Google AI Studio

=== "Your own model"

    Anything speaking the OpenAI-compatible shape — Ollama, vLLM, LM Studio, a
    company gateway.

    - **Provider**: Compatible
    - **Base URL**: `http://localhost:11434/v1`
    - **Model**: `llama3.1` — whatever you have pulled
    - **API key**: usually blank

    This is why the abstraction exists at all: an integration that only spoke to a
    hosted API would be no use to somebody running their own models.

The key is stored on your account and is **never sent back** to the settings page —
a page that repopulates a password field is one that will eventually leak it into a
screenshot.

## The shape of all of it

**The model proposes; you decide.** Nothing is written until you say yes, and the
proposal is a line in the same syntax you type yourself — `Ring til Anders i morgen
p1 #Firma` — sitting in a field you can edit before you accept it.

That is not caution for its own sake. A model that edits your tasks directly has to
be right *every* time to be worth having. One that suggests only has to be right
often enough to save you some typing, and when it is wrong you have already seen it.

It is also why the suggestion is a line rather than a set of fields: there is one
reading of "i morgen" in this program, and a suggestion goes through the same
[quick add](quick-add.md) parser as anything you type — including after you have
edited it by hand.

The model is given the names of the projects that exist and may name no others. A
`#Projekt` it invented would create one by accident the first time you accepted it,
and a guess must not be able to move a task somewhere you will not look for it.

## What it can do

**Tidy up the inbox.** In the Inbox, the menu → *Ryd op i indbakken*. For each loose
line it suggests how you would have written it if you had finished it — project,
date, priority — with one sentence on what it read out of the task. Accept, edit, or
skip, one line at a time.

**Find the tasks in a note.** Open a note → *Find opgaver i noten*. Only what
somebody committed to or was asked to do; a heading is not a task and a topic that
was discussed is not a task. Each suggestion quotes the words it came from, so a
wrong one can be seen through rather than merely being wrong. The note itself is
never edited — the task carries `[[the note's title]]` in its description instead.

**Today's plan, in the morning.** **Settings → Beskeder → Dagens plan**, off by
default; pick the hour yourself. One notification about what is due today and what
has been missed, and what to start with — and the same text as a card at the top of
**I dag**, where you are looking anyway when you start the day. The notification is
the reminder; the card is where it stays.

The card shows the plan *as it was sent*, with the time it was made, rather than a
new one each time you look: a plan that says something different when you look at it
than it said when it arrived is worse than either. **Forny planen** makes a new one
when the day has moved, and it does not send a notification about something you are
already looking at. With the morning notification off, the card offers to make one
on the spot.

The counts are done in the database and handed to the model as facts it may not
recompute — a plan that says four tasks where there are three is worse than no plan.
With no model configured the counts stand on their own, which is what you needed to
know at seven in the morning anyway. Nothing due and nothing overdue sends nothing.

**Mail that becomes a task you can act on.** Where a
[mailbox](mailboxes.md) used to produce *"Anders Jensen: SV: SV: Vedr. levering uge
12"*, the model writes the action the mail asks for — and leaves the mail alone if
it asks for nothing. The subject stays in the description, so a mail it misread can
still be read. If the model is unavailable the task is named sender and subject as
before: a model that is down must not be able to stop the post.

**Ask your own notes and tasks.** ⌘K, type the question, ⌘⏎. *"Hvad lovede jeg
Anders i august?"* — search finds what contains the words, this answers the
question, and the answer links to the notes and tasks it rests on. If nothing
matches, it says so; a guess would be useless here, because you are asking precisely
because you cannot remember.

**Split a task into sub-tasks.** On any task: *Split with AI*. Two to seven concrete
actions, created as sub-tasks.

**A weekly summary.** A short note over what is outstanding: what looks urgent and
what appears to be slipping. At most six lines — a prompt to think, not a report.

## What is sent

Only what the feature needs, and only when you invoke it:

| Feature | What leaves the instance |
| --- | --- |
| Tidy the inbox | The titles of the loose tasks in your inbox, the first words of their descriptions, and your project names |
| Tasks in a note | That one note's text, and your project names |
| Today's plan | The titles and dates of what is due or overdue today |
| Mail to task | Sender, subject and the snippet of the mails in that sweep |
| Ask | Your question, and the notes and tasks the search matched |
| Split a task | That task's title and notes |
| Weekly summary | The titles, dates and priorities of your open tasks |

Never comments, never attachments, never a project the feature was not invoked on,
and never anything belonging to somebody else. Nothing is sent to anybody until you
configure a provider, and with a local model nothing leaves your machine at all.

## Turning it off

Clear the model field. Every AI feature reports itself as unavailable rather than
failing, and the rest of the app is unchanged.
