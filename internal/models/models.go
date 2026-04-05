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
