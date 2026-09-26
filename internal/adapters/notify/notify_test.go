package notify

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeStev/stl-library/internal/app"
)

func TestNtfy(t *testing.T) {
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()
	n := Ntfy{URL: srv.URL + "/printer", Token: "tk_123"}
	err := n.Send(context.Background(), app.Notification{Event: app.EventPrintError, Title: "Printer error", Message: "resin low", Priority: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/printer" || got.Header.Get("Title") != "Printer error" || got.Header.Get("Priority") != "high" ||
		got.Header.Get("Tags") != "warning" || got.Header.Get("Authorization") != "Bearer tk_123" || body != "resin low" {
		t.Errorf("request %v %v body %q", got.URL, got.Header, body)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "forbidden", 403) }))
	defer bad.Close()
	if err := (Ntfy{URL: bad.URL + "/x"}).Send(context.Background(), app.Notification{}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("error: %v", err)
	}
}

// fakeSMTP accepts one plain-text session and returns what was sent.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		defer ln.Close()
		r := bufio.NewReader(c)
		var log strings.Builder
		reply := func(s string) { io.WriteString(c, s+"\r\n") }
		reply("220 fake")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				got <- log.String()
				return
			}
			log.WriteString(line)
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				reply("250 fake")
			case cmd == "DATA":
				reply("354 go ahead")
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					log.WriteString(l)
					if l == ".\r\n" {
						break
					}
				}
				reply("250 ok")
			case cmd == "QUIT":
				reply("221 bye")
				got <- log.String()
				return
			default:
				reply("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestEmailWithoutTLS(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	e := Email{S: app.EmailSettings{Host: host, Port: p, Security: "none", From: "STL <stl@example.org>", To: "me@example.org, you@example.org"}}
	if err := e.Send(context.Background(), app.Notification{Title: "Druck fertig ✓", Message: "Bell Head is done."}); err != nil {
		t.Fatal(err)
	}
	s := <-got
	for _, want := range []string{"MAIL FROM:<stl@example.org>", "RCPT TO:<me@example.org>", "RCPT TO:<you@example.org>",
		"Subject: =?utf-8?q?Druck_fertig_=E2=9C=93?=", "Bell Head is done."} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestChannelsOnlyConfigured(t *testing.T) {
	if n := len(Channels(app.NotificationSettings{})); n != 0 {
		t.Errorf("%d channels without settings", n)
	}
	s := app.NotificationSettings{Ntfy: app.NtfySettings{URL: "https://ntfy.sh/x"}, Email: app.EmailSettings{Host: "smtp.x", To: "a@b.c"}}
	if n := len(Channels(s)); n != 2 {
		t.Errorf("%d channels", n)
	}
}
