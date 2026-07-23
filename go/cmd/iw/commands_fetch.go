package main

// commands_fetch.go ports the fetch/fetch-diag CLI composition layer from
// the original implementation. The collector engine and product adapters live in
// internal/collectors; this file owns only argument/env resolution, the closed
// built-in authority choice, real-transport lifetime, diagnostics, and process
// exit classification.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/httptransport"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/spf13/cobra"
)

const fetchDiagnosticTimeoutMs = 15_000

var positiveFetchConcurrency = regexp.MustCompile(`^[1-9][0-9]*$`)

// deferredFetchPerformanceRecorder is a compile-time witness that a value
// can satisfy collectors.PerformanceRecorder, the one recorder seam this
// command still has (httptransport, unlike the retired resthttp package,
// does not expose a per-attempt/per-retry HTTP telemetry seam of its own --
// nothing wired it to real telemetry, so there was nothing to preserve; see
// the Go runtime contract §7). Block E will provide the real recorder and
// atomic report writer. Until then dispatch passes nil; the interface keeps
// this command from inventing a second telemetry contract.
type deferredFetchPerformanceRecorder struct{}

var _ collectors.PerformanceRecorder = deferredFetchPerformanceRecorder{}

func (deferredFetchPerformanceRecorder) Now() float64 { return 0 }

func (deferredFetchPerformanceRecorder) DurationSince(float64) float64 { return 0 }

func (deferredFetchPerformanceRecorder) SetFetchConcurrency(int) error { return nil }

func (deferredFetchPerformanceRecorder) RecordSpan(collectors.PerformanceSpan) error {
	return nil
}

type fetchCommandOptions struct {
	pack        packOptionDefaults
	concurrency int
	output      string
	hasOutput   bool
	resources   []string
	tenant      string
	hasTenant   bool
}

// fetchCLIOptions ports fetchCliOptions from the original implementation. Fetch uses
// the command's historical `||` environment semantics for pack root/profile;
// FETCH_CONCURRENCY is a Make variable forwarded as --concurrency, not a CLI
// environment input.
func fetchCLIOptions(parsed commandInput, requireTenant bool) (fetchCommandOptions, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return fetchCommandOptions{}, err
	}
	concurrency := 1
	if value, ok := lastCommandOption(parsed, "--concurrency"); ok {
		if !positiveFetchConcurrency.MatchString(value) {
			return fetchCommandOptions{}, usageError("--concurrency must be a positive integer")
		}
		parsedValue, parseErr := strconv.ParseUint(value, 10, 64)
		if parseErr != nil || parsedValue > collectors.MaxFetchConcurrency {
			return fetchCommandOptions{}, usageError(
				fmt.Sprintf("--concurrency must not exceed %d", collectors.MaxFetchConcurrency),
			)
		}
		concurrency = int(parsedValue)
	}

	tenant, hasTenant := lastCommandOption(parsed, "--tenant")
	if requireTenant && !hasTenant {
		return fetchCommandOptions{}, usageError("fetch requires --tenant")
	}
	if hasTenant {
		if err := roots.ValidateTenant(tenant); err != nil {
			return fetchCommandOptions{}, usageError(err.Error())
		}
	}
	output, hasOutput := lastCommandOption(parsed, "--out")
	return fetchCommandOptions{
		pack:        resolvePackOptions(rootDirectory, parsed),
		concurrency: concurrency,
		output:      output,
		hasOutput:   hasOutput,
		resources:   append([]string(nil), parsed.Options["--resource"]...),
		tenant:      tenant,
		hasTenant:   hasTenant,
	}, nil
}

// requireBuiltInCollectorAuthority ports the same-named CLI helper from
// the original implementation. Every resolver failure is a usage error and therefore
// must happen before credentials, CA files, transport setup, or output writes.
func requireBuiltInCollectorAuthority(
	root metadata.LoadedPackRoot,
	resourceTypes []string,
) (map[string]collectors.CollectorAdapter, error) {
	adapters, err := collectors.ResolveCollectorAdapters(collectors.ResolveCollectorAdaptersOptions{
		Authorities: collectors.CollectorAdapterAuthorities{
			ByProviderSource: collectors.CreateZscalerCollectorAdaptersByProviderSource(),
		},
		ResourceTypes: resourceTypes,
		Root:          root,
	})
	if err != nil {
		return nil, usageError(err.Error())
	}
	return adapters, nil
}

