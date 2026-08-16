# verdande — one static binary, one /data volume, no external database.
#
# Three stages: build the PWA, compile the binary with the PWA embedded in it, and
# copy that single file into a distroless image. What ships has no shell, no package
# manager and no libc — the attack surface of the running container is the binary.

# --- 1. the web interface ----------------------------------------------------
FROM node:22-alpine AS web

WORKDIR /src
COPY web/ ./

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
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first: this layer is cached until go.mod or go.sum actually changes,
# which is far less often than the source does.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/build ./cmd/verdande/webbuild

ARG VERSION=dev

# CGO off is what makes this binary static, and it is only possible because the
# SQLite driver is pure Go. -trimpath and the empty buildid keep the output
# reproducible; -s -w drop the symbol and DWARF tables, which is most of the size.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -tags embedweb \
        -trimpath \
        -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
        -o /verdande ./cmd/verdande

# --- 3. what actually ships --------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# SQLite writes timestamps and verdande resolves due dates in the user's timezone,
# so the container needs a tz database and CA certificates for outbound SMTP.
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip

COPY --from=build /verdande /verdande

# Everything that survives a redeploy lives here: the database, uploaded files and
# nightly backups.
VOLUME ["/data"]
ENV VERDANDE_DATA_DIR=/data \
    VERDANDE_ADDR=:8080

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/verdande"]
