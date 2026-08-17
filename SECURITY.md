# Security

## Reporting

Email **kristianwind@gmail.com** rather than opening an issue. Include what you
found, how to reproduce it, and what an attacker could do with it. You will get a
reply within a week.

## What verdande does about the obvious things

- **Passwords** are Argon2id with the parameters recorded in the hash, so they can
  be raised later without locking anyone out.
- **Every token** — session, invite, reset, API, calendar feed, mail address — is
  stored only as a SHA-256 hash. A database dump contains nothing that can be
  presented.
- **Sessions** are `httpOnly`, `SameSite=Lax`, and `__Host-` prefixed over HTTPS.
- **Uploads** are stored content-addressed by hash and always served as
  `application/octet-stream` with `nosniff`, so an uploaded SVG cannot run script
  on this origin.
- **The filter language** compiles to parameterised SQL. No typed text is ever
  concatenated into a statement.
- **Permission checks** are route middleware, and anything you may not see answers
  404 rather than 403 — a 403 confirms the thing exists.
- **The CSP** is computed at startup by hashing the inline scripts in the built
  page, rather than allowing `unsafe-inline`.

## What it does not do

verdande has no rate limiting beyond the login and password-reset endpoints, and
no audit log of reads. It is built to be run behind something — a Cloudflare
Tunnel, a reverse proxy, a VPN — by somebody who knows what they exposed.
