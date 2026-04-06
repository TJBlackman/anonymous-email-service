# Anonymous Email Service

This repository contains a disposable email service implemented in Go. The app persists inboxes and received messages in SQLite, accepts inbound SMTP traffic for generated inboxes, renders inbox and message views over HTTP, and includes the stage 6 hardening pass for cookies, request validation, security headers, and rate limiting.

## Requirements

- Windows 11 host
- WSL2 Ubuntu environment
- Go installed in WSL2

## Project Status

- `GET /` creates or resumes a disposable inbox and renders stored email
- `GET /email/{id}` renders a single stored email and marks it read
- `GET /email/{id}/attachment/{aid}` serves owned attachments as downloads
- `POST /email/{id}/delete` deletes an owned email
- `POST /inbox/new` rotates to a fresh inbox address
- `GET /api/emails` returns the current inbox contents as JSON
- `GET /health` checks repository reachability and returns `200 OK` or `503 Service Unavailable`
- SMTP accepts mail for existing generated inboxes on the configured domain and stores parsed bodies and attachments
- Expired inboxes are deleted by the background cleanup worker
- HTTP responses include security headers, POST routes enforce same-origin checks and request-size limits, and inbox creation / SMTP sessions are rate limited in-process

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
- `COOKIE_SECURE`: default `false`
- `INBOX_CREATE_LIMIT_PER_HOUR`: default `10`
- `SMTP_CONNECTION_LIMIT_PER_MIN`: default `30`
- `LOG_LEVEL`: default `info`

Validation notes:

- `DOMAIN` is trimmed, normalized to lowercase, and must not contain `@`, whitespace, leading dots, trailing dots, or consecutive dots
- `SMTP_LISTEN_ADDR` and `HTTP_LISTEN_ADDR` must be valid `host:port` values such as `:8080` or `127.0.0.1:2525`
- `INBOX_TTL_HOURS`, `MAX_EMAIL_SIZE_MB`, `MAX_ATTACHMENT_MB`, and `CLEANUP_INTERVAL_MIN` must be positive integers
- `MAX_ATTACHMENT_MB` cannot exceed `MAX_EMAIL_SIZE_MB`
- `COOKIE_SECURE` must be `true` or `false`
- `INBOX_CREATE_LIMIT_PER_HOUR` and `SMTP_CONNECTION_LIMIT_PER_MIN` must be positive integers
- `LOG_LEVEL` must be one of `debug`, `info`, `warn`, or `error`

Cookie behavior:

- Default cookie name is `inbox_token` with `HttpOnly` and `SameSite=Lax`
- When `COOKIE_SECURE=true`, the service sets `__Host-inbox_token` and requires secure transport semantics (`Secure`, `Path=/`, no `Domain` attribute)

HTTP hardening:

- All responses set `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, and a restrictive `Content-Security-Policy`
- POST routes require a same-origin `Origin` header, or a same-origin `Referer` if `Origin` is absent
- POST request bodies are limited to `64 KiB`

Rate limiting:

- Inbox creation is limited per client IP using the remote socket address only
- SMTP session creation is limited per client IP using the remote socket address only
- Trusted proxy headers such as `X-Forwarded-For` are intentionally ignored in this stage

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

- Rate limiting is in-memory and scoped to a single process
- Inbox ownership is session-cookie based; there is no account system
- HTML email bodies are sanitized before rendering and may lose unsupported markup
- The provided Docker assets expose the app ports only; external networking, TLS, DNS, and reverse proxying are expected to be handled outside the container
