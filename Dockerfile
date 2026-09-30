# syntax=docker/dockerfile:1

# ---- web build stage ---------------------------------------------------------
FROM node:22-alpine AS web

WORKDIR /src/web

# Cache dependencies separately from source.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
# Writes the production build into /src/internal/webui/dist.
RUN npm run build

# ---- go build stage ----------------------------------------------------------
FROM golang:1.26 AS build

WORKDIR /src

# Cache dependencies separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Use the freshly built frontend instead of the committed embed output.
COPY --from=web /src/internal/webui/dist ./internal/webui/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/mcp-diary ./cmd/mcp-diary

# ---- runtime stage -----------------------------------------------------------
FROM alpine:3.20

RUN adduser -D -u 10001 app \
    && mkdir -p /data \
    && chown -R app:app /data

COPY --from=build /out/mcp-diary /usr/local/bin/mcp-diary

USER app

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/mcp-diary"]
CMD ["serve", "--root", "/data", "--addr", ":8080"]
