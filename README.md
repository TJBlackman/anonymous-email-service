# Anonymous Email Service

This repository contains the current runtime scaffold for a disposable email service. The app now starts both its HTTP and SMTP listeners and can run directly or in Docker, while mailbox persistence and message processing remain intentionally deferred.

## Requirements

- Windows 11 host
- WSL2 Ubuntu environment
- Go installed in WSL2

## Project Status

- `GET /health` returns `200 OK`
- `GET /` renders a placeholder inbox page
- Additional planned routes are registered and currently return `501 Not Implemented`
- SMTP listens on `SMTP_LISTEN_ADDR`, accepts protocol traffic for the configured domain, and returns a temporary failure when message delivery reaches the unimplemented handling path
- Repository and cleanup worker packages remain scaffolded and are not fully implemented

## Configuration

The server reads configuration from environment variables at startup. `DOMAIN` is required. All other values have defaults, and invalid values stop startup immediately instead of falling back silently.

- `DOMAIN`: required email domain for generated inbox addresses
- `SMTP_LISTEN_ADDR`: default `:25`
- `HTTP_LISTEN_ADDR`: default `:8080`
- `DATABASE_PATH`: default `./data/mail.db`
- `INBOX_TTL_HOURS`: default `24`
- `MAX_EMAIL_SIZE_MB`: default `10`
- `MAX_ATTACHMENT_MB`: default `5`
- `CLEANUP_INTERVAL_MIN`: default `15`
- `LOG_LEVEL`: default `info`

Validation notes:

- `DOMAIN` is trimmed, normalized to lowercase, and must not contain `@`, whitespace, leading dots, trailing dots, or consecutive dots
- `SMTP_LISTEN_ADDR` and `HTTP_LISTEN_ADDR` must be valid `host:port` values such as `:8080` or `127.0.0.1:2525`
- `INBOX_TTL_HOURS`, `MAX_EMAIL_SIZE_MB`, `MAX_ATTACHMENT_MB`, and `CLEANUP_INTERVAL_MIN` must be positive integers
- `MAX_ATTACHMENT_MB` cannot exceed `MAX_EMAIL_SIZE_MB`
- `LOG_LEVEL` must be one of `debug`, `info`, `warn`, or `error`

## Run Locally

Use WSL2 as required by `AGENTS.md`:

```bash
wsl -u trevor
cd /mnt/c/Users/Trevor/Desktop/anonymous-email-service
export DOMAIN=example.test
go run ./cmd/server
```

The app listens on:

- HTTP: `HTTP_LISTEN_ADDR` default `:8080`
- SMTP: `SMTP_LISTEN_ADDR` default `:25`

## Build And Test

```bash
wsl -u trevor
cd /mnt/c/Users/Trevor/Desktop/anonymous-email-service
export DOMAIN=example.test
go test ./...
go build ./cmd/server
```

## Run With Docker

```bash
docker build -t anonymous-email-service .
docker run --rm \
  -e DOMAIN=example.test \
  -p 25:25 \
  -p 8080:8080 \
  anonymous-email-service
```

## Current Limits

- The service does not persist inboxes or emails yet
- SMTP traffic is accepted at the protocol layer but message handling still returns a temporary failure
- The provided Docker assets expose the app ports only; external networking, TLS, DNS, and reverse proxying are expected to be handled outside the container
