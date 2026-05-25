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
- `GET /admin` lists configured domains and lets an operator add, enable, or disable them
- SMTP accepts mail for existing generated inboxes on any enabled domain and stores parsed bodies and attachments
- Expired inboxes are deleted by the background cleanup worker
- HTTP responses include security headers, POST routes enforce same-origin checks and request-size limits, and inbox creation / SMTP sessions are rate limited in-process

## Configuration

The server reads configuration from environment variables at startup. All values have defaults except where noted, and invalid values stop startup immediately instead of falling back silently.

- `DOMAIN`: optional bootstrap seed domain. When set and the domains table is empty, it is inserted as the first enabled domain on startup. Domains are otherwise managed at runtime via the `/admin` UI (see Multiple Domains below).
- `SMTP_HOSTNAME`: hostname advertised in the SMTP EHLO banner. Defaults to `DOMAIN` if set, otherwise `localhost`. This is the greeting string only and is not used to validate recipients.
- `SMTP_LISTEN_ADDR`: default `:25`
- `HTTP_LISTEN_ADDR`: default `:8080`
- `DATABASE_PATH`: default `./data/mail.db`
- `INBOX_TTL_DAYS`: default `60`. Set to `0` to disable expiry — inboxes are never auto-cleaned.
- `MAX_EMAIL_SIZE_MB`: default `10`
- `MAX_ATTACHMENT_MB`: default `5`
- `CLEANUP_INTERVAL_MIN`: default `15` — how often the background worker purges expired inboxes
- `COOKIE_SECURE`: default `false`
- `INBOX_CREATE_LIMIT_PER_HOUR`: default `2`. Set to `0` to disable the limit (unlimited).
- `SMTP_CONNECTION_LIMIT_PER_MIN`: default `30`
- `LOG_LEVEL`: default `info`

Validation notes:

- When set, `DOMAIN` and `SMTP_HOSTNAME` are trimmed and normalized to lowercase; `DOMAIN` must not contain `@`, whitespace, leading dots, trailing dots, or consecutive dots
- `SMTP_LISTEN_ADDR` and `HTTP_LISTEN_ADDR` must be valid `host:port` values such as `:8080` or `127.0.0.1:2525`
- `MAX_EMAIL_SIZE_MB`, `MAX_ATTACHMENT_MB`, and `CLEANUP_INTERVAL_MIN` must be positive integers
- `MAX_ATTACHMENT_MB` cannot exceed `MAX_EMAIL_SIZE_MB`
- `COOKIE_SECURE` must be `true` or `false`
- `SMTP_CONNECTION_LIMIT_PER_MIN` must be a positive integer
- `INBOX_TTL_DAYS` and `INBOX_CREATE_LIMIT_PER_HOUR` must be non-negative integers (`0` disables expiry / the rate limit respectively)
- `LOG_LEVEL` must be one of `debug`, `info`, `warn`, or `error`

## Multiple Domains

The service supports several sending domains at once.

- Domains are stored in the database and managed through the `/admin` page: add a domain, and enable or disable it.
- When creating an inbox, users choose a domain from a dropdown on the home page. The auto-created inbox on first visit uses the default (oldest enabled) domain.
- Disabling a domain removes it from the dropdown and stops new inboxes from being created on it, but existing inboxes on that domain keep receiving mail until they expire.
- Inbound SMTP accepts mail for any currently enabled domain. The recipient domain is validated per message against the database (with a short in-process cache), so newly added domains start accepting mail within ~30 seconds without a restart.

> **⚠️ Launch blocker: the `/admin` page has NO authentication.** Anyone who can reach the HTTP port can add or disable domains. Do not expose this service publicly until authentication is added to the single `adminGuard` chokepoint in `internal/web/admin.go`.

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

- Rate limiting and the SMTP domain cache are in-memory and scoped to a single process
- The `/admin` domain management UI is currently unauthenticated (see Multiple Domains)
- Inbox ownership is session-cookie based; there is no account system
- HTML email bodies are sanitized before rendering and may lose unsupported markup
- The provided Docker assets expose the app ports only; external networking, TLS, DNS, and reverse proxying are expected to be handled outside the container

## Security Considerations

- Database access uses parameterized queries rather than string-built SQL
- HTML email content is sanitized before rendering to reduce XSS risk
- SMTP accepts mail only for known generated inboxes on the configured domain, which avoids acting as an open relay
- Request bodies, inbound message size, attachment size, and inbox creation / SMTP session rates are all bounded in-process to reduce denial-of-service risk
- Inbox ownership is token-based and enforced with `HttpOnly` cookies, `SameSite=Lax`, and optional secure `__Host-` cookie semantics when `COOKIE_SECURE=true`
- Email and attachment access is ID-based and checked against the current inbox, which prevents path-style traversal and cross-inbox object access
- Message views, attachment downloads, and delete actions all enforce inbox-scoped ownership checks to reduce information disclosure between inboxes

## Future Enhancements

These items are not implemented today and remain out of scope for the current service:

- Authentication for the `/admin` domain management UI (currently unauthenticated — see Multiple Domains)
- Custom inbox addresses
- Email forwarding
- API key authentication for programmatic access
- Webhook notifications for new email
- SMTP STARTTLS / broader TLS support
- Service metrics
