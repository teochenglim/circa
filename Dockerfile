# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0: no cgo dependency anywhere in the storage/collector stack,
# so the binary stays fully static - no libc needed at runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/circa ./cmd/circa
# distroless has no shell/mkdir; pre-create the data dir here so it can be
# COPY --chown'd into the runtime image owned by the nonroot user (65532).
# A named volume mounted over it inherits this ownership on first creation.
RUN mkdir -p /out/data

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/circa /app/circa
COPY --from=build --chown=65532:65532 /out/data /data

USER nonroot:nonroot
EXPOSE 9100
VOLUME ["/data"]
ENV CIRCA_LISTEN_ADDRESS=:9100 \
    CIRCA_STORAGE_PATH=/data

ENTRYPOINT ["/app/circa"]
CMD ["-config", "/etc/circa/config.yaml"]
