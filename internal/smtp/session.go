package smtp

import (
	"io"
	"net/mail"
	"strings"

	gosmtp "github.com/emersion/go-smtp"
)

type Session struct {
	backend *Backend
	from    string
	rcpts   []string
}

var stage2UnavailableError = &gosmtp.SMTPError{
	Code:         451,
	EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
	Message:      "message handling is not implemented yet",
}

func (s *Session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 1, 3},
			Message:      "invalid recipient address",
		}
	}

	parts := strings.Split(addr.Address, "@")
	if len(parts) != 2 || !strings.EqualFold(parts[1], s.backend.domain) {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 7, 1},
			Message:      "relay not permitted",
		}
	}

	s.rcpts = append(s.rcpts, addr.Address)
	return nil
}

func (s *Session) Data(r io.Reader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}

	return stage2UnavailableError
}

func (s *Session) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *Session) Logout() error {
	return nil
}
