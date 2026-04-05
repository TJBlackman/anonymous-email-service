# Disposable Email Service Development Plan

## Stage 1. Technical Stack

| Component        | Technology                       | Version/Notes                     |
| ---------------- | -------------------------------- | --------------------------------- |
| Language         | Go                               | 1.22+                             |
| Database         | SQLite                           | `modernc.org/sqlite` (CGO-free)   |
| HTTP Router      | Standard Library                 | `net/http`                        |
| Templating       | Standard Library                 | `html/template`                   |
| SMTP Server      | `github.com/emersion/go-smtp`    | Latest                            |
| Email Parser     | `github.com/emersion/go-message` | For MIME parsing                  |
| UUID Generation  | `github.com/google/uuid`         | For tokens and address generation |
| Containerization | Docker                           | Alpine Linux base                 |

## Stage 2. Infrastructure Requirements

### DNS Configuration

- **MX Record**: `@ IN MX 10 mail.yourdomain.com`
- **A Record**: `mail IN A <server-ip>`
- **SPF Record** (optional, for replies): `@ IN TXT "v=spf1 a mx -all"`

### Network Requirements

- Port 25 (SMTP) - inbound, may require ISP unblocking or relay
- Port 80/443 (HTTP/HTTPS) - inbound
- Firewall rules permitting these ports

## Stage 3. Data Models

### Database Schema

```sql
-- Enable foreign keys and WAL mode for performance
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS inboxes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT UNIQUE NOT NULL COLLATE NOCASE,
    local_part TEXT NOT NULL COLLATE NOCASE,
    token TEXT UNIQUE NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_accessed_at INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at INTEGER NOT NULL
);

CREATE INDEX idx_inboxes_token ON inboxes(token);
CREATE INDEX idx_inboxes_address ON inboxes(address);
CREATE INDEX idx_inboxes_expires_at ON inboxes(expires_at);

CREATE TABLE IF NOT EXISTS emails (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id INTEGER NOT NULL,
    message_id TEXT,
    sender TEXT NOT NULL,
    sender_name TEXT,
    recipient TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '(no subject)',
    body_html TEXT,
    body_text TEXT,
    raw_headers TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    received_at INTEGER NOT NULL DEFAULT (unixepoch()),
    is_read INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(inbox_id) REFERENCES inboxes(id) ON DELETE CASCADE
);

CREATE INDEX idx_emails_inbox_id ON emails(inbox_id);
CREATE INDEX idx_emails_received_at ON emails(received_at);

CREATE TABLE IF NOT EXISTS attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email_id INTEGER NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    content BLOB NOT NULL,
    FOREIGN KEY(email_id) REFERENCES emails(id) ON DELETE CASCADE
);

CREATE INDEX idx_attachments_email_id ON attachments(email_id);
```

### Go Structs

```go
package models

import "time"

type Inbox struct {
    ID             int64
    Address        string
    LocalPart      string
    Token          string
    CreatedAt      time.Time
    LastAccessedAt time.Time
    ExpiresAt      time.Time
}

type Email struct {
    ID             int64
    InboxID        int64
    MessageID      string
    Sender         string
    SenderName     string
    Recipient      string
    Subject        string
    BodyHTML       string
    BodyText       string
    RawHeaders     string
    SizeBytes      int64
    HasAttachments bool
    ReceivedAt     time.Time
    IsRead         bool
}

type Attachment struct {
    ID          int64
    EmailID     int64
    Filename    string
    ContentType string
    SizeBytes   int64
    Content     []byte
}
```

## Stage 4. Configuration

### Environment Variables

| Variable               | Required | Default          | Description                           |
| ---------------------- | -------- | ---------------- | ------------------------------------- |
| `DOMAIN`               | Yes      | -                | Email domain (e.g., `tempmail.dev`)   |
| `SMTP_LISTEN_ADDR`     | No       | `:25`            | SMTP server bind address              |
| `HTTP_LISTEN_ADDR`     | No       | `:8080`          | HTTP server bind address              |
| `DATABASE_PATH`        | No       | `./data/mail.db` | SQLite database file path             |
| `INBOX_TTL_HOURS`      | No       | `24`             | Inbox expiration time in hours        |
| `MAX_EMAIL_SIZE_MB`    | No       | `10`             | Maximum email size in megabytes       |
| `MAX_ATTACHMENT_MB`    | No       | `5`              | Maximum single attachment size        |
| `CLEANUP_INTERVAL_MIN` | No       | `15`             | Cleanup job interval in minutes       |
| `LOG_LEVEL`            | No       | `info`           | Logging level (debug/info/warn/error) |

