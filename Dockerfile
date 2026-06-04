# ---- build ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Pure-Go (modernc sqlite) — no CGO, fully static binary.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bot ./cmd/bot

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/bot /app/bot

# Runs as root so the bind-mounted host directory for the SQLite DB is always
# writable regardless of host uid/ownership.
WORKDIR /app
ENV BOT_DB_PATH=/data/bot.db

ENTRYPOINT ["/app/bot"]
