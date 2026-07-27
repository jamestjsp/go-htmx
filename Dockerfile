FROM golang:1.26.3-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/processlab \
    ./cmd/processlab

FROM alpine:3.23

RUN addgroup -S processlab \
    && adduser -S -G processlab processlab \
    && mkdir -p /data \
    && chown processlab:processlab /data

COPY --from=build --chown=processlab:processlab /out/processlab /usr/local/bin/processlab

USER processlab

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/ || exit 1

ENTRYPOINT ["/usr/local/bin/processlab"]
CMD ["-addr", "0.0.0.0:8080", "-db", "/data/processlab.db"]