### Config Struct

```go
package config

import (
    "os"
    "strconv"
    "time"
)

type Config struct {
    Domain            string
    SMTPListenAddr    string
    HTTPListenAddr    string
    DatabasePath      string
    InboxTTL          time.Duration
    MaxEmailSize      int64
    MaxAttachmentSize int64
    CleanupInterval   time.Duration
    LogLevel          string
}

func Load() (*Config, error) {
    cfg := &Config{
        Domain:            os.Getenv("DOMAIN"),
        SMTPListenAddr:    getEnvOrDefault("SMTP_LISTEN_ADDR", ":25"),
        HTTPListenAddr:    getEnvOrDefault("HTTP_LISTEN_ADDR", ":8080"),
        DatabasePath:      getEnvOrDefault("DATABASE_PATH", "./data/mail.db"),
        InboxTTL:          time.Duration(getEnvIntOrDefault("INBOX_TTL_HOURS", 24)) * time.Hour,
        MaxEmailSize:      int64(getEnvIntOrDefault("MAX_EMAIL_SIZE_MB", 10)) * 1024 * 1024,
        MaxAttachmentSize: int64(getEnvIntOrDefault("MAX_ATTACHMENT_MB", 5)) * 1024 * 1024,
        CleanupInterval:   time.Duration(getEnvIntOrDefault("CLEANUP_INTERVAL_MIN", 15)) * time.Minute,
        LogLevel:          getEnvOrDefault("LOG_LEVEL", "info"),
    }
    if cfg.Domain == "" {
        return nil, fmt.Errorf("DOMAIN environment variable is required")
    }
    return cfg, nil
}
```

## Stage 5. System Components

### 5.1 Repository Layer

```go
package repository

type Repository interface {
    // Inbox operations
    CreateInbox(ctx context.Context, inbox *models.Inbox) error
    GetInboxByToken(ctx context.Context, token string) (*models.Inbox, error)
    GetInboxByAddress(ctx context.Context, address string) (*models.Inbox, error)
    UpdateLastAccessed(ctx context.Context, inboxID int64) error
    DeleteExpiredInboxes(ctx context.Context) (int64, error)

    // Email operations
    SaveEmail(ctx context.Context, email *models.Email) error
    GetEmailsByInboxID(ctx context.Context, inboxID int64, limit, offset int) ([]*models.Email, error)
    GetEmailByID(ctx context.Context, inboxID, emailID int64) (*models.Email, error)
    MarkEmailAsRead(ctx context.Context, emailID int64) error
    GetUnreadCount(ctx context.Context, inboxID int64) (int, error)
    DeleteEmail(ctx context.Context, inboxID, emailID int64) error

    // Attachment operations
    SaveAttachment(ctx context.Context, attachment *models.Attachment) error
    GetAttachmentsByEmailID(ctx context.Context, emailID int64) ([]*models.Attachment, error)
    GetAttachment(ctx context.Context, attachmentID int64) (*models.Attachment, error)

    // Maintenance
    Close() error
}
```

### 5.2 SMTP Server

#### Backend Interface Implementation

```go
package smtp

type Backend struct {
    repo   repository.Repository
    domain string
    config *config.Config
    logger *slog.Logger
}

func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
    return &Session{
        backend: b,
        conn:    c,
    }, nil
}
```

#### Session Interface Implementation

