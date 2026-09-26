// Package notify delivers notifications: ntfy (HTTP) and email (SMTP).
package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/app"
)

// Channels builds the configured notifiers (for app.Notifications).
func Channels(s app.NotificationSettings) []app.Notifier {
	var out []app.Notifier
	if s.Ntfy.URL != "" {
		out = append(out, Ntfy{URL: s.Ntfy.URL, Token: s.Ntfy.Token})
	}
	if s.Email.Host != "" && s.Email.To != "" {
		out = append(out, Email{S: s.Email})
	}
	return out
}

// Ntfy posts to an ntfy topic URL.
type Ntfy struct {
	URL, Token string
	Client     *http.Client
}

var tags = map[string]string{
	app.EventPrintDone: "white_check_mark", app.EventPrintStopped: "stop_sign", app.EventPrintError: "warning",
	app.EventImportDone: "inbox_tray", app.EventImportFailed: "warning", app.EventAccountRegistered: "bust_in_silhouette",
}

func (n Ntfy) Send(ctx context.Context, note app.Notification) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, strings.NewReader(note.Message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", note.Title)
	if note.Priority == "high" {
		req.Header.Set("Priority", "high")
	}
	if t := tags[note.Event]; t != "" {
		req.Header.Set("Tags", t)
	}
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	c := n.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("ntfy: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Email sends over SMTP.
type Email struct {
	S app.EmailSettings
	// InsecureSkipVerify is for tests only.
	InsecureSkipVerify bool
}

func (e Email) Send(ctx context.Context, note app.Notification) error {
	s := e.S
	port := s.Port
	security := s.Security
	if security == "" {
		security = "starttls"
	}
	if port == 0 {
		port = map[string]int{"starttls": 587, "tls": 465, "none": 25}[security]
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(port))
	tlsConf := &tls.Config{ServerName: s.Host, InsecureSkipVerify: e.InsecureSkipVerify}
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, tlsConf)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: %w", err)
	}
	defer c.Close()
	if security == "starttls" {
		if err := c.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("email: STARTTLS: %w", err)
		}
	}
	if s.Username != "" {
		// PlainAuth refuses to send the password without TLS (except to
		// localhost), which is what we want.
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("email: login: %w", err)
		}
	}
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("email: sender: %w", err)
	}
	to, err := mail.ParseAddressList(s.To)
	if err != nil {
		return fmt.Errorf("email: recipients: %w", err)
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	var rcpt []string
	for _, t := range to {
		if err := c.Rcpt(t.Address); err != nil {
			return fmt.Errorf("email: %w", err)
		}
		rcpt = append(rcpt, t.String())
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	subject := mimeHeader(note.Title)
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from.String(), strings.Join(rcpt, ", "), subject, time.Now().Format(time.RFC1123Z), strings.ReplaceAll(note.Message, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return c.Quit()
}

// mimeHeader encodes a header value that isn't plain ASCII.
func mimeHeader(s string) string {
	for _, r := range s {
		if r > 126 || r < 32 {
			return mimeWord(s)
		}
	}
	return s
}
