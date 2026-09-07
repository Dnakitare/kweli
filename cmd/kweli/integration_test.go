package main

// End-to-end smoke test: builds the real kweli binary and runs it against
// internal/testserver's lying and truthful fixtures, checking exit codes
// and output shape. This is the closest thing to "a stranger runs one
// command against their vendor's sandbox" (brief §9) that a unit test can
// exercise.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestIntegration_SMARTBackendAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	bin := buildKweli(t)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "kweli-cli-test-client"
	srv, stats := testserver.NewSMARTProtected(&priv.PublicKey, clientID, time.Hour)
	defer srv.Close()

	jwkPath := filepath.Join(t.TempDir(), "key.jwk.json")
	jwk := map[string]any{
		"kty": "RSA", "kid": "cli-test-kid", "alg": "RS384",
		"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
		"d": base64.RawURLEncoding.EncodeToString(priv.D.Bytes()),
		"p": base64.RawURLEncoding.EncodeToString(priv.Primes[0].Bytes()),
		"q": base64.RawURLEncoding.EncodeToString(priv.Primes[1].Bytes()),
	}
	b, err := json.Marshal(jwk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jwkPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, srv.URL,
		"--smart-backend", "--client-id", clientID, "--jwk", jwkPath,
		"--resources", "Patient", "--format", "json", "--no-color")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("running kweli: %v (stderr: %s)", err, stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), `"server"`) {
		t.Fatalf("stdout doesn't look like the JSON report (stdout: %s) (stderr: %s)", stdout.String(), stderr.String())
	}
	if stats.TokenCalls() < 1 {
		t.Error("kweli never actually exchanged a token — the SMART auth flow didn't run")
	}
}

func TestIntegration_SMARTBackendRequiresClientIDAndJWK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	bin := buildKweli(t)

	var stderr bytes.Buffer
	cmd := exec.Command(bin, "https://example.org/r4", "--smart-backend")
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected kweli to exit non-zero without --client-id/--jwk, got: %v", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Errorf("exit code = %d, want 2 (config failure before probing)", exitErr.ExitCode())
	}
	if !strings.Contains(stderr.String(), "--client-id") {
		t.Errorf("stderr doesn't explain what's missing: %s", stderr.String())
	}
}

func TestIntegration_ExpectUSCore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	bin := buildKweli(t)
	srv := testserver.New()
	defer srv.Close()

	// --expect us-core is opt-in and produces "missing" findings the
	// default --fail-on (rejected,ignored) doesn't catch, so a plain run
	// should still exit 1 for the fixture's planted lies — but the report
	// itself must carry the missing findings, and asking --fail-on to
	// catch them specifically should still exit 1 too.
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, srv.URL, "--expect", "us-core", "--format", "json", "--no-color", "--resources", "Coverage")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("running kweli: %v (stderr: %s)", err, stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), `"missing"`) {
		t.Errorf("stdout doesn't mention any missing findings (stdout: %s) (stderr: %s)", stdout.String(), stderr.String())
	}

	var stdout2, stderr2 bytes.Buffer
	cmd2 := exec.Command(bin, srv.URL, "--expect", "us-core", "--fail-on", "missing", "--format", "text", "--no-color", "--resources", "Coverage")
	cmd2.Stdout = &stdout2
	cmd2.Stderr = &stderr2
	err := cmd2.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected kweli to exit non-zero with --fail-on missing against this fixture, got: %v (stdout: %s)", err, stdout2.String())
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
	}
	if !strings.Contains(stdout2.String(), "MISSING") {
		t.Errorf("text output missing the MISSING section: %s", stdout2.String())
	}
}

func TestIntegration_ExpectRejectsUnknownTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	bin := buildKweli(t)

	var stderr bytes.Buffer
	cmd := exec.Command(bin, "https://example.org/r4", "--expect", "not-a-real-table")
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a non-zero exit for an unknown --expect table, got: %v", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Errorf("exit code = %d, want 2 (config failure before probing)", exitErr.ExitCode())
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