```go
type Session struct {
    backend *Backend
    conn    *smtp.Conn
    from    string
    rcpts   []string
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
    s.from = from
    return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
    // Validate recipient is for our domain
    addr, err := mail.ParseAddress(to)
    if err != nil {
        return &smtp.SMTPError{Code: 550, Message: "Invalid address"}
    }

    parts := strings.Split(addr.Address, "@")
    if len(parts) != 2 || !strings.EqualFold(parts[1], s.backend.domain) {
        return &smtp.SMTPError{Code: 550, Message: "User not found"}
    }

    // Check inbox exists
    _, err = s.backend.repo.GetInboxByAddress(context.Background(), addr.Address)
    if err != nil {
        return &smtp.SMTPError{Code: 550, Message: "User not found"}
    }

    s.rcpts = append(s.rcpts, addr.Address)
    return nil
}

func (s *Session) Data(r io.Reader) error {
    // Limit reader to max email size
    lr := io.LimitReader(r, s.backend.config.MaxEmailSize)

    // Parse email using go-message
    msg, err := mail.CreateReader(lr)
    if err != nil {
        return &smtp.SMTPError{Code: 554, Message: "Failed to parse message"}
    }

    // Extract headers, body, attachments
    // Store in database for each recipient
    return s.processMessage(msg)
}

func (s *Session) Reset() {
    s.from = ""
    s.rcpts = nil
}

func (s *Session) Logout() error {
    return nil
}
```

### 5.3 HTTP Server

#### Middleware

```go
package web

type contextKey string

const inboxContextKey contextKey = "inbox"

func SessionMiddleware(repo repository.Repository, domain string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            var inbox *models.Inbox

            cookie, err := r.Cookie("inbox_token")
            if err == nil && cookie.Value != "" {
                inbox, _ = repo.GetInboxByToken(r.Context(), cookie.Value)
            }

            if inbox == nil {
                // Create new inbox
                token := uuid.NewString()
                localPart := generateLocalPart() // e.g., "abc123"
                address := fmt.Sprintf("%s@%s", localPart, domain)
                expiresAt := time.Now().Add(24 * time.Hour)

                inbox = &models.Inbox{
                    Address:   address,
                    LocalPart: localPart,
                    Token:     token,
                    ExpiresAt: expiresAt,
                }
                if err := repo.CreateInbox(r.Context(), inbox); err != nil {
                    http.Error(w, "Internal error", http.StatusInternalServerError)
                    return
                }

                http.SetCookie(w, &http.Cookie{
                    Name:     "inbox_token",
                    Value:    token,
                    Path:     "/",
                    HttpOnly: true,
                    Secure:   true,
                    SameSite: http.SameSiteLaxMode,
                    MaxAge:   86400,
                })
            } else {
                _ = repo.UpdateLastAccessed(r.Context(), inbox.ID)
            }

            ctx := context.WithValue(r.Context(), inboxContextKey, inbox)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

func GetInboxFromContext(ctx context.Context) *models.Inbox {
    inbox, _ := ctx.Value(inboxContextKey).(*models.Inbox)
    return inbox
}
```

#### Routes

| Method | Path                           | Handler             | Description                  |
| ------ | ------------------------------ | ------------------- | ---------------------------- |
| GET    | `/`                            | `HandleIndex`       | Inbox view with email list   |
| GET    | `/email/{id}`                  | `HandleViewEmail`   | View single email            |
| GET    | `/email/{id}/attachment/{aid}` | `HandleAttachment`  | Download attachment          |
| POST   | `/email/{id}/delete`           | `HandleDeleteEmail` | Delete single email          |
| POST   | `/inbox/new`                   | `HandleNewInbox`    | Generate new inbox address   |
| GET    | `/api/emails`                  | `HandleAPIEmails`   | JSON endpoint for polling    |
| GET    | `/health`                      | `HandleHealth`      | Health check (no middleware) |

### 5.4 Background Cleanup Worker

```go
package worker

type CleanupWorker struct {
    repo     repository.Repository
    interval time.Duration
    logger   *slog.Logger
    done     chan struct{}
}

func (w *CleanupWorker) Start(ctx context.Context) {
    ticker := time.NewTicker(w.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-w.done:
            return
        case <-ticker.C:
            deleted, err := w.repo.DeleteExpiredInboxes(ctx)
            if err != nil {
                w.logger.Error("cleanup failed", "error", err)
            } else if deleted > 0 {
                w.logger.Info("cleanup complete", "deleted_inboxes", deleted)
            }
        }
    }
}

func (w *CleanupWorker) Stop() {
    close(w.done)
}
```

## Stage 6. Implementation Steps

### Phase 1: Project Setup

**Step 1.1: Initialize Project Structure**

