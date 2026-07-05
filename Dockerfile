# syntax=docker/dockerfile:1

FROM golang:1.26.4-trixie AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api \
    ./cmd/api

FROM debian:13-slim AS runtime

RUN groupadd --system app && useradd --system --gid app --home-dir /nonexistent --shell /usr/sbin/nologin app

COPY --from=build /out/api /usr/local/bin/api

USER app

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
