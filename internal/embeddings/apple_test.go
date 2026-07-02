package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func appleWithFakeRun(t *testing.T, fn func(stdin []byte) ([]byte, []byte, error)) *Apple {
	t.Helper()
	a := NewApple("/nonexistent/helper", "apple-nlcontextual-v1", 3)
	a.goos = "darwin" // tests exercise the protocol regardless of host OS
	a.run = func(ctx context.Context, bin string, stdin []byte) ([]byte, []byte, error) {
		return fn(stdin)
	}
	return a
}

func TestAppleEmbedRoundTrip(t *testing.T) {
	var gotReq appleRequest
	a := appleWithFakeRun(t, func(stdin []byte) ([]byte, []byte, error) {
		if err := json.Unmarshal(stdin, &gotReq); err != nil {
			t.Fatal(err)
		}
		return []byte(`{"model":"apple-nlcontextual-v1","dim":3,"vectors":[[1,2,3],[4,5,6]]}`), nil, nil
	})
	vecs, err := a.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotReq.Texts) != 2 || gotReq.Texts[0] != "a" {
		t.Errorf("request texts = %v", gotReq.Texts)
	}
	if len(vecs) != 2 || vecs[1][2] != 6 {
		t.Errorf("vectors = %v", vecs)
	}
}

func TestAppleEmbedEmptyInputNoSubprocess(t *testing.T) {
	called := false
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		called = true
		return nil, nil, nil
	})
	vecs, err := a.Embed(context.Background(), nil)
	if err != nil || vecs != nil || called {
		t.Errorf("empty input: vecs=%v err=%v called=%v", vecs, err, called)
	}
}

func TestAppleEmbedCountMismatch(t *testing.T) {
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		return []byte(`{"model":"m","dim":3,"vectors":[[1,2,3]]}`), nil, nil
	})
	if _, err := a.Embed(context.Background(), []string{"a", "b"}); err == nil ||
		!strings.Contains(err.Error(), "1 vectors for 2 inputs") {
		t.Errorf("want count-mismatch error, got %v", err)
	}
}

func TestAppleEmbedDimMismatch(t *testing.T) {
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		return []byte(`{"model":"m","dim":2,"vectors":[[1,2]]}`), nil, nil
	})
	if _, err := a.Embed(context.Background(), []string{"a"}); err == nil ||
		!strings.Contains(err.Error(), "reindex") {
		t.Errorf("want dim-mismatch error mentioning reindex, got %v", err)
	}
}

func TestAppleEmbedFailureIncludesStdoutAndStderr(t *testing.T) {
	a := appleWithFakeRun(t, func([]byte) ([]byte, []byte, error) {
		return []byte("assets not downloaded"), []byte("some warning"), errors.New("exit status 3")
	})
	_, err := a.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"exit status 3", "assets not downloaded", "some warning"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got %q", want, err.Error())
		}
	}
}

func TestAppleNonDarwinGuard(t *testing.T) {
	a := NewApple("/x", "m", 3)
	a.goos = "linux"
	if _, err := a.Embed(context.Background(), []string{"a"}); err == nil ||
		!strings.Contains(err.Error(), "macOS") {
		t.Errorf("want macOS-only error, got %v", err)
	}
	if err := a.Healthcheck(context.Background()); err == nil {
		t.Error("healthcheck should fail on non-darwin")
	}
}

func TestAppleModelAndDim(t *testing.T) {
	a := NewApple("/x", "apple-nlcontextual-v1", 512)
	if a.Model() != "apple-nlcontextual-v1" || a.Dim() != 512 {
		t.Errorf("model=%q dim=%d", a.Model(), a.Dim())
	}
}
