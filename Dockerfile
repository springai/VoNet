# syntax=docker/dockerfile:1.7

# Build toolchains run natively on the BuildKit host. Without BUILDPLATFORM,
# the arm64 branch executes npm and the Go compiler through QEMU, which is much
# slower and makes npm ci appear to hang despite producing no progress output.
# ---- Stage 1: build the web frontend once on the native builder ----
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 2: cross-compile the Go binary on the native builder ----
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go-builder
RUN apk add --no-cache git
WORKDIR /src

ARG VERSION=0.1.0-dev
ARG BUILD_TIME=""
ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Overlay the freshly built frontend so go:embed web/dist picks it up.
COPY --from=web-builder /web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags "-s -w -X vonet/internal/buildinfo.Version=${VERSION} -X vonet/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/vonet \
    ./cmd/vonet

# ---- Stage 3: minimal runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates ccid iproute2 pcsc-lite qmi-utils tzdata && \
    addgroup -S -g 1000 vonet && \
    adduser -S -D -H -u 1000 -G vonet vonet

RUN mkdir -p /opt/vonet/bin /opt/vonet/data && \
    chown -R vonet:vonet /opt/vonet

COPY --from=go-builder /out/vonet /opt/vonet/bin/vonet
COPY scripts/docker-entrypoint.sh /usr/local/bin/vonet-entrypoint

# Symlink into /usr/local/bin so `docker exec <ctr> vonet ...` finds it via $PATH.
RUN ln -s /opt/vonet/bin/vonet /usr/local/bin/vonet && \
    chmod 0755 /usr/local/bin/vonet-entrypoint

# Hardware access and the bundled pcscd daemon require root inside the
# container. The container already needs host networking and privileged device
# access for modem, QMI, IPsec, and hot-plug support.
USER root
VOLUME ["/opt/vonet/data"]
EXPOSE 7575
ENV VONET_ADDR=0.0.0.0:7575 \
    VONET_DATABASE_PATH=/opt/vonet/data/vonet.db

ENTRYPOINT ["/usr/local/bin/vonet-entrypoint"]
