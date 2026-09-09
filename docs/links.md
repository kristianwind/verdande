# Opening links

A link in a task or a note opens in whichever browser your system opens links in.
**Settings → Links** can try to change that — and the honest description of what it
does is *try*.

## What a web page cannot do

Two things, and they set the limits of this feature:

- **It cannot see which browsers are installed.** The list is deliberately closed:
  it would be a fingerprint, distinctive enough to follow somebody from site to
  site.
- **It cannot ask the system to use a particular one.** A link goes to the default
  browser, and there is no API that says otherwise.

The only handle that exists is the URL schemes browsers register for themselves —
`firefox://open-url?url=…`, `googlechrome://…`, `microsoft-edge:…` — and there is no
way to ask whether one exists. You can only try it.

## So the setting is an attempt

Pick Safari, Chrome, Firefox or Edge, or write your own scheme. When you click a
link, that scheme is tried first; if nothing has happened within about half a
second, the link opens the ordinary way.

The fallback is the whole reason this is safe to turn on. Without it, a setting
that works on your phone would leave links doing nothing on your desktop.

In practice: **it works on a phone**, where browsers register these schemes, and
**rarely on a Mac or a PC**, where Chrome and Firefox do not. Edge on Windows is
the usual exception. There is a **Prøv det** button for exactly this reason — you
cannot ask whether a scheme exists, so it is better to find out on example.dk than
on a link you meant to follow.

## Writing your own

Three placeholders, because browsers disagree about the shape they want the address
in:

| | |
| --- | --- |
| `{url}` | The address as it is — `x-safari-https://example.dk` |
| `{encoded}` | The address as a parameter value — `firefox://open-url?url=https%3A%2F%2F…` |
| `{stripped}` | The address without the `https://` — `googlechrome://example.dk` |

## It belongs to the machine, not the account

The choice is kept in the browser you are sitting at, not on your account — the
same rule the sidebar's width follows. `firefox://` works on the phone and does
nothing on the Mac, so a choice that followed the account would be wrong in one of
the two places every time.