```text
disposable-email/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── models/
│   │   └── models.go
│   ├── repository/
│   │   ├── repository.go
│   │   └── sqlite.go
│   ├── smtp/
│   │   ├── backend.go
│   │   └── session.go
│   ├── web/
│   │   ├── handlers.go
│   │   ├── middleware.go
│   │   └── routes.go
│   └── worker/
│       └── cleanup.go
├── templates/
│   ├── base.gohtml
│   ├── index.gohtml
│   └── email.gohtml
├── static/
│   └── style.css
├── migrations/
│   └── 001_initial.sql
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── README.md
```

**Step 1.2: Initialize Go Module**

```bash
go mod init github.com/yourname/disposable-email
go get modernc.org/sqlite
go get github.com/emersion/go-smtp
go get github.com/emersion/go-message
go get github.com/google/uuid
```

**Step 1.3: Implement Configuration Loading**

- Create `internal/config/config.go`
- Parse environment variables with defaults
- Validate required fields

### Phase 2: Storage Layer

**Step 2.1: Implement Database Initialization**

- Create `internal/repository/sqlite.go`
- Implement connection with pragmas (foreign keys, WAL mode)
- Run migrations on startup
- Implement connection pooling settings

**Step 2.2: Implement Inbox Repository Methods**

- `CreateInbox`: Insert with generated token and expiration
- `GetInboxByToken`: Select with token validation
- `GetInboxByAddress`: Case-insensitive address lookup
- `UpdateLastAccessed`: Touch timestamp on access
- `DeleteExpiredInboxes`: Bulk delete where `expires_at < now()`

**Step 2.3: Implement Email Repository Methods**

- `SaveEmail`: Insert email with all fields
- `GetEmailsByInboxID`: Paginated select ordered by `received_at DESC`
- `GetEmailByID`: Single email with inbox ownership check
- `MarkEmailAsRead`: Update `is_read` flag
- `DeleteEmail`: Delete with ownership verification

**Step 2.4: Implement Attachment Repository Methods**

- `SaveAttachment`: Insert with foreign key to email
- `GetAttachmentsByEmailID`: List attachments for email
- `GetAttachment`: Single attachment retrieval

**Step 2.5: Write Repository Unit Tests**

- Test all CRUD operations
- Test cascade deletes
- Test expiration cleanup

### Phase 3: SMTP Server

**Step 3.1: Implement SMTP Backend**

- Create `internal/smtp/backend.go`
- Implement `smtp.Backend` interface
- Configure server options (timeouts, limits)

**Step 3.2: Implement SMTP Session**

- Create `internal/smtp/session.go`
- Implement `Mail()`: Store sender address
- Implement `Rcpt()`: Validate domain and inbox existence
- Implement `Data()`: Parse message and store
- Implement `Reset()` and `Logout()`

**Step 3.3: Implement Email Parsing**

- Parse MIME multipart messages
- Extract plain text and HTML bodies
- Extract attachments with size limits
- Handle character encoding (UTF-8, ISO-8859-1, etc.)

**Step 3.4: Write SMTP Integration Tests**

- Test valid email delivery
- Test rejection of unknown recipients
- Test size limit enforcement
- Test malformed email handling

### Phase 4: Web Layer

**Step 4.1: Implement Session Middleware**

- Create `internal/web/middleware.go`
- Cookie creation and validation
- Inbox context injection
- CSRF protection (if forms are used)

**Step 4.2: Implement HTTP Handlers**

- Create `internal/web/handlers.go`
- `HandleIndex`: Render inbox with email list
- `HandleViewEmail`: Render single email (sanitize HTML)
- `HandleAttachment`: Serve attachment with correct MIME type
- `HandleDeleteEmail`: Delete with redirect
- `HandleNewInbox`: Clear cookie, redirect to `/`
- `HandleAPIEmails`: JSON response for polling

**Step 4.3: Create HTML Templates**

- `base.gohtml`: Layout with head, nav, footer
- `index.gohtml`: Email list table, refresh button, copy address
- `email.gohtml`: Email detail with headers, body, attachments
- Use `html/template` auto-escaping

**Step 4.4: Implement Static File Serving**

- Serve `/static/` directory
- Add cache headers for CSS/JS

