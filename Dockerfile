# syntax=docker/dockerfile:1

# Build stage: compile a fully static binary (modernc.org/sqlite is pure Go,
# so CGO_ENABLED=0 works and no compiler toolchain ships in the image).
FROM golang:1.27-alpine AS build

WORKDIR /src

# Cache module downloads before copying the source so small edits rebuild fast.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/jat ./cmd/server

# Final stage: distroless static, nonroot user, no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
USER 65532:65532

ENV DATA_DIR=/data \
    PORT=8080 \
    TZ=Africa/Harare \
    BASE_URL=http://localhost:8080

COPY --from=build /out/jat /app/app

# The single backup unit: SQLite DB, uploaded files, and goose DB version table.
VOLUME /data

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD ["/app/app", "healthcheck"]

# `jat serve` by default; `docker run --rm jat healthcheck` works too.
ENTRYPOINT ["/app/app"]
CMD ["serve"]
