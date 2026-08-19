package dashboard

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestListenRefusesTakenPort is the reason Listen is split out of Serve: the
// daemon must be able to fail fast when another daemon already holds the port,
// rather than logging a warning from a goroutine and running on headless.
func TestListenRefusesTakenPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	host, port, err := net.SplitHostPort(held.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Host: host, Port: atoi(t, port)})
	ln, err := srv.Listen()
	if err == nil {
		ln.Close()
		t.Fatal("Listen on a taken port: want an error, got nil")
	}
}

// TestListenThenServe covers the happy path: a bound listener serves requests
// and Serve returns once the context is cancelled.
func TestListenThenServe(t *testing.T) {
	// Port 0 means "the default" to New, so take a real free port instead.
	srv := New(Config{Host: "127.0.0.1", Port: freePort(t)})
	ln, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve after cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Serve did not return within 5s of cancellation")
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a port: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// freePort returns a port nothing is listening on, by briefly binding one.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}
