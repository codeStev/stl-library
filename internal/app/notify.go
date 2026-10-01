package app

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

// Events a notification can be sent for.
const (
	EventPrintDone    = "print.done"
	EventPrintStopped = "print.stopped"
	EventPrintError   = "print.error"
	EventImportDone   = "import.done"
	EventImportFailed = "import.failed"
	// EventAccountRegistered: someone created an account themselves.
	EventAccountRegistered = "account.registered"
)

// (EventDigest, the weekly summary, is in digest.go.)

// AllEvents in display order.
var AllEvents = []string{EventPrintDone, EventPrintStopped, EventPrintError, EventImportDone, EventImportFailed, EventAccountRegistered, EventDigest}

// Notification is one message.
type Notification struct {
	Event    string
	Title    string
	Message  string
	Priority string // "default" or "high"
}

// Notifier delivers notifications over one channel.
type Notifier interface {
	Send(ctx context.Context, n Notification) error
}

// NotificationSettings configure the channels and which events are sent.
type NotificationSettings struct {
	Ntfy   NtfySettings    `json:"ntfy"`
	Email  EmailSettings   `json:"email"`
	Events map[string]bool `json:"events"`
}

// NtfySettings: a topic URL on ntfy.sh or a self-hosted ntfy server.
type NtfySettings struct {
	URL   string `json:"url"`   // e.g. https://ntfy.sh/my-printer-topic
	Token string `json:"token"` // optional access token; stored sealed
}

// EmailSettings: an SMTP server.
type EmailSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // "starttls" (default), "tls", "none"
	Username string `json:"username"`
	Password string `json:"password"` // stored sealed
	From     string `json:"from"`
	To       string `json:"to"`
}

// Sealer encrypts values for storage.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// NotificationStore keeps the notification settings (secrets sealed).
type NotificationStore interface {
	NotificationSettings(ctx context.Context) (NotificationSettings, error)
	SaveNotificationSettings(ctx context.Context, s NotificationSettings) error
}

// Channels builds the notifiers for settings (only configured channels).
type Channels func(NotificationSettings) []Notifier

// Notifications sends events over the configured channels.
type Notifications struct {
	Store    NotificationStore
	Sealer   Sealer
	Channels Channels
	// OnError receives delivery problems (e.g. to log them); may be nil.
	OnError func(event string, err error)
}

func (n *Notifications) report(event string, err error) {
	if n.OnError != nil {
		n.OnError(event, err)
	}
}

// Settings returns the settings with secrets opened.
func (n *Notifications) Settings(ctx context.Context) (NotificationSettings, error) {
	s, err := n.Store.NotificationSettings(ctx)
	if err != nil {
		return s, err
	}
	if s.Ntfy.Token, err = n.Sealer.Open(s.Ntfy.Token); err != nil {
		return s, err
	}
	if s.Email.Password, err = n.Sealer.Open(s.Email.Password); err != nil {
		return s, err
	}
	return s, nil
}

// Save validates and stores settings. An empty token/password keeps the
// stored one (the UI never gets secrets back); keep is false to clear it.
func (n *Notifications) Save(ctx context.Context, s NotificationSettings, keepSecrets bool) error {
	if err := validNotifications(s); err != nil {
		return err
	}
	if keepSecrets {
		old, err := n.Settings(ctx)
		if err != nil {
			return err
		}
		if s.Ntfy.Token == "" {
			s.Ntfy.Token = old.Ntfy.Token
		}
		if s.Email.Password == "" {
			s.Email.Password = old.Email.Password
		}
	}
	var err error
	if s.Ntfy.Token, err = n.Sealer.Seal(s.Ntfy.Token); err != nil {
		return err
	}
	if s.Email.Password, err = n.Sealer.Seal(s.Email.Password); err != nil {
		return err
	}
	return n.Store.SaveNotificationSettings(ctx, s)
}

// Notify sends an event over every channel, if the event is switched on.
// Delivery problems are logged, not returned: a notification must never
// break what triggered it.
func (n *Notifications) Notify(ctx context.Context, note Notification) {
	if n == nil {
		return
	}
	s, err := n.Settings(ctx)
	if err != nil {
		n.report(note.Event, err)
		return
	}
	if !s.Events[note.Event] {
		return
	}
	for _, ch := range n.Channels(s) {
		if err := ch.Send(ctx, note); err != nil {
			n.report(note.Event, err)
		}
	}
}

// Test sends a test message with the given settings (secrets left empty
// fall back to the stored ones); every channel's error is returned.
func (n *Notifications) Test(ctx context.Context, s NotificationSettings) error {
	if err := validNotifications(s); err != nil {
		return err
	}
	old, err := n.Settings(ctx)
	if err != nil {
		return err
	}
	if s.Ntfy.Token == "" {
		s.Ntfy.Token = old.Ntfy.Token
	}
	if s.Email.Password == "" {
		s.Email.Password = old.Email.Password
	}
	chs := n.Channels(s)
	if len(chs) == 0 {
		return fmt.Errorf("%w: no channel configured", ErrInvalid)
	}
	var errs []error
	for _, ch := range chs {
		if err := ch.Send(ctx, Notification{Event: "test", Title: "STL Library", Message: "Test notification - it works."}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validNotifications(s NotificationSettings) error {
	if s.Ntfy.URL != "" {
		u, err := url.Parse(s.Ntfy.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.Trim(u.Path, "/") == "" {
			return fmt.Errorf("%w: the ntfy URL must look like https://ntfy.sh/<topic>", ErrInvalid)
		}
	}
	e := s.Email
	if e.Host != "" {
		if e.Port < 0 || e.Port > 65535 {
			return fmt.Errorf("%w: SMTP port", ErrInvalid)
		}
		switch e.Security {
		case "", "starttls", "tls", "none":
		default:
			return fmt.Errorf("%w: SMTP security must be starttls, tls or none", ErrInvalid)
		}
		if _, err := mail.ParseAddress(e.From); err != nil {
			return fmt.Errorf("%w: sender address", ErrInvalid)
		}
		if _, err := mail.ParseAddressList(e.To); err != nil {
			return fmt.Errorf("%w: recipient address", ErrInvalid)
		}
	}
	for ev := range s.Events {
		known := false
		for _, k := range AllEvents {
			known = known || k == ev
		}
		if !known {
			return fmt.Errorf("%w: unknown event %q", ErrInvalid, ev)
		}
	}
	return nil
}
