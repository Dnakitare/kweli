// kweli is a FHIR CapabilityStatement truth-tester CLI.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dnakitare/kweli/internal/capstmt"
	"github.com/Dnakitare/kweli/internal/client"
	"github.com/Dnakitare/kweli/internal/model"
	"github.com/Dnakitare/kweli/internal/probe"
	"github.com/Dnakitare/kweli/internal/report"
	"github.com/Dnakitare/kweli/internal/smart"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "kweli <base-url>",
		Short: "FHIR CapabilityStatement truth-tester",
		Long: "kweli probes a FHIR server's CapabilityStatement to verify that claimed " +
			"capabilities actually work.",
		Args: cobra.ExactArgs(1),
		RunE: runKweli,
	}

	// Flags
	var (
		token           string
		smartBackend    bool
		clientID        string
		jwk             string
		tokenURL        string
		scope           string
		resources       string
		concurrency     int
		rps             float64
		timeout         time.Duration
		budget          time.Duration
		format          string
		failOn          string
		probeOperations bool
		noColor         bool
		quiet           bool
		verbose         bool
		expect          string
	)

	rootCmd.Flags().StringVar(&token, "token", "", "Bearer token for authentication")
	rootCmd.Flags().BoolVar(&smartBackend, "smart-backend", false, "Use SMART Backend Services auth (client_credentials + private_key_jwt)")
	rootCmd.Flags().StringVar(&clientID, "client-id", "", "Client ID registered with the server (required with --smart-backend)")
	rootCmd.Flags().StringVar(&jwk, "jwk", "", "Path to a private JWK/JWK Set file (required with --smart-backend)")
	rootCmd.Flags().StringVar(&tokenURL, "token-url", "", "Token endpoint override (default: discover via .well-known/smart-configuration, then the CapabilityStatement's oauth-uris extension)")
	rootCmd.Flags().StringVar(&scope, "scope", smart.DefaultScope, "OAuth scope to request with --smart-backend")
	rootCmd.Flags().StringVar(&resources, "resources", "", "Comma-separated resource types to probe (empty = all)")
	rootCmd.Flags().IntVar(&concurrency, "concurrency", 4, "Number of resources to probe concurrently")
	rootCmd.Flags().Float64Var(&rps, "rps", 8, "Requests per second limit")
	rootCmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Timeout for individual HTTP requests")
	rootCmd.Flags().DurationVar(&budget, "budget", 5*time.Minute, "Total wall-clock budget for the run")
	rootCmd.Flags().StringVar(&format, "format", "text", "Output format: text, json, or markdown")
	rootCmd.Flags().StringVar(&failOn, "fail-on", "rejected,ignored", "Comma-separated failure categories: rejected, ignored, paging, untested, missing (missing only ever fires with --expect)")
	rootCmd.Flags().BoolVar(&probeOperations, "probe-operations", false, "Probe operations like $everything and $export")
	rootCmd.Flags().BoolVar(&noColor, "no-color", false, "Disable colored output")
	rootCmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress progress output (report still prints)")
	rootCmd.Flags().BoolVar(&verbose, "verbose", false, "Verbose client logging")
	rootCmd.Flags().StringVar(&expect, "expect", "", "Expectation table to check the CapabilityStatement against; only \"us-core\" is implemented")

	if err := rootCmd.Execute(); err != nil {
		// Cobra-level errors (missing base-url arg, unknown flag, etc.) are
		// config failures before any probing starts — exit 2, per the
		// brief's exit code contract. Exit 1 is reserved for findings that
		// match --fail-on.
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}
}

