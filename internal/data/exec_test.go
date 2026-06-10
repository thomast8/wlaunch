package data

import (
	"context"
	"strings"
	"testing"
)

func TestRunEnvInjectsVariable(t *testing.T) {
	out, err := RunEnv(context.Background(), "", []string{"WLAUNCH_TEST_VAR=hello"},
		"sh", "-c", `printf %s "$WLAUNCH_TEST_VAR"`)
	if err != nil {
		t.Fatalf("RunEnv: %v", err)
	}
	if got := string(out); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestRunEnvKeepsParentEnv(t *testing.T) {
	// PATH must survive the overlay or no subprocess would resolve at all.
	out, err := RunEnv(context.Background(), "", []string{"WLAUNCH_TEST_VAR=x"},
		"sh", "-c", `printf %s "$PATH"`)
	if err != nil {
		t.Fatalf("RunEnv: %v", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("PATH is empty in child process; parent env was dropped")
	}
}
