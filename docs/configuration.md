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

## What is *not* configured here

**AI providers** are per-user, under settings in the app — see [AI](ai.md). One
person's key is not the instance's key.

**The VAPID keypair** for Web Push is generated on first use and stored in the
database. There is nothing to set.
