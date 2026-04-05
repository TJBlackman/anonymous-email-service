package smtp

import (
	"io"

	"anonymous-email-service/internal/repository"
)

type Session struct {
	backend *Backend
	from    string
	rcpts   []string
}

func (s *Session) Mail(from string) error {
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string) error {
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *Session) Data(io.Reader) error {
	return repository.ErrNotImplemented
}

func (s *Session) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *Session) Logout() error {
	return nil
}
