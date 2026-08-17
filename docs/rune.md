# Installing as a Rune

verdande ships with a Rune manifest for
[Yggdrasil Panel](https://github.com/kristianwind/yggdrasil).

## Before you start: the registry is private

The image lives at `ghcr.io/kristianwind/verdande` and the package is **private**,
so an anonymous pull fails with a message that reads as though the image does not
exist at all:

```
create container: Error response from daemon:
No such image: ghcr.io/kristianwind/verdande:latest
```

That is a permission error wearing the wrong hat. The Docker daemon **on the
Yggdrasil host** has to be logged in once:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u kristianwind --password-stdin
```

`GHCR_TOKEN` is a GitHub personal access token with the **`read:packages`** scope
and nothing else. The login is stored in `~/.docker/config.json` for whichever
user runs the daemon — if Yggdrasil runs as root or through systemd, log in as
that user, not as yourself.

!!! tip "Or make the package public"
    Under **Packages → verdande → Package settings → Change visibility**. The
    repository can stay private; only the built image becomes readable. Then no
    login is needed anywhere and this section stops applying.

## Installing

1. **Runes → Carve a rune (upload)** and pick
   [`rune/verdande.yaml`](https://github.com/kristianwind/verdande/blob/main/rune/verdande.yaml)
   from the repository.
2. Create a server from it.
3. Fill in the settings below.
4. Start it, open the URL, create the first account.

## Settings

| Setting | What to put |
|---|---|
| **Public address** | The address you reach it on. `{{PUBLIC_URL}}` is the default and usually right. |
| **SMTP host** | Your mail server. Leave blank and invite links are shown in the app instead of emailed. |
| **SMTP port** | 587 for STARTTLS, 465 for implicit TLS. |
| **SMTP username / password** | As your mail server expects. |
| **Sender address** | What invites and reminders come from. |
| **Use STARTTLS** | On, unless you are on port 465. |
| **How long a login lasts** | `720h` — thirty days. |

!!! note "Why the public address cannot be guessed"
    The host port is not chosen until the server is created, so verdande has no way
    to work out its own address. The panel does, and passes it in as
    `{{PUBLIC_URL}}`.

## Reaching it from outside

Give the server a **Subdomain** under its settings and enable
**Cloudflare Tunnel (subdomains)** under **Settings → Network**. No port forwarding
and no public IP.

## Backups

The Rune's backup includes the whole `/data` directory, which is what you want —
see [the warning about copying the database alone](self-hosting.md#what-lives-in-data).

## Watchers

The manifest ships log watchers for database errors, repeated failed logins and
HTTP 5xx. They appear under **Settings → Kvasir Watchers** as ordinary per-server
rules you can edit or turn off.
