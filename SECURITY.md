# Security

## Reporting

Email **kristianwind@gmail.com** rather than opening an issue. Include what you
found, how to reproduce it, and what an attacker could do with it. You will get a
reply within a week.

## What verdande does about the obvious things

- **Passwords** are Argon2id with the parameters recorded in the hash, so they can
  be raised later without locking anyone out.
- **Session, invite, reset and API tokens** are stored only as a SHA-256 hash. A
  database dump contains nothing that can be presented.
- **The calendar feed address and the mail-to-task address are not hashed.** They
  are looked up by their exact value, and they are shown in the interface more
  than once, which a hash cannot do. Anybody holding a backup file therefore holds
  those two URLs: the feed reads that person's tasks without a login, and the mail
  address files tasks into their inbox. Rotating either from **Settings →
  Integrations** invalidates the old one. This is written down rather than fixed
  because fixing it means the address can only ever be shown once, which is a
  decision about how the feature works and not only about how it is stored.
- **Mailbox passwords, OAuth tokens, TOTP secrets and AI keys** are sealed with
  AES-GCM under a key kept outside the database — see `internal/secret`. That is
  what makes a stray copy of the database less than a stray copy of everything:
  the backup does not carry the key.
- **Sessions** are `httpOnly`, `SameSite=Lax`, and `__Host-` prefixed over HTTPS.
- **Uploads** are stored content-addressed by hash and served as
  `application/octet-stream` with `nosniff` — with one exception, added so that a
  note can show the pictures in it: a short allowlist of raster image types is
  served inline as its own type. The list is named one format at a time rather
  than matched on an `image/` prefix, because the prefix would let `image/svg+xml`
  through, and an SVG is a document that can run script. `nosniff` stays, so the
  browser must read the file as that image or not at all.
- **The filter language** compiles to parameterised SQL. No typed text is ever
  concatenated into a statement.
- **Permission checks** are route middleware, and anything you may not see answers
  404 rather than 403 — a 403 confirms the thing exists.
- **The CSP** is computed at startup by hashing the inline scripts in the built
  page, rather than allowing `unsafe-inline`.

- **Note bodies, task titles, descriptions and comments are not encrypted.** The
  sealing above covers credentials — mailbox passwords, OAuth tokens, TOTP secrets,
  AI keys — and nothing else. A note holding a password is holding it in plain text
  in every backup that can be downloaded. Encrypting note bodies is on the list and
  is larger than it sounds: the full-text index and the folded search column are
  both derived from the body, so both would have to go and search would have to be
  rewritten to decrypt in application code.

## What it does not do

Rate limiting covers the endpoints a stranger can reach: signing in, resetting a
password, the second factor, the installation beacon, and inbound mail. **Nothing
else is limited** — an authenticated caller can ask as often as they like, and a
signed-in account that wanted to be a nuisance can be one.

There is no audit log of reads. Who *changed* something is recorded per project;
who looked at it is not.

It is built to be run behind something — a Cloudflare Tunnel, a reverse proxy, a
VPN — by somebody who knows what they exposed.
