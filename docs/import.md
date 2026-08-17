# Importing from Todoist

## Getting the file out of Todoist

For one project: open it, **⋯ → Export as template → Export as CSV**.

For everything: **Settings → Backups**, then take the most recent one. That gives
you one CSV per project.

## Bringing it in

**Settings → Import → From Todoist**, then pick the file. One project comes in per
file.

## What comes across

| Todoist | verdande |
|---|---|
| Tasks, in their tree | Tasks, with sub-tasks nested the same way |
| Sections | Sections |
| Notes | Comments |
| Priority | Priority — see below |
| Due dates | Due dates, and recurrence where it can be read |
| Descriptions | Descriptions |

!!! info "Priorities are renumbered on purpose"
    Todoist's CSV numbers priorities the opposite way to how its interface shows
    them: what you see as P1 is written as `4`. verdande stores what the interface
    means, so the two are converted. A P1 in Todoist is a P1 here.

## Dates

Todoist stores what you typed rather than a date — `every Monday`, `tomorrow`,
`15 Mar`. Machine-readable dates come across exactly. Anything else goes through
the same [quick-add parser](quick-add.md) the app uses, which understands a great
deal of it in both languages — including recurrence, so `every Monday` arrives as a
real repeating task.

Anything it genuinely cannot read is listed in the import result rather than
dropped silently.

## What does not come across

**Assignees.** A person in your Todoist workspace is not a person here. The import
lists them so you can reassign.

**Attachments.** Todoist's CSV does not include them; they are links into Todoist's
own storage.

**Reminders and filters.** Not in the export format.

## Any other CSV

**Settings → Import → From a CSV file** takes anything with a header row, and asks
you which column means what. The only one it needs is the task text.

## Getting back out

Nothing here is a one-way door.

- **A project as Todoist CSV** — the same format, so it opens in Todoist.
- **A project as ICS** — a calendar file.
- **Your whole account as JSON** — every project, task, comment, label and filter.

A CSV that goes in and comes back out again is the same project: the round trip is
tested, and two exports of an unchanged project are byte-identical.