func selectedFetchProducts(
	root metadata.LoadedPackRoot,
	resourceTypes []string,
) (map[string]struct{}, error) {
	products := make(map[string]struct{})
	for _, resourceType := range resourceTypes {
		resource, ok := root.Resources[resourceType]
		if !ok {
			return nil, fmt.Errorf("unknown active resource %s", resourceType)
		}
		products[resource.Product] = struct{}{}
	}
	return products, nil
}

// fetchWithOwnedTransport preserves the Node finally block's error precedence:
// Fetch failure is primary; Close failure matters only after a successful run.
func fetchWithOwnedTransport(
	transport collectors.HttpTransport,
	options collectors.FetchResourcesOptions,
) (result collectors.FetchRunResult, err error) {
	defer func() {
		closeErr := transport.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	options.Transport = transport
	return collectors.FetchResources(options)
}

// deferredProbeTransport lets ProbeRestHost remain the sole authority for
// diagnostic-target validation while preserving the Node construction order:
// hostUrl(host) runs before createRestHttpTransport(...). Setup failures are
// retained separately because ProbeRestHost deliberately turns request errors
// into a FAIL result, whereas Node transport-construction failures escape the
// probe and remain fatal.
type deferredProbeTransport struct {
	environment     collectors.Environment
	includeCustomCA bool
	timeoutMs       int

	setupDone bool
	setupErr  error
	transport collectors.HttpTransport
}

var _ collectors.HttpTransport = (*deferredProbeTransport)(nil)

func (transport *deferredProbeTransport) Request(
	request collectors.HTTPRequest,
) (collectors.HTTPResponse, error) {
	if !transport.setupDone {
		transport.setupDone = true
		transport.transport, transport.setupErr = httptransport.New(
			transport.environment,
			httptransport.Options{
				IncludeCustomCA:  &transport.includeCustomCA,
				RequestTimeoutMs: &transport.timeoutMs,
			},
		)
	}
	if transport.setupErr != nil {
		return collectors.HTTPResponse{}, transport.setupErr
	}
	return transport.transport.Request(request)
}

func (transport *deferredProbeTransport) Close() error {
	if transport.transport == nil {
		return nil
	}
	return transport.transport.Close()
}

func fetchCommand(
	arguments []string,
	performance collectors.PerformanceRecorder,
) (int, error) {
	return executeStandaloneCobra(newFetchCobraCommand(performance), arguments)
}

func newFetchCobraCommand(performance collectors.PerformanceRecorder) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "fetch", short: "Fetch provider resources",
		valueFlags:       []string{"--tenant", "--resource", "--out", "--concurrency", "--root", "--profile"},
		rejectDuplicates: []string{"--concurrency"},
		run: func(parsed commandInput) (int, error) {
			if len(parsed.Positionals) != 0 {
				return 0, usageError("fetch does not accept positional arguments")
			}
			return fetchCommandInput(parsed, performance)
		},
	})
}

func fetchCommandInput(parsed commandInput, performance collectors.PerformanceRecorder) (int, error) {
	options, err := fetchCLIOptions(parsed, true)
	if err != nil {
		return 0, err
	}
	if !options.hasTenant {
		return 0, usageError("fetch requires --tenant")
	}
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   options.pack.root,
		ProfilePath: &options.pack.profile,
	})
	if err != nil {
		return 0, err
	}
	selected, err := collectors.SelectFetchResources(collectors.SelectFetchResourcesOptions{
		Root:      root,
		Selectors: options.resources,
	})
	if err != nil {
		return 0, usageError(err.Error())
	}
	products, err := selectedFetchProducts(root, selected)
	if err != nil {
		return 0, err
	}
	adapters, err := requireBuiltInCollectorAuthority(root, selected)
	if err != nil {
		return 0, err
	}
	environment := collectors.Environment(environMap())
	mode := collectors.CollectorAuthModeFromEnvironment(environment)
	context, err := collectors.NewCollectorContext(collectors.NewCollectorContextInput{
		Environment:    environment,
		NeededProducts: products,
		Mode:           mode,
	})
	if err != nil {
		return 0, err
	}
	debugLines, err := collectors.FetchDebugLines(collectors.FetchDebugLinesInput{
		Environment: environment,
		Context:     context,
		Mode:        mode,
		Products:    products,
	})
	if err != nil {
		return 0, err
	}
	for _, line := range debugLines {
		fmt.Fprintf(os.Stderr, "%s\n", line)
	}
	transport, err := httptransport.New(environment, httptransport.Options{})
	if err != nil {
		return 0, err
	}
	outputDirectory := options.output
	if !options.hasOutput {
		outputDirectory = filepath.Join("pulls", options.tenant)
	}
	concurrency := options.concurrency
	result, err := fetchWithOwnedTransport(transport, collectors.FetchResourcesOptions{
		Adapters:    adapters,
		Concurrency: &concurrency,
		Context:     context,
		Environment: environment,
		Mode:        mode,
		OnDiagnostic: func(message string) {
			fmt.Fprintf(os.Stderr, "%s\n", message)
		},
		OutputDirectory: outputDirectory,
		Performance:     performance,
		Root:            root,
		Selectors:       options.resources,
	})
	if err != nil {
		return 0, err
	}
	if len(result.Failed) != 0 {
		return 1, nil
	}
	return 0, nil
}

