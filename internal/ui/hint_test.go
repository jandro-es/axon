package ui

import (
	"errors"
	"strings"
	"testing"
)

func TestHintAppleHelper(t *testing.T) {
	err := errors.New(`apple embed helper /x/axon-apple-embed: fork/exec: no such file or directory`)
	h := Hint(err)
	if !strings.Contains(h, "axon init") {
		t.Errorf("apple helper hint should point at axon init, got %q", h)
	}
}
