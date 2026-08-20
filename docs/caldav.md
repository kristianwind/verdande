# Calendar and CalDAV

Two ways to get your tasks into a calendar, one way to get a calendar into
verdande, and none of the three is the same thing.

| | [Feed](#subscribing-to-a-feed) | [CalDAV](#connecting-a-caldav-client) | [Google Calendar](#showing-a-google-calendar-in-verdande) |
|---|---|---|---|
| Direction | Tasks out, read only | Tasks out, two-way | Events **in**, read only |
| Works with | Anything that subscribes to a URL | Apple Reminders, Thunderbird, DAVx⁵ | A Google account |
| You can tick things off | No | Yes | No — an event is not a task |
| Setup | Paste a URL | Username and token | Connect, then pick calendars |

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

## Showing a Google calendar in verdande

The two sections above send your tasks *out*. This is the other direction: your
Google calendar drawn underneath them, so one screen answers "what does this day
already have in it".

**Settings → Integrations → Google Calendar → Connect**, then tick the calendars
you want and press Save. They appear in **Calendar** in the sidebar, in Google's
own colour for each one, beside your own tasks with due dates.

It needs the same OAuth client Gmail uses, with the Calendar API enabled and the
second redirect URI registered — see [Configuration](configuration.md#google).

!!! warning "Read only"
    verdande never writes to your Google calendar. An event cannot be dragged,
    edited or ticked off here; clicking one opens it in Google, which is where it
    can be changed. Your own tasks in the same grid still drag to another day as
    they always have.

    Writing would need the `calendar.events` scope and an answer to what should
    happen when both sides moved the same meeting — which is a synchronisation
    model rather than a scope, and it is deliberately not built yet.

!!! note "The Internal client applies here too"
    An OAuth client registered as *Internal* in a Google Workspace can only be
    used by accounts in that workspace, calendars included. A personal
    `@gmail.com` address is refused with `org_internal`. For one personal account
    the shorter road is the calendar's own secret address: in Google Calendar,
    **Settings → the calendar → Integrate calendar → Secret address in iCal
    format**. Nothing in verdande subscribes to one yet — it is the next piece of
    this feature.

Events are kept for ninety days back and a year forward, refreshed every fifteen
minutes and whenever you press **Fetch now**. Paging the grid past that window
says so rather than showing an empty month.

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
