// Package mail sends transactional emails over SMTP.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is a plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// LogSender writes messages to the log instead of sending them.
// Used in development and when SMTP is not configured.
type LogSender struct{ Log *slog.Logger }

// Send implements Sender.
func (s LogSender) Send(_ context.Context, m Message) error {
	s.Log.Info("email (not sent: SMTP not configured)", "to", m.To, "subject", m.Subject, "body", m.Body)
	return nil
}

// TLS modes for SMTPSender.
const (
	TLSStartTLS = "starttls" // require STARTTLS (default)
	TLSImplicit = "tls"      // TLS from the first byte (usually port 465)
	TLSNone     = "none"     // no encryption; only for local mail catchers
)

// SMTPSender sends mail through an SMTP server.
type SMTPSender struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	TLS      string // one of the TLS* constants
}

// Send implements Sender.
func (s SMTPSender) Send(ctx context.Context, m Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("invalid from address: %w", err)
	}
	msg, err := build(from, m)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var conn net.Conn
	if s.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: s.Host}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if s.TLS == TLSStartTLS {
		// Never fall back to plaintext: an attacker on the network path
		// could hide STARTTLS from the server's reply to read login codes.
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not offer STARTTLS (set SMTP_TLS=none only for local mail catchers)")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
			return err
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func build(from *mail.Address, m Message) ([]byte, error) {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return nil, fmt.Errorf("invalid header value")
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(m.Body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
