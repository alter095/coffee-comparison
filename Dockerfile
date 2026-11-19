FROM node:22-bookworm-slim AS ui
WORKDIR /src/frontend
ENV NODE_OPTIONS=--max-old-space-size=384
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run check && npm run build

# Depend on the completed UI stage to avoid two compilers running in parallel.
FROM golang:1.27.3-bookworm AS api
WORKDIR /src
ENV CGO_ENABLED=1 GOTOOLCHAIN=local GOMAXPROCS=1 GOMEMLIMIT=192MiB
COPY --from=ui /src/frontend/dist /web
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -p 1 -trimpath -ldflags='-s -w' -o /coffee ./cmd/coffee

FROM debian:bookworm-slim
RUN mkdir -p /data /config /backups && chown 10001:10001 /data /config /backups && chmod 0700 /data /config /backups
COPY --from=api /coffee /usr/local/bin/coffee
COPY --from=api /web /app/web
USER 10001:10001
WORKDIR /app
ENV GOMAXPROCS=1 GOMEMLIMIT=96MiB
ENTRYPOINT ["/usr/local/bin/coffee"]
