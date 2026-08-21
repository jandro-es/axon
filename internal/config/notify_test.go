package config

import (
	"strings"
	"testing"
)

func TestValidateNotify(t *testing.T) {
	p := func(url string, events ...string) Profile {
		return Profile{Notify: NotifyConfig{URL: url, Events: events}}
	}

	// Off is valid, and is the default.
	if err := validateNotify(Profile{}); err != nil {
		t.Fatalf("both-empty must be valid (it is the off state): %v", err)
	}
	if err := validateNotify(p("https://ntfy.sh/axon-test", "automation.fail")); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	// Plaintext to loopback and to a private IP is allowed (self-hosted ntfy).
	for _, u := range []string{"http://localhost:8080/axon", "http://127.0.0.1:8080/axon", "http://192.168.1.10/axon"} {
		if err := validateNotify(p(u, "automation.fail")); err != nil {
			t.Errorf("self-hosted target %q must be allowed: %v", u, err)
		}
	}
	// An UNRECOGNISED kind is accepted: kinds are not statically enumerable,
	// so a stale list must never refuse valid config. Doctor warns instead.
	if err := validateNotify(p("https://ntfy.sh/x", "some.future.kind")); err != nil {
		t.Fatalf("an unrecognised kind must be accepted (doctor warns): %v", err)
	}

	cases := []struct {
		name string
		prof Profile
		want string
	}{
		{"events without url", p("", "automation.fail"), "notify.url"},
		{"url without events", p("https://ntfy.sh/x"), "notify.events"},
		{"unparseable url", p("://nope", "automation.fail"), "url"},
		{"file scheme", p("file:///etc/passwd", "automation.fail"), "scheme"},
		{"plaintext to a public host", p("http://ntfy.sh/x", "automation.fail"), "https"},
		{"blank kind", p("https://ntfy.sh/x", "   "), "empty"},
	}
	for _, c := range cases {
		err := validateNotify(c.prof)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestNotifyEnabled(t *testing.T) {
	if (NotifyConfig{}).Enabled() {
		t.Error("empty config must be disabled")
	}
	if (NotifyConfig{URL: "https://ntfy.sh/x"}).Enabled() {
		t.Error("a url with no events must be disabled")
	}
	if !(NotifyConfig{URL: "https://ntfy.sh/x", Events: []string{"automation.fail"}}).Enabled() {
		t.Error("a fully configured notifier must be enabled")
	}
}
