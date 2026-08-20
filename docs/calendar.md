# Calendar

Your dated tasks as a grid, with other people's calendars laid under them.

**Calendar** in the menu shows a month or a week. The week is a day: hours down the
side, and everything with a time on it in its place. Two things at ten o'clock sit
beside each other, because that is a clash you should be able to see rather than
read.

## What is in it

- **Your tasks**, on the day they are due — and at the time, if they have one.
- **Events from calendars you have connected**, read-only.

Drag a task to another day to move it. Drop it on the hour column and it takes that
time; drop it in the band across the top and it keeps the day and loses the time.
Events cannot be dragged: verdande holds a copy of somebody else's calendar, and a
chip that lets itself be moved is a promise the server cannot keep.

A project's own calendar is under **Calendar** on the project page, with the same
week and month.

## Subscribing to a calendar

**Settings → Integrations → Subscriptions.** Paste an address and it is fetched at
once.

This works with anything that publishes an `.ics` file: public holidays, a club's
fixtures, a shared calendar from work, your own calendar's secret address from
Google, Apple or Fastmail.

- **`webcal://` addresses work.** They are the same URL over https with a scheme
  that makes a desktop hand it to the calendar app; it is rewritten for you.
- **Several at a time.** Unlike the Google connection, which is one account, a
  subscription is one address and you can have as many as you like.
- **Read-only, refreshed every fifteen minutes.**
- The address is fetched once before it is stored, so a typo tells you now rather
  than by silently never filling.

!!! note "This is the way in for a private account"
    If the instance's Google registration is *Internal*, Google refuses accounts
    from outside the organisation with `org_internal` — no setting on your side
    changes that. Your calendar's own secret ICS address goes through this panel
    and works regardless.

## Connecting Google Calendar

**Settings → Integrations → Google Calendar.** It shares the OAuth client Gmail
uses, so the instance needs that registration first — see
[Configuration](configuration.md#google).

Two things have to be true in Google Cloud, and only the owner of the registration
can do them:

1. **Google Calendar API** is enabled for the project.
2. The calendar callback is listed as an authorised redirect URI. The exact string
   is printed in the panel — copy it from there rather than typing it. Google
   compares it exactly: scheme, host, path, no trailing slash. `redirect_uri_mismatch`
   almost always means one of those four differs.

Once connected, tick the calendars you want to see. The list is refreshed on every
sync, so a calendar added or renamed since last time appears under its new name.

!!! warning "Written to the specification, not yet proven against Google"
    The Google Calendar half is implemented from Google's documentation and tested
    against a stand-in server. It has not been exercised against Google itself.
    The subscription route above and the outgoing feed below have.

## Your own calendar, elsewhere

Two ways out, for two different jobs:

- **[The ICS feed](caldav.md#subscribing-to-a-feed)** — a read-only address for Apple Calendar,
  Google or Thunderbird. Your tasks appear as dated entries.
- **[CalDAV](caldav.md)** — two-way. Tick something off in Apple Reminders and it
  is ticked off here.

The feed address is a credential in itself: anybody holding it can read your tasks
without logging in, because a calendar client cannot sign in. Rotate it from
**Settings → Integrations** if it gets out.

## What it does not do

- **It does not write to a connected calendar.** Events are a copy.
- **It does not expand repeating events** from a subscribed file. A weekly meeting
  in somebody else's calendar is stored as its first occurrence.
- **It does not resolve named time zones** from a subscribed file. An event carrying
  `TZID=Europe/Copenhagen` shows the clock the file wrote, rather than one this
  server has moved.
- **Only Google, and only reading**, for account connections. Everything else goes
  through a subscription.
