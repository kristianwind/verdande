# Installing as a Rune

verdande ships with a Rune manifest for
[Yggdrasil Panel](https://github.com/kristianwind/yggdrasil).

## If the image will not pull

```
create container: Error response from daemon:
No such image: gitea.nolimit.dk/kw/verdande:latest
```

This message is a liar. It does not mean the tag is missing from the registry —
it means the image is not on the host, and the panel's attempt to fetch it failed
without saying so.

The usual cause is a **private** package. Yggdrasil pulls anonymously: its
`PullImage` calls the Docker SDK with no `RegistryAuth`, so a private image comes
back 401. The pull error is discarded, and `ContainerCreate` — which only ever
looks locally — then reports the image as missing.

!!! warning "`docker login` on the host does not help"
    `docker login` is client-side. The daemon stores no credentials; the CLI
    sends them itself with each request. Yggdrasil is a different client and
    sends none, so logging in changes nothing for the panel.

**The package has to be readable without credentials**, and the only way to know
is to ask the registry for a token as a stranger:

```bash
curl -s "https://ghcr.io/token?scope=repository:kristianwind/verdande:pull&service=ghcr.io"
```

A 200 with a `token` in it means anyone can pull, which means the panel can. A
401 means it cannot, whatever the package page says.

!!! danger "Do not test this with `docker --config /tmp/empty pull`"
    It looks like the honest test and is not. An empty config directory does not
    stop the machine's credential helper, so the pull is quietly authenticated
    with credentials you forgot you had — and reports success for an image no
    stranger can fetch. This cost a production instance an hour of downtime: the
    image was declared public on that evidence, the rune was pointed at it, and
    the host — which had never logged in — could not pull a thing.

    A self-hosted Gitea is where this bites. An instance with `REQUIRE_SIGNIN_VIEW`
    turned on refuses anonymous tokens for its registry too, and no per-package
    setting overrides it. Making the source repository public does not change it
    either; that was tried.

Once it is public, press **Start** on the server you already have. There is no
need to delete it and make another: the image reference is read from the rune at
container-create time, and both Start and Restart recreate the container.

To keep the package private you have to put the image on the host yourself,
before creating the server, and again after every release:

```bash
sudo docker login gitea.nolimit.dk -u kw
sudo docker pull gitea.nolimit.dk/kw/verdande:latest
```

Run it as the user the Docker daemon runs as — `sudo` if the panel does. The
panel's own pull still fails silently; create simply finds the image already
there.

!!! danger "This hides updates"
    The panel re-pulls on every recreate, fails quietly, and carries on with the
    local image. A new release then *looks* like it rolled out and did not. For
    something you intend to update, public is the right answer and host-pull is
    the stopgap.

## Installing

1. **Runes → Carve a rune (upload)** and pick
   [`rune/verdande.yaml`](https://github.com/kristianwind/verdande/blob/main/rune/verdande.yaml)
   from the repository.
2. Create a server from it.
3. Fill in the settings below.
4. Start it, open the URL, create the first account.

!!! note "Updating the rune is a manual step"
    A rune does not update itself when this manifest changes. The panel writes
    the rune row from a UI action only — neither a restart nor a timer does it —
    so pick up a new manifest with **Runes → verdande → Update**, or install it
    again from Browse GitHub.

    The catalogue list is cached in memory for ten minutes, so two attempts in a
    row can both hand you the old version. Recent panel versions show the list's
    age with a *fetch again* link.

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
