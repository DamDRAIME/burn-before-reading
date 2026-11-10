# -- Build stage --
FROM golang:1.27-bookworm AS builder
WORKDIR /src

# Download dependencies first so this layer is cached until go.mod/go.sum change
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
# Static binary (no cgo); tzdata is embedded via the time/tzdata import in main.go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bbr .

# -- Runtime stage --
# distroless/static: CA certificates, no shell, no package manager; runs as uid 65532
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/bbr /bbr

ENV ADDR=:8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/bbr"]
