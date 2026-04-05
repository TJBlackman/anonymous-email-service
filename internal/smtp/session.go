package smtp

import (
	"bytes"
	"context"
	"errors"
	"io"
	stdmail "net/mail"
	"strings"
	"time"

	"anonymous-email-service/internal/models"
	"anonymous-email-service/internal/repository"
	"github.com/emersion/go-message"
	msgmail "github.com/emersion/go-message/mail"
	gosmtp "github.com/emersion/go-smtp"
)

type Session struct {
	backend *Backend
	from    string
	rcpts   []string
	inbox   *models.Inbox
}

func (s *Session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	addr, err := stdmail.ParseAddress(to)
	if err != nil {
		return smtpError(550, gosmtp.EnhancedCode{5, 1, 3}, "invalid recipient address")
	}

	parts := strings.Split(addr.Address, "@")
	if len(parts) != 2 || !strings.EqualFold(parts[1], s.backend.domain) {
		return smtpError(550, gosmtp.EnhancedCode{5, 7, 1}, "relay not permitted")
	}

	inbox, err := s.backend.repo.GetInboxByAddress(context.Background(), addr.Address)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return smtpError(550, gosmtp.EnhancedCode{5, 1, 1}, "user not found")
		}
		return smtpError(451, gosmtp.EnhancedCode{4, 3, 0}, "temporary mailbox lookup failure")
	}

	s.inbox = inbox
	s.rcpts = append(s.rcpts, addr.Address)
	return nil
}

func (s *Session) Data(r io.Reader) error {
	raw, err := readRawMessage(r, s.backend.config.MaxEmailSize)
	if err != nil {
		if errors.Is(err, gosmtp.ErrDataTooLarge) {
			return gosmtp.ErrDataTooLarge
		}
		return smtpError(451, gosmtp.EnhancedCode{4, 3, 0}, "temporary read failure")
	}

	if s.inbox == nil || len(s.rcpts) == 0 {
		return smtpError(554, gosmtp.EnhancedCode{5, 5, 1}, "no valid recipients")
	}

	email, attachments, err := s.parseMessage(raw, s.inbox)
	if err != nil {
		return err
	}

	if err := s.backend.repo.SaveInboundMessage(context.Background(), email, attachments); err != nil {
		return smtpError(451, gosmtp.EnhancedCode{4, 3, 0}, "temporary storage failure")
	}

	return nil
}

func (s *Session) Reset() {
	s.from = ""
	s.rcpts = nil
	s.inbox = nil
}

func (s *Session) Logout() error {
	return nil
}

func (s *Session) parseMessage(raw []byte, inbox *models.Inbox) (*models.Email, []*models.Attachment, error) {
	reader, err := msgmail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
	}

	messageID, err := reader.Header.MessageID()
	if err != nil {
		return nil, nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "invalid message headers")
	}

	subject, err := reader.Header.Subject()
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "invalid message headers")
	}
	if subject == "" {
		subject = "(no subject)"
	}

	sender, senderName, err := parseSender(reader.Header, s.from)
	if err != nil {
		return nil, nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "invalid message headers")
	}

	bodyText, bodyHTML, attachments, err := s.parseParts(reader)
	if err != nil {
		return nil, nil, err
	}

	email := &models.Email{
		InboxID:        inbox.ID,
		MessageID:      messageID,
		Sender:         sender,
		SenderName:     senderName,
		Recipient:      inbox.Address,
		Subject:        subject,
		BodyHTML:       bodyHTML,
		BodyText:       bodyText,
		RawHeaders:     extractRawHeaders(raw),
		SizeBytes:      int64(len(raw)),
		HasAttachments: len(attachments) > 0,
		ReceivedAt:     time.Now().UTC(),
	}

	return email, attachments, nil
}

func (s *Session) parseParts(reader *msgmail.Reader) (string, string, []*models.Attachment, error) {
	var bodyText string
	var bodyHTML string
	var attachments []*models.Attachment

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) {
			return "", "", nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
		}

		switch header := part.Header.(type) {
		case *msgmail.InlineHeader:
			contentType, _, headerErr := header.ContentType()
			if headerErr != nil {
				return "", "", nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
			}

			content, readErr := io.ReadAll(part.Body)
			if readErr != nil {
				return "", "", nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
			}

			switch strings.ToLower(contentType) {
			case "text/plain":
				if bodyText == "" && len(content) > 0 {
					bodyText = string(content)
				}
			case "text/html":
				if bodyHTML == "" && len(content) > 0 {
					bodyHTML = string(content)
				}
			}
		case *msgmail.AttachmentHeader:
			attachment, err := s.readAttachment(header, part.Body)
			if err != nil {
				return "", "", nil, err
			}
			attachments = append(attachments, attachment)
		default:
			if _, err := io.Copy(io.Discard, part.Body); err != nil {
				return "", "", nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
			}
		}
	}

	return bodyText, bodyHTML, attachments, nil
}

func (s *Session) readAttachment(header *msgmail.AttachmentHeader, body io.Reader) (*models.Attachment, error) {
	filename, err := header.Filename()
	if err != nil {
		return nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
	}
	if filename == "" {
		filename = "attachment"
	}

	contentType, _, err := header.ContentType()
	if err != nil {
		return nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	content, err := io.ReadAll(io.LimitReader(body, s.backend.config.MaxAttachmentSize+1))
	if err != nil {
		return nil, smtpError(554, gosmtp.EnhancedCode{5, 6, 0}, "failed to parse message")
	}
	if int64(len(content)) > s.backend.config.MaxAttachmentSize {
		return nil, smtpError(552, gosmtp.EnhancedCode{5, 3, 4}, "attachment too large")
	}

	return &models.Attachment{
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   int64(len(content)),
		Content:     content,
	}, nil
}

func parseSender(header msgmail.Header, fallback string) (string, string, error) {
	addresses, err := header.AddressList("From")
	if err != nil {
		return "", "", err
	}
	if len(addresses) == 0 {
		return fallback, "", nil
	}

	return addresses[0].Address, addresses[0].Name, nil
}

func readRawMessage(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := &io.LimitedReader{R: r, N: maxBytes + 1}
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		_, _ = io.Copy(io.Discard, r)
		return nil, gosmtp.ErrDataTooLarge
	}

	return raw, nil
}

func extractRawHeaders(raw []byte) string {
	for _, sep := range [][]byte{[]byte("\r\n\r\n"), []byte("\n\n")} {
		if idx := bytes.Index(raw, sep); idx >= 0 {
			return string(raw[:idx])
		}
	}

	return string(raw)
}

func smtpError(code int, enhanced gosmtp.EnhancedCode, message string) error {
	return &gosmtp.SMTPError{
		Code:         code,
		EnhancedCode: enhanced,
		Message:      message,
	}
}
