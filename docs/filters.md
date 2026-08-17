# Saved filters

A filter is a small expression describing a slice of your tasks. The grammar is
Todoist's, so if you are coming from there you already know it.

```
today & p1
#Firma & @regnskab
overdue | (today & assigned to: me)
7 days & !@venter
```

## What you can write

| Term | Matches |
|---|---|
| `today`, `i dag` | Due today. |
| `tomorrow`, `i morgen` | Due tomorrow. |
| `overdue`, `forsinket` | Due before today. |
| `7 days`, `14 dage`, `2 weeks` | Due between now and then. A window, not a single day. |
| `no date`, `ingen dato` | No due date at all. |
| `p1` … `p4` | That priority. |
| `#Firma` | In that project. |
| `@regnskab` | Carrying that label. |
| `assigned to: me`, `tildelt: mig` | Assigned to you. |
| `assigned to: anders@example.dk` | Assigned to that person. |
| `assigned to: none` | Assigned to nobody. |
| `recurring`, `gentagen` | Repeats. |
| `completed`, `færdig` | Already done. |
| `subtask`, `underopgave` | Has a parent. |
| `2026-12-24` | Due on that exact day. |
| anything else | Searched for in the title and description. |

## Combining them

| Operator | Means |
|---|---|
| `&` | Both. |
| `|` or `,` | Either. |
| `!` | Not. |
| `( )` | Grouping. |

`&` binds tighter than `|`, so `overdue | today & p1` means
`overdue | (today & p1)`. Use brackets when you mean otherwise.

## Some that are worth saving

```
today & p1                      What has to happen today
overdue                         What slipped
7 days & !@venter               The week, minus what you are blocked on
#Firma & assigned to: me        Your part of a shared project
p1 & no date                    Important, unscheduled — usually a mistake
recurring & overdue             Habits you have stopped doing
```

## Why an expression is refused

A filter is compiled before it is saved. A filter that cannot run is worse than
none: you would find out later, from a list that is empty for a reason nobody can
see. If you get an error, it names the part it could not read.

!!! note "Labels are personal"
    `@regnskab` matches *your* label of that name. Somebody else using the same
    word on their own tasks is a different label, and your filter will not find it.
