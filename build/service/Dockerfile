FROM golang:1.26-alpine AS build

WORKDIR /app/src

RUN apk add --no-cache git

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOCACHE=/root/.cache/go-build \
    GOMODCACHE=/root/go/pkg/mod

# Leverage build cache by copying go.mod and go.sum first
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download && go mod verify

COPY . .

ARG VERSION=dev
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-s -w -X main.version=${VERSION}" -o /app/service ./cmd/service

# Debug stage using Delve
FROM build AS debug

RUN go install github.com/go-delve/delve/cmd/dlv@latest
RUN go build -gcflags="all=-N -l" -o /app/service-debug ./cmd/service

CMD ["/root/go/bin/dlv", "--listen=:40000", "--headless=true", "--api-version=2", "--accept-multiclient", "exec", "/app/service-debug"]

FROM alpine:3.22 AS app

# curl for health checks, tzdata for chart bucket timezones
RUN apk --update --no-cache add curl tzdata

ARG user=app
ARG group=app
RUN addgroup -S $group && adduser -S -G $group -H -D $user

COPY --chown=$user:$group --from=build /app/service /usr/local/bin/service

USER $user
WORKDIR /usr/local/service

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 CMD curl --fail http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/service"]
