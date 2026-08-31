# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

FROM golang:1.27.0-trixie@sha256:df98008ecd2b0ecc9f0a94d1b07e3564a9c92b555369b33d9b5f60d0765b2db7 AS build

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

FROM debian:13-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132 AS runtime

ENV HTTP_ADDR=:8080 \
    WORKER_ADMIN_ADDR=:8081

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/worker /usr/local/bin/worker

USER 10001:10001

EXPOSE 8080 8081

CMD ["/usr/local/bin/api"]
