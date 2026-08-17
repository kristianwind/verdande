# Self-hosting

verdande is one static binary and one directory. There is no database server, no
cache, no queue and no second process.

## Docker

!!! note "If the pull is refused"
    The GHCR package is public, so this needs no credentials. If it is ever set
    back to private, `docker login ghcr.io` with a token carrying the
    `read:packages` scope before pulling — and see
    [the Rune page](rune.md#if-the-image-will-not-pull), because a private image
    fails differently and much more confusingly under Yggdrasil.

```bash
docker run -d --name verdande \
  --restart unless-stopped \
  -p 8080:8080 \
  -v verdande-data:/data \
  -e VERDANDE_BASE_URL=https://todo.example.dk \
  ghcr.io/kristianwind/verdande:latest
```

`VERDANDE_BASE_URL` has to be the address you actually reach it on. Invite links,
password resets and calendar feeds are all built from it, so getting it wrong
means the links in your email point somewhere that does not answer.

## Docker Compose

```yaml
services:
  verdande:
    image: ghcr.io/kristianwind/verdande:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      VERDANDE_BASE_URL: https://todo.example.dk
      VERDANDE_SMTP_HOST: mail.example.dk
      VERDANDE_SMTP_USER: verdande@example.dk
      VERDANDE_SMTP_PASS: ${SMTP_PASSWORD}
      VERDANDE_SMTP_FROM: verdande@example.dk
```

## Behind a reverse proxy

verdande expects to be terminated by something else. It reads `X-Forwarded-For`,
so rate limiting and the activity log see the real caller rather than the proxy.

=== "Caddy"

    ```
    todo.example.dk {
        reverse_proxy localhost:8080
    }
    ```

=== "Cloudflare Tunnel"

    ```yaml
    ingress:
      - hostname: todo.example.dk
        service: http://localhost:8080
      - service: http_status:404
    ```

    The WebSocket needs no special handling; Cloudflare proxies it as-is. A silent
    socket is closed after 100 seconds, which is why verdande pings every 30.

!!! warning "Use HTTPS"
    Over plain HTTP the session cookie cannot be `Secure`, and verdande will not
    use the `__Host-` prefix that stops a subdomain overwriting it. Fine on a
    laptop; not fine on anything reachable from outside.

## First run

Open the address and create an account. The first one is the administrator, and
the endpoint that creates it refuses once any account exists — so there is no
window in which somebody else can claim your instance.

Everybody after that joins by invite.

## What lives in `/data`

```
/data
├── verdande.db      SQLite, WAL mode
├── files/           attachments, content-addressed
└── backups/         nightly snapshots, 14 kept
```

Back up the whole directory.

!!! danger "Do not copy verdande.db on its own"
    It runs in WAL mode, so recent writes live in `verdande.db-wal` until they are
    checkpointed. A copy of the `.db` file alone can be missing them. Copy the
    whole directory, or take one of the nightly snapshots from `backups/` — those
    are written with `VACUUM INTO` and are complete on their own.

## Backups

A snapshot is written once a day and the most recent fourteen are kept. They are
counted rather than aged out, so a container that was off for a month comes back
with its backups intact.

## Updating

```bash
docker compose pull && docker compose up -d
```

Migrations run at startup, each in its own transaction. Take a copy of `/data`
first anyway.
