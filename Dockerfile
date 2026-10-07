# verdande — one static binary, one /data volume, no external database.
#
# Three stages: build the PWA, compile the binary with the PWA embedded in it, and
# copy that single file into a distroless image. What ships has no shell, no package
# manager and no libc — the attack surface of the running container is the binary.

# --- 1. the web interface ----------------------------------------------------
#
# Pinned to the *build* platform, not the target. What this stage produces is
# HTML, CSS and JavaScript, which are the same bytes whatever the image will run
# on — so building it once and copying it into both architectures is not an
# optimisation, it is the correct thing.
#
# Without the pin, buildx runs this stage once per target, which means running
# Node and esbuild under QEMU for arm64. That crashes: exit 132, SIGILL, an
# instruction the emulator does not implement, roughly one release in three. It
# looks like a broken build and is a broken emulator.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web

WORKDIR /src
COPY web/ ./

# Passed into the frontend build, because SvelteKit's default build version is
# `Date.now()` — a millisecond that landed in version.json, in index.html and in
# the service worker, changed the content hash of every chunk referencing them, and
# so changed those chunks' filenames. Every build of one commit produced different
# bytes. See the long note in web/svelte.config.js.
ARG VERSION=dev
ENV VERDANDE_VERSION=${VERSION}

# The frontend is built when it is present. During backend-only development it is
# not, and the build must still produce a working image rather than failing on a
# missing directory — so this stage falls back to a placeholder page. The Go stage
# embeds whatever lands in /src/build either way.
RUN if [ -f package.json ]; then \
        npm ci --no-audit --no-fund && npm run build; \
    else \
        mkdir -p build && \
        printf '%s\n' '<!doctype html><meta charset="utf-8"><title>verdande</title>' \
            '<p>verdande is running. The web interface is not part of this build.</p>' \
            > build/index.html; \
    fi

# --- 2. the binary -----------------------------------------------------------
#
# Also pinned to the build platform, and cross-compiled instead. Go does that
# natively and CGO is already off, so there is nothing to emulate — where
# building *in* the target architecture means running the whole Go toolchain
# under QEMU for one of the two.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first: this layer is cached until go.mod or go.sum actually changes,
# which is far less often than the source does.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/build ./cmd/verdande/webbuild

ARG VERSION=dev
# Supplied by buildx per target. The default keeps a plain `docker build` working.
ARG TARGETARCH=amd64

# CGO off is what makes this binary static, and it is only possible because the
# SQLite driver is pure Go. -trimpath and the empty buildid keep the output
# reproducible; -s -w drop the symbol and DWARF tables, which is most of the size.
#
# "Reproducible" was false for a year and nobody read the sentence that said it:
# the flags above do their job, and the frontend embedded below carried a
# millisecond timestamp, so every build of one commit produced a different binary.
# Fixed in web/svelte.config.js, and CI now asserts that the two images' layers
# match rather than trusting this comment.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
        -tags embedweb \
        -trimpath \
        -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
        -o /verdande ./cmd/verdande

# --- 3. what actually ships --------------------------------------------------
#
# Two images come out of this stage, and the only difference between them is one
# environment variable:
#
#     docker build .                          → verdande
#     docker build --build-arg EDITION=notes  → urd
#
# Stages 1 and 2 are byte-identical for both, so the binary in the two images is
# the same build rather than a rebuild that happens to match. That is the point:
# urd is not a fork, and "built from the same source" is a property of the layers
# instead of a sentence in a README. ENV is image *config* rather than a layer, so
# the two images share every layer digest and differ only in that config — which CI
# asserts on every pull request.
FROM gcr.io/distroless/static-debian12:nonroot

# Declared here as well as used here: an ARG is scoped to the stage it appears in,
# so one above the FROM above would be invisible down here. `full` by default,
# because that is what this program has always been and an unqualified build must
# not quietly produce the other product.
ARG EDITION=full

# SQLite writes timestamps and verdande resolves due dates in the user's timezone,
# so the container needs a tz database and CA certificates for outbound SMTP.
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip

COPY --from=build /verdande /verdande

# Everything that survives a redeploy lives here: the database, uploaded files and
# nightly backups.
VOLUME ["/data"]
ENV VERDANDE_DATA_DIR=/data \
    VERDANDE_ADDR=:8080 \
    VERDANDE_EDITION=${EDITION}

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/verdande"]