func probeRestHostWithOwnedTransport(
	host string,
	environment collectors.Environment,
	includeCustomCA bool,
) (collectors.RestHostProbeResult, error) {
	timeoutMs := fetchDiagnosticTimeoutMs
	transport := &deferredProbeTransport{
		environment:     environment,
		includeCustomCA: includeCustomCA,
		timeoutMs:       timeoutMs,
	}
	defer func() {
		// Node treats diagnostic-transport cleanup as best effort so the
		// connectivity result remains authoritative.
		_ = transport.Close()
	}()
	result, err := collectors.ProbeRestHost(host, collectors.RestHostProbeOptions{
		TimeoutMs: timeoutMs,
		Transport: transport,
	})
	if err != nil {
		return collectors.RestHostProbeResult{}, err
	}
	if transport.setupErr != nil {
		return collectors.RestHostProbeResult{}, transport.setupErr
	}
	return result, nil
}

func fetchDiagCommand(arguments []string) (int, error) {
	return executeStandaloneCobra(newFetchDiagCobraCommand(), arguments)
}

func newFetchDiagCobraCommand() *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "fetch-diag", short: "Diagnose fetch TLS connectivity",
		valueFlags: []string{"--root", "--profile"},
		run: func(parsed commandInput) (int, error) {
			if len(parsed.Positionals) != 0 {
				return 0, usageError("fetch-diag does not accept positional arguments")
			}
			return fetchDiagCommandInput(parsed)
		},
	})
}

func fetchDiagCommandInput(parsed commandInput) (int, error) {
	options, err := fetchCLIOptions(parsed, false)
	if err != nil {
		return 0, err
	}
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   options.pack.root,
		ProfilePath: &options.pack.profile,
	})
	if err != nil {
		return 0, err
	}
	selected, err := collectors.SelectFetchResources(collectors.SelectFetchResourcesOptions{
		Root: root,
	})
	if err != nil {
		return 0, err
	}
	products, err := selectedFetchProducts(root, selected)
	if err != nil {
		return 0, err
	}
	if _, err := requireBuiltInCollectorAuthority(root, selected); err != nil {
		return 0, err
	}
	environment := collectors.Environment(environMap())
	bundle := environment["REQUESTS_CA_BUNDLE"]
	if bundle == "" {
		bundle = environment["SSL_CERT_FILE"]
	}
	hosts, err := collectors.DiagnosticHosts(environment, products)
	if err != nil {
		return 0, err
	}
	for _, host := range hosts {
		maskedHost := collectors.MaskCollectorIdentifiers(host)
		if strings.Contains(host, "<") {
			fmt.Fprintf(os.Stderr, "%s: skipped (env vars not set)\n", maskedHost)
			continue
		}
		system, err := probeRestHostWithOwnedTransport(host, environment, false)
		if err != nil {
			return 0, err
		}
		line := fmt.Sprintf(
			"%s: system-trust %s (%s)",
			maskedHost,
			probeStatus(system.OK),
			collectors.MaskCollectorIdentifiers(system.Detail),
		)
		if bundle == "" {
			line += "; no CA bundle configured (set REQUESTS_CA_BUNDLE)"
		} else {
			custom, err := probeRestHostWithOwnedTransport(host, environment, true)
			if err != nil {
				return 0, err
			}
			line += fmt.Sprintf(
				"; +bundle %s (%s)",
				probeStatus(custom.OK),
				collectors.MaskCollectorIdentifiers(custom.Detail),
			)
		}
		fmt.Fprintf(os.Stderr, "%s\n", line)
	}
	return 0, nil
}

func probeStatus(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}
