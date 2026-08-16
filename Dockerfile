# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89

FROM golang:1.26.6-trixie@sha256:b75d466dd608587fd66cca705a307ba65b889827d06ad61d6a75f0482b51b7c7 AS build

ENV GOTOOLCHAIN=local

WORKDIR /src

COPY go.mod go.sum ./
RUN expected="$(sed -n 's/^toolchain //p' go.mod)" && \
    actual="$(go env GOVERSION)" && \
    if [ -z "$expected" ]; then \
        echo "go.mod is missing the required toolchain directive" >&2; \
        exit 1; \
    fi && \
    if [ "$actual" != "$expected" ]; then \
        echo "Go toolchain mismatch: go.mod requires $expected, Docker builder provides $actual" >&2; \
        exit 1; \
    fi && \
    go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
    -mod=readonly \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api \
    ./cmd/api && \
    CGO_ENABLED=0 go build \
    -mod=readonly \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/worker \
    ./cmd/worker

FROM debian:13-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258 AS runtime

ENV HTTP_ADDR=:8080 \
    WORKER_ADMIN_ADDR=:8081

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system app && \
    useradd --system --gid app --home-dir /nonexistent --shell /usr/sbin/nologin app

COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/worker /usr/local/bin/worker

USER app

EXPOSE 8080 8081

CMD ["/usr/local/bin/api"]
