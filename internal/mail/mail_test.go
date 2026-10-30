package mail

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal plaintext SMTP server that never offers STARTTLS.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	data []string // received message bodies
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-fake")
			reply("250 8BITMIME")
		case strings.HasPrefix(cmd, "DATA"):
			reply("354 go ahead")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			f.mu.Lock()
			f.data = append(f.data, body.String())
			f.mu.Unlock()
			reply("250 queued")
		case strings.HasPrefix(cmd, "QUIT"):
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (f *fakeSMTP) sender(mode string) SMTPSender {
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return SMTPSender{Host: host, Port: p, From: "makers.lv <noreply@makers.lv>", TLS: mode}
}

func (f *fakeSMTP) received() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

func TestStartTLSIsRequired(t *testing.T) {
	f := startFakeSMTP(t)
	msg := Message{To: "a@b.lv", Subject: "123456 is your code", Body: "Code: 123456"}

	err := f.sender(TLSStartTLS).Send(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected a STARTTLS error, got %v", err)
	}
	if f.received() != 0 {
		t.Fatal("message was sent in plaintext despite starttls mode")
	}

	if err := f.sender(TLSNone).Send(context.Background(), msg); err != nil {
		t.Fatalf("none mode: %v", err)
	}
	if f.received() != 1 {
		t.Fatal("message not delivered in none mode")
	}
}

func TestBuildRejectsHeaderInjection(t *testing.T) {
	f := startFakeSMTP(t)
	err := f.sender(TLSNone).Send(context.Background(), Message{To: "a@b.lv\r\nBcc: x@y.lv", Subject: "s"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if f.received() != 0 {
		t.Fatal("message with an injected header was sent")
	}
}
