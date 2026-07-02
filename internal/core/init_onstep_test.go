package core

import (
	"context"
	"testing"
)

func TestInitOnStepReceivesEverySteps(t *testing.T) {
	cfg, profile := initProfile(t)
	var seen []string
	_, err := Init(context.Background(), InitOptions{
		Config: cfg, ProfileName: "personal", Profile: profile,
		CheckEmbeddingModel: stubEmbedCheck,
		OnStep:              func(s StepResult) { seen = append(seen, s.Name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 5 {
		t.Errorf("OnStep saw only %v", seen)
	}
}
