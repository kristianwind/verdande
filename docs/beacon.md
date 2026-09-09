# The install count

verdande reports that it exists. Two values, once a day:

```json
{
  "instance_id": "01a0806d-3601-7000-ad76-057f1f684881",
  "version": "v0.44.1"
}
```

That is the whole message. No IP is stored by the collector, no hostname, no
domain, no account, no counts of anything you have made, nothing about what you
use it for. The payload is small enough to be printed in full on the settings page,
and it is printed there — a promise about telemetry is worth exactly as much as
your ability to check it.

## On by default, off in one click

The alternative that was considered first was compulsory, and the argument against
it is practical rather than principled: it cannot be enforced. The domain can be
blocked, the binary can be patched, and a fork removes it in its first commit. So
mandatory buys nothing over on-by-default while costing the trust the rest of this
program works for.

Which is why the first administrator to sign in is **told, unprompted**, with the
real id, the real version and the real address in front of them, and two buttons.
On by default is only defensible if it is said out loud without being asked; a
settings page where it is stated fully is not the same thing, because that page is
opened by people who already suspect there is something to read.

**Settings → Data** afterwards: one switch. An explicit "off" survives upgrades.

!!! note "This disagrees with the update check, on purpose"

    `VERDANDE_UPDATE_CHECK` is off unless you ask for it, and its reasoning is that
    a self-hosted app reaching out unbidden has broken the deal its operator made by
    self-hosting. That is the same situation as this one, and the two defaults do
    not agree. The case for treating them differently: the update check reveals an
    instance to a third party (GitHub) and returns something you can act on, while
    this returns nothing and goes to the project. Whether that is a distinction or
    an excuse is a judgement call, and it was made deliberately.

## What the collector cannot see, and what it can

A request has a source address whether or not anybody writes it down. The collector
stores none and logs none, and the code is arranged so the address never reaches a
function that could — the table has nowhere to put one. But the request passes
through a reverse proxy and a tunnel on the way, and those keep their own logs.
"We send no IP" would be a half-truth; what is true is that nothing here records
one.

The ping is not authenticated either, so anybody can post a made-up id and inflate
the count. That is an accepted trade: a token would have to ship inside every
install anyway.

## Running your own count

Somebody running a fleet points **Settings → Data → Modtagerens adresse** at their
own instance, and ticks **Denne instans er tælleren** there. Until that is ticked,
`POST /api/v1/beacon` answers **404** rather than accepting quietly — an instance
nobody made a collector must not become a bucket anyone can post into.

The collector's page then shows totals, active in 7 and 30 days, and a breakdown by
version. Publishing that number at `/api/v1/beacon/count` is a second, separate
opt-in, and it has a floor under it (25 by default): a number that *falls* cannot be
published as a fact — a bad release, a DNS change that quietly stops the pings, or
lost rows all look like "fewer people use this now" — and a small number mostly says
how little it takes to move it.

## When it cannot reach

The reason is written down and shown on the settings page. A beacon that cannot
reach its collector otherwise fails in complete silence: it tries again tomorrow,
the page says "last sent" with a date that quietly gets older, and nobody notices.
