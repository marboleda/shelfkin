# syntax=docker/dockerfile:1
#
# Shelfkin API image: the API server plus the migrate (#3) and expiry-job (#22)
# commands, as static binaries on distroless, running as a non-root user.
#
#   docker build -f api/Dockerfile api
#
# The default command is the API server. The Cloud Run migrate job runs
# another binary by overriding the arguments (there is no ENTRYPOINT, so the
# first argument is the program), e.g.
#
#   docker run --rm -e DATABASE_URL=... <image> /usr/local/bin/migrate status
#
# See "Deploying the API" in the README.

# Keep the Go version in sync with the go line in go.mod.
FROM golang:1.26.5-bookworm AS build
WORKDIR /src

COPY . .

# Static binaries (no cgo), with paths and debug info stripped. The cache
# mounts only speed up rebuilds on the same machine; they are not part of the
# image.
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/api . \
 && go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && go build -ldflags="-s -w" -o /out/expiry-job ./cmd/expiry-job

# distroless/static has CA certificates (for Firebase and Expo push) and tzdata
# (the expiry job uses users' time zones), and no shell. The nonroot tag runs
# as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/migrate /out/expiry-job /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT []
CMD ["/usr/local/bin/api"]
