# verdande

Self-hosted tasks and projects, shared with the people you share the work with.
One binary with SQLite inside it — no database to provision, no second process.

**Verdande** is one of the three Norns: the one who spins *that which is becoming*.
Not what has been, and not what is fated to be — the thing you are in the middle
of. Which is what a to-do list is.

## What it is for

Todoist Pro, on your own hardware, with your own data. And with the integrations a
hosted product will not build for you: a real CalDAV server, an address you can
forward mail to, and an MCP endpoint so Claude can work your task list directly.

## What it does

- **[Quick add](quick-add.md) that reads what you type.** `betal moms i morgen kl
  10 p1 #Firma @regnskab` becomes a task due tomorrow at 10:00, priority 1, in
  Firma, labelled regnskab. Danish and English, mixed freely in one line.
- **Projects, sections, sub-tasks, labels, [saved filters](filters.md).** List,
  board and calendar views.
- **Sharing** with owner, editor and viewer roles. Anyone outside your instance
  joins through an invite link.
- **Live sync** — a change made by somebody else appears without a refresh.
- **Repeating tasks** as RRULE, reminders, comments and attachments.
- **[Calendar](caldav.md), [mail](mail.md) and [Claude](mcp.md)** connected over
  open standards rather than private formats.

## Getting it running

```bash
docker run -d --name verdande \
  -p 8080:8080 \
  -v verdande-data:/data \
  -e VERDANDE_BASE_URL=https://todo.example.dk \
  ghcr.io/kristianwind/verdande:latest
```

Open the address, create the first account — that account is the administrator.

Running Yggdrasil Panel? [Install it as a Rune](rune.md) instead.

## What it will not do

No karma or points. No team workspaces. No native apps. No mail integration beyond
Gmail and the forwarding address. No languages beyond Danish and English. And no
sync protocol of its own — [CalDAV](caldav.md) and the [API](api.md) *are* the
sync.