**Step 4.5: Write HTTP Handler Tests**

- Test index page rendering
- Test email view with ownership
- Test attachment download
- Test new inbox generation

### Phase 5: Application Wiring

**Step 5.1: Implement Main Entry Point**

- Create `cmd/server/main.go`
- Load configuration
- Initialize database and repository
- Start SMTP server in goroutine
- Start HTTP server in goroutine
- Start cleanup worker in goroutine

**Step 5.2: Implement Graceful Shutdown**

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    // ... initialization ...

    <-ctx.Done()

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Shutdown in order: SMTP, HTTP, Worker, DB
    smtpServer.Shutdown(shutdownCtx)
    httpServer.Shutdown(shutdownCtx)
    cleanupWorker.Stop()
    repo.Close()
}
```

**Step 5.3: Implement Health Check Endpoint**

- Check database connectivity
- Return 200 OK with JSON status
- No session middleware on this route

### Phase 6: Security Hardening

**Step 6.1: Input Validation**

- Validate email address format before database queries
- Sanitize HTML email bodies before rendering (use `bluemonday`)
- Limit request body sizes on HTTP endpoints

**Step 6.2: Rate Limiting**

- Limit inbox creation per IP (e.g., 10 per hour)
- Limit SMTP connections per IP
- Use in-memory store or Redis for counters

**Step 6.3: Security Headers**

- Add middleware for security headers:

```go
w.Header().Set("X-Content-Type-Options", "nosniff")
w.Header().Set("X-Frame-Options", "DENY")
w.Header().Set("Content-Security-Policy", "default-src 'self'")
w.Header().Set("Referrer-Policy", "no-referrer")
```

**Step 6.4: Cookie Security**

- Set `HttpOnly`, `Secure`, `SameSite=Lax`
- Use `__Host-` prefix in production

### Phase 7: Containerization

**Step 7.1: Create Dockerfile**

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 appuser

WORKDIR /app
COPY --from=builder /server .
COPY templates/ ./templates/
COPY static/ ./static/

RUN mkdir -p /data && chown appuser:appuser /data

USER appuser

EXPOSE 25 8080

VOLUME ["/data"]

ENTRYPOINT ["./server"]
```

**Step 7.2: Create docker-compose.yml**

```yaml
version: "3.8"

services:
  app:
    build: .
    ports:
      - "25:25"
      - "8080:8080"
    environment:
      - DOMAIN=tempmail.example.com
      - DATABASE_PATH=/data/mail.db
      - INBOX_TTL_HOURS=24
    volumes:
      - mail_data:/data
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 5s
      retries: 3

volumes:
  mail_data:
```

### Phase 8: Testing and Documentation

**Step 8.1: Unit Tests**

- Repository layer tests with in-memory SQLite
- SMTP session tests with mock repository
- HTTP handler tests with `httptest`

**Step 8.2: Integration Tests**

- End-to-end SMTP to web flow
- Test email receipt and display
- Test inbox expiration

**Step 8.3: Manual Testing Checklist**

- [ ] Send email via external SMTP client
- [ ] Verify email appears in web UI
- [ ] Test attachment download
- [ ] Test inbox expiration after TTL
- [ ] Test new inbox generation
- [ ] Test concurrent requests

**Step 8.4: Documentation**

- README with setup instructions
- Environment variable reference
- DNS configuration guide
- Troubleshooting section

## Stage 7. Security Considerations

| Threat                 | Mitigation                                         |
| ---------------------- | -------------------------------------------------- |
| SQL Injection          | Parameterized queries only                         |
| XSS in email content   | Sanitize HTML with allowlist (bluemonday)          |
| SMTP abuse/spam relay  | Only accept mail for known inboxes                 |
| Denial of Service      | Rate limiting, size limits, connection limits      |
| Session hijacking      | HttpOnly, Secure cookies, token rotation           |
| Path traversal         | Validate IDs are integers, no file paths from user |
| Information disclosure | Ownership checks on all email/attachment access    |

## Stage 8. Future Enhancements (Out of Scope)

- Custom inbox addresses
- Email forwarding
- API key authentication for programmatic access
- Webhook notifications on new email
- TLS for SMTP (STARTTLS)
- Multiple domain support
- Admin dashboard with metrics
