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

## Gmail

Only needed if anyone will connect a mailbox. Register an OAuth client in Google
Cloud (**APIs & Services → Credentials → OAuth client ID → Web application**) with
the redirect URI `https://your-address/oauth/gmail/callback`, enable the Gmail API,
and add the `gmail.readonly` scope.

| Variable | What it does |
|---|---|
| `VERDANDE_GMAIL_CLIENT_ID` | The client id from Google Cloud. |
| `VERDANDE_GMAIL_CLIENT_SECRET` | The client secret. |

One registration serves every user on the instance; each person authorises their
own mailbox from **Settings → Gmail**. The redirect URI is derived from
`VERDANDE_BASE_URL`, so it cannot drift out of step with what you registered.

## Update checks

| Variable | Default | What it does |
|---|---|---|
| `VERDANDE_UPDATE_CHECK` | `false` | Ask GitHub whether a newer release exists. |
| `VERDANDE_PANEL_URL` | — | The Yggdrasil panel this instance runs under. |
| `VERDANDE_PANEL_TOKEN` | — | An API token from that panel, belonging to somebody with control of this server. |
| `VERDANDE_PANEL_SERVER_ID` | — | Which server to restart — the id in the panel's URL. |

All three together let **Indstillinger → Notifikationer** restart this instance,
which is also how it picks up a new version: a container cannot replace its own
image, so the panel recreates it and pulls `:latest` on the way. Any one of the
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
