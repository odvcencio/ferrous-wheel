package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestScriptRunErrorKeepsExitCodeAndCleanupDetail(t *testing.T) {
	if os.Getenv("FW_TEST_EXIT_SEVEN") == "1" {
		os.Exit(7)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestScriptRunErrorKeepsExitCodeAndCleanupDetail$")
	cmd.Env = append(os.Environ(), "FW_TEST_EXIT_SEVEN=1")
	childErr := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(childErr, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("fixture exit = %v, want 7", childErr)
	}

	converted := scriptRunError(&processControlError{
		processErr: childErr,
		controlErr: errors.New("job cleanup failed"),
	})
	var status *exitStatusError
	if !errors.As(converted, &status) || status.code != 7 {
		t.Fatalf("converted exit = %v, want status 7", converted)
	}
	if got := converted.Error(); !strings.Contains(got, "job cleanup failed") || strings.Contains(got, "\n") {
		t.Fatalf("cleanup diagnostic must fit one line, got %q", got)
	}
}
