package clientip

import (
	"net"
	"net/http"

	gosmtp "github.com/emersion/go-smtp"
)

const unknownClient = "unknown"

func FromHTTPRequest(r *http.Request) string {
	if r == nil {
		return unknownClient
	}
	return FromRemoteAddr(r.RemoteAddr)
}

func FromSMTPConn(conn *gosmtp.Conn) string {
	if conn == nil || conn.Conn() == nil || conn.Conn().RemoteAddr() == nil {
		return unknownClient
	}
	return FromRemoteAddr(conn.Conn().RemoteAddr().String())
}

func FromRemoteAddr(remoteAddr string) string {
	if remoteAddr == "" {
		return unknownClient
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}

	return remoteAddr
}
