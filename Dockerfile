# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

FROM golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728 AS build

ENV GOTOOLCHAIN=local

WORKDIR /src

COPY go.mod go.sum ./
RUN expected="$(awk '$1 == "go" { print "go" $2; exit }' go.mod)" && \
    actual="$(go env GOVERSION)" && \
    if [ -z "$expected" ]; then \
        echo "go.mod is missing the required go directive" >&2; \
        exit 1; \
    fi && \
    if [ "$actual" != "$expected" ]; then \
        echo "Docker Go version mismatch: expected $expected from go.mod, got $actual" >&2; \
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

FROM debian:13-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad AS runtime

ENV HTTP_ADDR=:8080 \
    WORKER_ADMIN_ADDR=:8081

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/worker /usr/local/bin/worker

USER 10001:10001

EXPOSE 8080 8081

CMD ["/usr/local/bin/api"]
