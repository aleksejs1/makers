package config

import "testing"

func TestSMTPTLSDefaults(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, "starttls"},
		{[]string{"-smtp-port", "465"}, "tls"},
		{[]string{"-smtp-tls", "none"}, "none"},
	}
	for _, tt := range tests {
		c, err := Load(append([]string{"-env", "dev"}, tt.args...))
		if err != nil {
			t.Fatal(err)
		}
		if c.SMTPTLS != tt.want {
			t.Errorf("Load(%v).SMTPTLS = %q, want %q", tt.args, c.SMTPTLS, tt.want)
		}
	}
	if _, err := Load([]string{"-env", "dev", "-smtp-tls", "maybe"}); err == nil {
		t.Error("invalid TLS mode accepted")
	}
}

func TestProductionRequiresSMTP(t *testing.T) {
	if _, err := Load(nil); err == nil {
		t.Fatal("prod without SMTP_HOST must fail: login codes would be logged")
	}
	if _, err := Load([]string{"-smtp-host", "smtp.example.com"}); err != nil {
		t.Fatalf("prod with SMTP: %v", err)
	}
	if _, err := Load([]string{"-env", "dev"}); err != nil {
		t.Fatalf("dev without SMTP: %v", err)
	}
}

func TestTrustedProxies(t *testing.T) {
	c, err := Load([]string{"-env", "dev", "-trusted-proxies", "127.0.0.1, 172.16.0.0/12,::1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 3 || c.TrustedProxies[0].String() != "127.0.0.1/32" {
		t.Fatalf("got %v", c.TrustedProxies)
	}
	if _, err := Load([]string{"-env", "dev", "-trusted-proxies", "not-an-ip"}); err == nil {
		t.Fatal("invalid proxy accepted")
	}
}
