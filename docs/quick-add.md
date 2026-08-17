# Quick add

One line in, a fully-formed task out.

```
betal moms i morgen kl 10 p1 #Firma @regnskab
```

becomes a task called **betal moms**, due tomorrow at 10:00, priority 1, in the
Firma project, labelled regnskab.

The parts it understands are tinted underneath the text as you type, so you can see
it being read before you commit to it.

## What it reads

| You type | It means |
|---|---|
| `#Firma` | Put it in that project. `#"Q3 rapport"` for a name with spaces. |
| `@regnskab` | Add that label. As many as you like. |
| `p1` … `p4` | Priority. `!!!`, `!!` and `!` also work. |
| `i dag`, `i morgen`, `i overmorgen` | Today, tomorrow, the day after. |
| `mandag`, `på fredag` | The next such weekday. |
| `næste mandag` | Next week's Monday, counted from Monday — not merely the next one to come round. |
| `om 3 dage`, `om en uge`, `næste måned` | Relative dates. |
| `15. marts`, `15/3`, `2026-03-15` | Written dates. Day first. |
| `kl 10`, `kl 14:30`, `klokken 16` | A clock time. |
| `hver mandag`, `hverdage`, `hver 2. uge` | Makes it [repeat](#repeating-tasks). |

English works everywhere Danish does: `tomorrow`, `next friday`, `in 3 days`,
`every 2 weeks`, `at 10am`, `weekdays`.

## Mixing the two

```
ring til Anders on friday kl 14 #Arbejde
```

Both languages are read at once rather than behind a setting, because people mix
them mid-sentence. The only thing the language setting decides is a bare hour after
English `at`: `at 3` means three in the afternoon, while Danish `kl 3` means three
in the morning — Danish has no am/pm habit and the 24-hour clock is what people
mean.

## Repeating tasks

```
vand planterne hver mandag
standup hverdage kl 9
betal husleje den 1. i måneden
review every 2 weeks
```

Stored as an RFC 5545 RRULE, so a task exported to Apple Reminders keeps repeating
there.

Ticking off a repeating task **moves it to its next occurrence** rather than
closing it. The same task keeps its id, so its sub-tasks and comments stay
attached — a weekly review is one thing that recurs, not fifty-two things. The
completion is still recorded, so "what did I get done this week" includes the
chores that repeat.

## Dates it cannot read

Anything it does not recognise stays in the title. A task with a clumsy title beats
an error message and a lost thought.

!!! tip "Numeric dates are day-first"
    `15/3` is the fifteenth of March. That is how both Danish and British English
    write it, and guessing month-first would silently move a date by months rather
    than failing visibly.

## The shortcuts

Press ++q++ to jump to the quick-add box from anywhere, ++t++ for Today, ++u++ for
Upcoming, ++cmd+k++ to search, and ++question++ for the full list.
