package smtp

import (
	"log/slog"

	"anonymous-email-service/internal/config"
	"anonymous-email-service/internal/repository"
)

type Backend struct {
	repo   repository.Repository
	domain string
	config *config.Config
	logger *slog.Logger
}
