package main

// End-to-end smoke test: builds the real kweli binary and runs it against
// internal/testserver's lying and truthful fixtures, checking exit codes
// and output shape. This is the closest thing to "a stranger runs one
// command against their vendor's sandbox" (brief §9) that a unit test can
// exercise.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnakitare/kweli/internal/testserver"
)

func buildKweli(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "kweli")
	goBin := "go"
	if _, err := exec.LookPath("/opt/homebrew/bin/go"); err == nil {
		goBin = "/opt/homebrew/bin/go"
	}
	cmd := exec.Command(goBin, "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building kweli: %v\n%s", err, out)
	}
	return bin
}

func TestIntegration_LyingServerExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	bin := buildKweli(t)
	srv := testserver.New()
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, srv.URL, "--format", "json", "--no-color")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if err != nil && !ok {
		t.Fatalf("running kweli: %v (stderr: %s)", err, stderr.String())
	}
	exitCode := 0
	if exitErr != nil {
		exitCode = exitErr.ExitCode()
	}
	// Default --fail-on is rejected,ignored; the lying fixture has both,
	// so exit code must be 1 (not 0 "clean", not 2 "config/network error").
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1 (stdout: %s) (stderr: %s)", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"server"`) {
		t.Errorf("stdout doesn't look like the JSON report: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "kweli-testserver") {
		t.Errorf("stdout missing software identification: %s", stdout.String())
	}
}

func TestIntegration_TruthfulServerExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	bin := buildKweli(t)
	srv := testserver.NewTruthful()
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, srv.URL, "--format", "text", "--no-color")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("kweli against a truthful server should exit 0, got error: %v (stdout: %s) (stderr: %s)", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "LIES (0)") {
		t.Errorf("LIES section should be omitted entirely when there are none, got: %s", stdout.String())
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
