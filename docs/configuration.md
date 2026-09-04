# Configuration

Everything is an environment variable. There is no config file — a Rune has
nowhere to put one that the operator would ever see.

## Core

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_BASE_URL` | `http://localhost:8080` | The public address. Invite links, password resets, calendar feeds and the mail-to-task address are all built from it. |
| `VERDANDE_ADDR` | `:8080` | Listen address. |
| `VERDANDE_DATA_DIR` | `/data` | Database, uploaded files and backups. |
| `VERDANDE_DEV` | `false` | Human-readable logs at debug level, and a relaxed origin check so a Vite dev server can connect. Never in production. |

## Behind a proxy

verdande keys its login rate limit and its audit log on the caller's address, so
that address has to be one the caller cannot forge. A forwarded-for header is
believed only when the machine that connected is a trusted proxy; a connection
straight off the internet is keyed on the address it really came from.

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_TRUSTED_PROXIES` | private networks | Whose `X-Forwarded-For` to believe, as comma-separated CIDRs. The default trusts loopback and the private ranges — the shape of a reverse proxy or tunnel sharing the host or the docker bridge. Set to `none` for an instance exposed directly with no proxy, so no forwarded header is ever believed. |
| `VERDANDE_REAL_IP_HEADER` | `X-Forwarded-For` | The header the client address is read from, for a trusted proxy. |

!!! warning "A proxy that does not overwrite the header"
    The default is safe when your proxy replaces a client-supplied
    `X-Forwarded-For` with the real address — nginx, Caddy and Cloudflare Tunnel all
    do. If yours passes the client's own header through, narrow
    `VERDANDE_TRUSTED_PROXIES` to just your proxy's address so a visitor cannot
    choose the address they are rate-limited and logged as.

## Sessions and links

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_SESSION_TTL` | `720h` | How long a login lasts. |
| `VERDANDE_INVITE_TTL` | `168h` | How long an invite link stays valid. |
| `VERDANDE_RESET_TTL` | `1h` | How long a password-reset link stays valid. |

Any Go duration: `720h`, `30m`, `45s`.

## Mail

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_SMTP_HOST` | — | Blank disables outbound mail entirely. |
| `VERDANDE_SMTP_PORT` | `587` | |
| `VERDANDE_SMTP_USER` | — | |
| `VERDANDE_SMTP_PASS` | — | |
| `VERDANDE_SMTP_FROM` | `verdande@localhost` | Sender address. Also decides the domain of the [mail-to-task address](mail.md). |
| `VERDANDE_SMTP_STARTTLS` | `true` unless port 465 | Port 465 is TLS from the first byte; everything else negotiates. |
| `VERDANDE_SMTP_INSECURE` | `false` | Skip certificate verification. For a self-signed internal mail server, and nothing else. |

!!! info "Running without a mail server"
    Perfectly supported. Invite links are shown in the app for you to send
    yourself, password resets are written to the log, and reminders arrive in the
    app and as Web Push rather than by email.

## Google

Only needed if anyone will connect a Gmail mailbox or a Google calendar. One
registration covers both: register an OAuth client in Google Cloud
(**APIs & Services → Credentials → OAuth client ID → Web application**) and add
**both** redirect URIs:

```
https://your-address/oauth/gmail/callback
https://your-address/oauth/calendar/callback
```

Then enable the APIs and scopes for whichever you want:

| For | Enable | Scope |
|---|---|---|
| Mailboxes | Gmail API | `gmail.modify` |
| Calendar | Google Calendar API | `calendar.readonly` |

| Variable | What it does |
|---|---|
| `VERDANDE_GMAIL_CLIENT_ID` | The client id from Google Cloud. |
| `VERDANDE_GMAIL_CLIENT_SECRET` | The client secret. |

One registration serves every user on the instance; each person authorises their
own mailbox from **Settings → Integrations → Gmail** and their own calendar from
**Settings → Integrations → Google Calendar**. Both redirect URIs are derived from
`VERDANDE_BASE_URL`, so they cannot drift out of step with what you registered.

!!! note "Two connections, two grants"
    Connecting a calendar does not disturb a working Gmail connection, and
    disconnecting one does not take the other with it. Google issues a refresh
    token per authorisation and keeps up to a hundred of them live per account,
    each carrying only the scopes it was granted with.

## Secrets

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_SECRET_KEY` | — | Encrypts the mail tokens and mailbox passwords in the database. 32 bytes of base64. |

Mail tokens and [mailbox](mailboxes.md) passwords are encrypted at rest, and the
key does not live in the database. That is the whole point: **backups can be
downloaded through the interface**, so a copy that ends up somewhere it should
not would otherwise open your mail.

Leave the variable empty and verdande writes a key to `secret.key` in the data
directory on first start, mode `0600`. Backups are a `VACUUM INTO` of the
database alone, so the key file is not in them.

Generate one yourself if you would rather keep it out of the volume entirely:

```
openssl rand -base64 32
```

!!! warning "Lose the key and the mailboxes must be reconnected"
    Nothing else is lost — tasks, projects, files and history are not encrypted —
    but a token that cannot be decrypted cannot be used, and there is no way back
    from a key that is gone. If you restore a database somewhere new, bring
    `secret.key` with it, or set `VERDANDE_SECRET_KEY` to the same value.

A database written before there was a key is read as it stands and encrypted the
next time each value is written, so turning this on costs nobody a reconnection.

## Update checks

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_UPDATE_CHECK` | `false` | Ask GitHub whether a newer release exists. |
| `VERDANDE_PANEL_URL` | — | The Yggdrasil panel this instance runs under. |
| `VERDANDE_PANEL_TOKEN` | — | An API token from that panel, belonging to somebody with control of this server. |
| `VERDANDE_PANEL_SERVER_ID` | — | Which server to restart — the id in the panel's URL. |

All three together let **Settings → Notifications** restart this instance,
which is also how it picks up a new version: a container cannot replace its own
image, so the panel recreates it and pulls `:latest` on the way. The request asks
the panel for a *scheduled* restart rather than an immediate one — an immediate
one is carried out while the request is still open, and stopping the container
kills the very process waiting for the answer, so the panel never gets to the
half that starts it again. Any one of the
three alone does nothing, and the page names the ones that are missing.

The token is a panel credential with control over that server. Anybody who can
read this environment can restart it — which is true of anybody with the host
anyway — and it is never sent to a browser.

Off unless you turn it on. When on, verdande asks the public GitHub releases API
at most every six hours and sends nothing about your instance — no identifier, no
version, no count of anything. Only administrators are shown the result.

## What is *not* configured here

**AI providers** are per-user, under settings in the app — see [AI](ai.md). One
person's key is not the instance's key.

**The VAPID keypair** for Web Push is generated on first use and stored in the
database. There is nothing to set.
