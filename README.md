# Anonymous Email Service

This repository contains the initial Go scaffold for a disposable email service. The current state is intentionally thin: it provides a runnable HTTP server, placeholder route wiring, the planned package layout, and the first database migration, while SMTP and SQLite behavior remain stubbed for later implementation.

## Requirements

- Windows 11 host
- WSL2 Ubuntu environment
- Go installed in WSL2

## Project Status

- `GET /health` returns `200 OK`
- `GET /` renders a placeholder inbox page
- Additional planned routes are registered and currently return `501 Not Implemented`
- Repository, SMTP, and cleanup worker packages are scaffolded but not fully implemented

## Configuration

The server supports these environment variables:

- `DOMAIN` default: `example.test`
- `SMTP_LISTEN_ADDR` default: `:25`
- `HTTP_LISTEN_ADDR` default: `:8080`
- `DATABASE_PATH` default: `./data/mail.db`
- `INBOX_TTL_HOURS` default: `24`
- `MAX_EMAIL_SIZE_MB` default: `10`
- `MAX_ATTACHMENT_MB` default: `5`
- `CLEANUP_INTERVAL_MIN` default: `15`
- `LOG_LEVEL` default: `info`

## Run Locally

Use WSL2 as required by `AGENTS.md`:

```bash
wsl -u trevor
cd /mnt/c/Users/Trevor/Desktop/anonymous-email-service
go run ./cmd/server
```

## Build And Test

```bash
wsl -u trevor
cd /mnt/c/Users/Trevor/Desktop/anonymous-email-service
go test ./...
go build ./cmd/server
```
