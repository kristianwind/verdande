# Two editions: verdande and urd

One program, built once, with two faces.

| | **verdande** | **urd** |
|---|---|---|
| What it is | Tasks, projects and notes | Notes |
| `VERDANDE_EDITION` | `full` (the default) | `notes` |
| Routes served | 200 | 171 |
| Mark | ᚹ | ᚢ |

Set `VERDANDE_EDITION=notes` and the instance is urd: the task routes are not
mounted, the sidebar has no way to them, search answers with notes only, and the
program calls itself urd in its tab, its wordmark and its web manifest.

## Why one codebase and not a fork

The two share everything that is hard: accounts, sessions, passkeys, sharing,
invitations, the realtime socket, search, the backup format, the migrations. Notes
are about a twentieth of the Go in this repository and reference no task type;
tasks reference no note type. A fork would have duplicated the other nineteen
twentieths in order to separate the one — and then every security fix would have
needed applying twice, by hand, forever.

So the split is a configuration value read in one place, and the thing that keeps
it honest is a test rather than a convention: `TestNotesEditionServesOnlyItsOwnRoutes`
walks both routers, compares them against a written-down list of the 29 withheld
routes, and fails in **both** directions — a route that appears in the notes
edition and a route that stops being withheld are both a failure.

## An unknown value refuses to start

`VERDANDE_EDITION=note`, `Notes` and `none` are all rejected at startup, by name,
rather than being treated as "not notes".

The silent direction is the dangerous one here. An operator who sets up an instance
for notes and mistypes the value would otherwise get every task route served on it,
with no error anywhere and no way to tell from the outside.

## What urd does not have

The 29 withheld routes are tasks, sub-tasks, labels, saved filters, reminders, the
upcoming and delegated views, and the task half of search and export. Everything
else is the same program:

- accounts, invitations, passkeys and two-factor
- projects — which in urd are what a note belongs to
- per-note sharing with other people, independent of projects
- search, over notes
- export, of notes
- the mail, calendar and MCP surfaces, minus their task routes

## The name

Verðandi and Urðr are two of the three Norns in Völuspá, and the poem has them
cutting marks into wood — the act of writing something down. Verdande is what is
becoming; Urd is what has become and was kept.

An instance started without the task routes is a different product to the person
using it, so it says a different name. That name is read from the configuration in
one place (`Config.ProductName`), which is why there is no build-time flag and no
second frontend: the frontend is byte-identical in both editions, and the server
renames the two files a browser reads before any JavaScript runs — the HTML shell's
title and the web manifest.

!!! note "The beacon counts them separately"
    Both editions report the same two values once a day — instance id and version
    — and nothing else; see [The install count](beacon.md). The edition is not in
    the payload, so the count does not distinguish them.
