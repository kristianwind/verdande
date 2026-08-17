# Contributing

Thank you for looking. verdande is one person's self-hosted to-do app, so the bar
for a change is "does this make it better for people running it on their own
hardware" rather than "is it a feature some product has".

## Before you write code

Open an issue first for anything beyond a bug fix. The
[non-goals](README.md#non-goals) are deliberate, and it is kinder to say no to a
paragraph than to a weekend.

## Running it

```bash
VERDANDE_DATA_DIR=./data VERDANDE_DEV=true go run ./cmd/verdande
```

```bash
cd web && npm install && npm run dev
```

The API is on 8080, the interface on 5173 with `/api` proxied to it — so the
session cookie stays first-party exactly as it does in production.

## What CI checks

```bash
gofmt -l .             # must print nothing
go vet ./...
go test -race ./...
```

The Docker image is built on every pull request too, so a Dockerfile that no
longer works fails there rather than at release.

## What a change needs

**Tests for anything that could be silently wrong.** Not coverage for its own
sake — the parser, the permission checks and the import round trip are tested
because a quiet failure in any of them loses somebody's data or shows them
somebody else's.

**Comments that say why, not what.** The code says what it does. A comment earns
its place by explaining a decision that is not obvious from reading it: why the
connection pool is not 1, why recurrence is parsed before dates, why an uploaded
file is never served inline.

**Danish and English both.** Anything that reads or writes text a person typed
has to work in both. `ø` is not an accented `o`, and a feature that only works
for one of the two languages is a broken feature.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/): `feat(store):`,
`fix(web):`, `docs:`. Releases are semver tags, which build and publish the image.

## Security

Please do not open a public issue for a vulnerability. Email the address in
[SECURITY.md](SECURITY.md) instead.
