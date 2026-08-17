# Calendar and CalDAV

Two ways to get your tasks into a calendar, and they are not the same thing.

| | [Feed](#subscribing-to-a-feed) | [CalDAV](#connecting-a-caldav-client) |
|---|---|---|
| Direction | Read only | Two-way |
| Works with | Anything that subscribes to a URL | Apple Reminders, Thunderbird, DAVx⁵ |
| You can tick things off | No | Yes |
| Setup | Paste a URL | Username and token |

## Subscribing to a feed

**Settings → Calendar feed** gives you a URL. Paste it into whatever subscribes to
calendars.

=== "Apple Calendar"

    **File → New Calendar Subscription**, paste the URL, and set auto-refresh to
    every hour.

=== "Google Calendar"

    **Other calendars → + → From URL**. Google refetches on its own schedule,
    which can be many hours — this is Google's behaviour and nothing here can
    change it.

=== "Thunderbird"

    **New Calendar → On the Network → iCalendar (ICS)**.

Tasks go out as `VTODO`. Google ignores those entirely, so tasks with a clock time
are *also* sent as events — which is what makes the feed useful in the calendar
most people actually keep.

!!! warning "The URL is the password"
    Anybody with the link can read your tasks. Calendar clients cannot log in, so
    there is nothing else it could be. If it ends up somewhere it should not,
    **Settings → Calendar feed → Rotate** issues a new one and breaks the old
    immediately.

## Connecting a CalDAV client

This is the one where ticking something off in Apple Reminders ticks it off here.

**First, make a token.** **Settings → API tokens → New token**. Copy it — it is
shown once.

Then:

- **Server**: your verdande address
- **Username**: your email address
- **Password**: the API token, **not your account password**

=== "Apple Reminders (macOS/iOS)"

    **Settings → Accounts → Add Other Account → CalDAV account**, choose *Manual*,
    and fill in the three fields above.

=== "Thunderbird"

    **New Calendar → On the Network**, enter your email as the username and the
    address as the location. It will find the collections itself.

=== "DAVx⁵ (Android)"

    **Add account → Login with URL and username**. Then pick which projects to
    sync.

Each project appears as its own calendar, so you can turn them on and off
individually.

!!! note "Why a token and not your password"
    A CalDAV client stores what you give it and sends it on every request. A token
    can only read and write tasks — it cannot sign in to the app or change your
    password — and you can revoke one without touching anything else.

## What syncs

Task titles, notes, due dates and times, priorities, repetition, and completion —
in both directions.

Not sections, labels, sub-task relationships, comments or attachments. CalDAV's
VTODO has nowhere to put them, and inventing an encoding would produce something no
other client could read.

Completed tasks stay in the collection. A client that stops seeing one treats it as
deleted and drops its own record of having done it.