func runKweli(cmd *cobra.Command, args []string) error {
	baseURL := args[0]

	// Get flag values
	token, _ := cmd.Flags().GetString("token")
	smartBackend, _ := cmd.Flags().GetBool("smart-backend")
	clientID, _ := cmd.Flags().GetString("client-id")
	jwk, _ := cmd.Flags().GetString("jwk")
	tokenURL, _ := cmd.Flags().GetString("token-url")
	scope, _ := cmd.Flags().GetString("scope")
	resources, _ := cmd.Flags().GetString("resources")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	rps, _ := cmd.Flags().GetFloat64("rps")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	budget, _ := cmd.Flags().GetDuration("budget")
	format, _ := cmd.Flags().GetString("format")
	failOn, _ := cmd.Flags().GetString("fail-on")
	probeOperations, _ := cmd.Flags().GetBool("probe-operations")
	noColor, _ := cmd.Flags().GetBool("no-color")
	quiet, _ := cmd.Flags().GetBool("quiet")
	verbose, _ := cmd.Flags().GetBool("verbose")
	expect, _ := cmd.Flags().GetString("expect")

	// Validate --format
	if !isValidFormat(format) {
		fmt.Fprintf(os.Stderr, "Error: unknown format %q (must be text, json, or markdown)\n", format)
		os.Exit(2)
	}

	// Validate --fail-on entries
	failOnSet, err := parseFailOn(failOn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid --fail-on: %v\n", err)
		os.Exit(2)
	}

	// Validate --expect (Phase 3). "us-core" is the only table kweli
	// ships; refuse anything else rather than silently ignoring it.
	if expect != "" && expect != "us-core" {
		fmt.Fprintf(os.Stderr, "Error: unknown --expect %q (only \"us-core\" is implemented)\n", expect)
		os.Exit(2)
	}

	// Validate the SMART Backend Services flag combination before doing
	// any network I/O, per the exit-2-before-probing-starts contract.
	if smartBackend {
		if token != "" {
			fmt.Fprintf(os.Stderr, "--smart-backend and --token are mutually exclusive — pick one auth mode\n")
			os.Exit(2)
		}
		if clientID == "" {
			fmt.Fprintf(os.Stderr, "--smart-backend requires --client-id\n")
			os.Exit(2)
		}
		if jwk == "" {
			fmt.Fprintf(os.Stderr, "--smart-backend requires --jwk (path to a private JWK/JWK Set file)\n")
			os.Exit(2)
		}
	} else if clientID != "" || jwk != "" || tokenURL != "" {
		fmt.Fprintf(os.Stderr, "--client-id/--jwk/--token-url only apply with --smart-backend\n")
		os.Exit(2)
	}

	var signingKey *smart.SigningKey
	if smartBackend {
		key, err := smart.LoadKey(jwk)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loading --jwk: %v\n", err)
			os.Exit(2)
		}
		signingKey = key
	}

	// Build context with budget timeout
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	// Parse resources list
	resourcesSlice := parseResourceList(resources)

	// Build client config
	clientCfg := client.Config{
		BaseURL:     baseURL,
		Token:       token,
		Timeout:     timeout,
		RPS:         rps,
		Concurrency: concurrency,
		Verbose:     verbose && !quiet,
		Out:         os.Stderr,
	}

	cl := client.New(clientCfg)

	// Fetch /metadata
	resp, err := cl.Get(ctx, "metadata")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetching /metadata: %v\n", err)
		os.Exit(2)
	}

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "fetching /metadata: server returned %d\n", resp.StatusCode)
		os.Exit(2)
	}

	// Parse CapabilityStatement
	cs, err := capstmt.Parse(bytes.NewReader(resp.Body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetching /metadata: %v\n", err)
		os.Exit(2)
	}

	// SMART Backend Services: /metadata itself is fetched unauthenticated
	// above (it's meant to be publicly readable, and its own security
	// extension is one of the ways to discover where to authenticate).
	// Everything from here on needs a token, so rebuild cl with a
	// TokenSource instead of reusing the unauthenticated one.
	if smartBackend {
		discoveredTokenURL, err := smart.DiscoverTokenURL(ctx, cl, cs, baseURL, tokenURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discovering SMART token endpoint: %v\n", err)
			os.Exit(2)
		}
		ts := smart.NewTokenSource(signingKey, clientID, discoveredTokenURL, scope)
		clientCfg.Token = ""
		clientCfg.TokenSource = ts.Token
		cl = client.New(clientCfg)
	}

	// Run probes
	rep, err := probe.Run(ctx, cs, cl, probe.Options{
		Resources:       resourcesSlice,
		Concurrency:     concurrency,
		ProbeOperations: probeOperations,
		Seed:            0,
		Expect:          expect,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe run failed: %v\n", err)
		os.Exit(2)
	}

	// Set server from the base URL argument
	rep.Server = baseURL

	// Render report
	var renderErr error
	switch format {
	case "text":
		// Determine if we should use color
		color := shouldUseColor(noColor)
		renderErr = report.Text(os.Stdout, rep, color)
	case "json":
		renderErr = report.JSON(os.Stdout, rep)
	case "markdown":
		renderErr = report.Markdown(os.Stdout, rep)
	}

	if renderErr != nil {
		fmt.Fprintf(os.Stderr, "rendering report: %v\n", renderErr)
		os.Exit(2)
	}

	// Determine exit code based on findings and --fail-on
	exitCode := computeExitCode(rep, failOnSet)
	os.Exit(exitCode)

	return nil // Never reached, but keeps the signature
}

func isValidFormat(f string) bool {
	switch f {
	case "text", "json", "markdown":
		return true
	default:
		return false
	}
}

func parseFailOn(s string) (map[string]bool, error) {
	result := make(map[string]bool)
	validCategories := map[string]bool{
		"rejected": true,
		"ignored":  true,
		"paging":   true,
		"untested": true,
		// "missing" only ever produces findings under --expect (Phase 3);
		// listing it here is harmless when --expect isn't set (there will
		// simply never be a finding in that category to match).
		"missing": true,
	}

	if s == "" {
		return result, nil
	}

	entries := strings.Split(s, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !validCategories[entry] {
			return nil, fmt.Errorf("unknown category %q", entry)
		}
		result[entry] = true
	}

	return result, nil
}

func parseResourceList(s string) []string {
	if s == "" {
		return nil
	}

	entries := strings.Split(s, ",")
	var result []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			result = append(result, entry)
		}
	}
	return result
}

func shouldUseColor(noColor bool) bool {
	if noColor {
		return false
	}

	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	// Check if stdout is a terminal
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}

	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func computeExitCode(rep *model.Report, failOnSet map[string]bool) int {
	for _, finding := range rep.Findings {
		category := finding.FailOnCategory()
		if category != "" && failOnSet[category] {
			return 1
		}
	}
	return 0
}
