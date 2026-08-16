# web

The SvelteKit PWA. Not written yet — this directory holds its place so the Docker
build has something to copy, and so the frontend stage can tell "not built yet"
apart from "the build failed".

The Dockerfile builds this directory when it finds a `package.json` here, and
otherwise emits a placeholder page. So adding the real app is exactly: put a
SvelteKit project in this directory whose `npm run build` produces `build/`.

## What it needs to be

- **SvelteKit**, `adapter-static`, output in `build/`. The Go binary embeds that
  directory at compile time and serves it, falling back to `index.html` so
  client-side routes survive a reload.
- **A PWA**: installable, offline reading of already-cached lists, swipe gestures
  for complete and schedule.
- **Nordic minimalism.** Dark by default, light available. One accent colour,
  everything else neutral. Runic mark for Verdande.
- **Optimistic on every local action.** No spinner ever appears for something the
  browser could already know the answer to.

## Talking to the backend

Same origin — the Go binary serves both. `/api/v1` for REST, `/api/v1/ws` for the
live sync. Session cookie is `httpOnly`, so the frontend never handles the token
itself.

During development, run the backend on 8080 and point the Vite dev server's proxy
at it:

```bash
VERDANDE_DATA_DIR=./data VERDANDE_DEV=true go run ./cmd/verdande
```
