// Haeryong is the headless local CLI and foreground daemon. OS installation and
// complete result-directory APIs retain their ordered implementing Tasks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	httpconfig "haeryong.local/haeryong/internal/adapters/http/configuration"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	httpsource "haeryong.local/haeryong/internal/adapters/http/source"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
)

type options struct {
	selection       service.Selection
	port            int
	portSelected    bool
	profileSelected bool
	endpoint        string
	json            bool
	command         []string
	flags           map[string]string
	noInput         bool
}
type commandError struct {
	code int
	err  error
}

func (e commandError) Error() string { return e.err.Error() }
func (e commandError) Unwrap() error { return e.err }
func invalid(s string) error         { return commandError{2, errors.New(s)} }

type envelope struct {
	Data      any        `json:"data"`
	Error     *errorBody `json:"error"`
	RequestID string     `json:"request_id"`
}
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func parse(args []string) (options, error) {
	opt := options{selection: service.Selection{Profile: "default"}, port: service.DefaultPort, flags: map[string]string{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			opt.command = append(opt.command, arg)
			continue
		}
		key, value, equals := strings.Cut(arg, "=")
		if key == "--json" || key == "--no-input" || key == "--new-document" || key == "--resolve-link" || key == "--confirm" || key == "--confirmed" || key == "--unadopted" || key == "--include-retained-sources" {
			if equals {
				return opt, invalid("boolean flags do not accept a value")
			}
			if key == "--json" {
				opt.json = true
			} else if key == "--no-input" {
				opt.noInput = true
			} else {
				opt.flags[strings.TrimPrefix(key, "--")] = "true"
			}
			continue
		}
		switch key {
		case "--provider-binary", "--capture", "--other", "--read", "--decision", "--expected-attempt-generation", "--destination", "--metadata-destination", "--records", "--choices", "--index", "--expected-revision", "--flag", "--action", "--if-match", "--if-none-match", "--profile", "--data-dir", "--volume-root", "--port", "--endpoint", "--credential", "--client-dir", "--operation-id", "--project", "--document", "--base-version", "--metadata", "--version", "--path", "--output", "--input", "--state", "--cursor", "--limit", "--directory", "--kind", "--file", "--format", "--expected-config-revision", "--scope", "--name", "--evidence", "--expected-sha256", "--expected-excerpt-file", "--source-id", "--root-path", "--repo-id", "--retention-mode", "--provider-ids", "--plan-id", "--page", "--region", "--coverage-set", "--reference-id", "--query", "--lifecycle", "--versions":
		default:
			return opt, invalid("unsupported flag: " + key)
		}
		if !equals {
			i++
			if i >= len(args) {
				return opt, invalid("flag value required: " + key)
			}
			value = args[i]
		}
		if value == "" && key != "--query" {
			return opt, invalid("empty flag value: " + key)
		}
		switch key {
		case "--profile":
			opt.selection.Profile = value
			opt.profileSelected = true
		case "--data-dir":
			opt.selection.DataDir = value
		case "--volume-root":
			opt.selection.VolumeRoot = value
		case "--endpoint":
			opt.endpoint = value
		default:
			flag := strings.TrimPrefix(key, "--")
			if _, exists := opt.flags[flag]; exists {
				return opt, invalid("duplicate flag: " + key)
			}
			opt.flags[flag] = value
		case "--port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1024 || port > 65535 {
				return opt, invalid("port must be between 1024 and 65535")
			}
			opt.port = port
			opt.portSelected = true
		}
	}
	if len(opt.command) == 0 {
		return opt, invalid("command required: init, daemon run, service status/stop, doctor, status or version")
	}
	return opt, nil
}

func execute(ctx context.Context, opt options, emit func(any) error) error {
	command := strings.Join(opt.command, " ")
	selectedBinary, e := providerBinarySelection(opt)
	if e != nil {
		return e
	}
	if opt.endpoint != "" && (command == "init" || command == "daemon run") {
		return invalid("initialization and daemon startup require local profile settings")
	}
	if len(opt.command) >= 2 && opt.command[0] == "auth" && opt.command[1] == "connect" {
		if e := validateCommandFlags(opt); e != nil {
			return e
		}
		return connectCommand(ctx, opt, emit)
	}
	if len(opt.command) >= 2 && opt.command[0] == "auth" && opt.command[1] == "grant" {
		return commandError{5, errors.New("FEATURE_NOT_READY: workflow credential issuance")}
	}
	if opt.command[0] == "backup" || opt.command[0] == "restore" || opt.command[0] == "export" {
		if e := validateCommandFlags(opt); e != nil {
			return e
		}
		return copyCommand(ctx, opt, emit)
	}
	if isProductCommand(opt) {
		if e := validateCommandFlags(opt); e != nil {
			return e
		}
		return productCommand(ctx, opt, emit)
	}
	if len(opt.command) >= 2 && opt.command[0] == "auth" {
		return bootstrapCommand(ctx, opt, emit, os.Stdin)
	}
	switch command {
	case "version":
		return emit(map[string]string{"binary": "development", "local_control_api": service.APIVersion, "product_api": "query_cli_configuration_v1", "package": "1.0", "skill": "not_implemented"})
	case "init":
		p, err := service.Initialize(opt.selection)
		if err != nil {
			return err
		}
		return emit(map[string]string{"library_id": p.LibraryID, "profile_id": p.ProfileID, "profile": p.Name, "data_dir": p.DataDir})
	case "daemon run":
		var historicalDiagnostic error
		for {
			var outputErr error
			store := foundation.New(nil)
			core := appauth.New(store)
			imports := importer.New(store, core, store, staging.New())
			configurationCore := appconfig.New(store, core, configpath.Resolver{})
			configurationCore.Backup = store.BackupStatus
			queries := query.New(store, core, store)
			copyCore := recovery.New(store, core, store)
			sourceCore := appsource.New(store, core, store, gitreader.New())
			profile, profileErr := service.Load(opt.selection)
			if profileErr != nil {
				return errors.Join(historicalDiagnostic, profileErr)
			}
			defaultClient, profileErr := clientDirectory(options{selection: opt.selection})
			if profileErr != nil {
				return errors.Join(historicalDiagnostic, profileErr)
			}
			profilesRoot, profileErr := service.DefaultProfilesRoot()
			if profileErr != nil {
				return errors.Join(historicalDiagnostic, profileErr)
			}
			sourceCore.Files = sourcefiles.ManagedFiles(profile.DataDir, profilesRoot, filepath.Join(defaultClient, "credential.json"))
			var managementOrigin string
			var externalOrigin string
			renderFacade := newRenderFacade(store, core, store, func() string { return managementOrigin })
			providerDriver := codex.New(store, store, filepath.Join(profile.DataDir, "storage", "provider-spool"))
			providerDriver.Binary = selectedBinary
			configurationCore.ProviderTransportAvailable = true
			queries.ProviderStatus = func(ctx context.Context) domain.ProviderStatus { return providerDriver.Status(ctx, profile.DataDir) }
			runCtx, cancelRun := context.WithCancel(ctx)
			sourceCore.Lifetime = runCtx
			var restart atomic.Bool
			configurationCore.Restart = func() { restart.Store(true); cancelRun() }
			endpoint := &localcontrol.HTTP{Auth: core, Copies: copyCore, BeforeDrain: cancelRun, Product: func(origin string) (http.Handler, error) {
				// Resolve open staged checks left by a crash before serving.
				if err := providerDriver.RecoverSpool(); err != nil {
					return nil, err
				}
				if err := sourceCore.Recover(runCtx); err != nil {
					return nil, err
				}
				managementOrigin = origin
				configurationCore.RuntimeEndpoint = origin
				configurationCore.ListenerOverride = opt.portSelected
				configuration, err := store.Configuration(runCtx)
				if err != nil {
					return nil, err
				}
				externalOrigin = configuredExternalOrigin(configuration.Value)
				return productHandler(core, origin, configuration.Value, productRoutes{httpsource.New(sourceCore), httpimports.New(imports), httpv1.New(queries, renderFacade, func() (string, string) { return managementOrigin, externalOrigin }), httpconfig.New(configurationCore)})
			}}
			runner := service.Runner{Selection: opt.selection, Port: opt.port, Storage: store, Endpoint: endpoint, Work: (work.Dispatcher{Provider: &work.ProviderCoordinator{Store: store, Bytes: store, Driver: providerDriver}, Copies: copyCore, Purge: &work.Purger{Store: store, Bytes: store}, Store: store, Imports: imports, Derived: work.NewMaintenance(store, store)}).Run, CloseDependents: renderFacade.Shutdown, ListenerPort: func(ctx context.Context) (int, error) {
				configuration, e := store.Configuration(ctx)
				if e != nil {
					return 0, e
				}
				parsed, e := appconfig.Decode(strings.NewReader(string(configuration.Value)), "json")
				if e != nil {
					return 0, e
				}
				if e = appconfig.Validate(parsed); e != nil {
					return 0, e
				}
				if opt.portSelected {
					return opt.port, nil
				}
				var value struct {
					Network struct {
						Listener struct {
							Port int `json:"port"`
						} `json:"listener"`
					} `json:"network"`
				}
				if e = json.Unmarshal(configuration.Value, &value); e != nil {
					return 0, e
				}
				return value.Network.Listener.Port, nil
			}}
			err := runner.Run(runCtx, func(id service.Identity) { outputErr = emit(id) })
			cancelRun()
			again, final := daemonRestartResult(ctx, restart.Load(), err, outputErr, &historicalDiagnostic)
			if !again {
				return final
			}
		}
	case "service status", "status", "doctor", "service stop":
		p, err := service.Load(opt.selection)
		if err != nil {
			return err
		}
		id, err := service.ReadDiscovery(p)
		if err != nil {
			return fmt.Errorf("service discovery unavailable; daemon may be stopped: %w", err)
		}
		if opt.endpoint != "" {
			id.Endpoint = opt.endpoint
		}
		if command == "service stop" {
			if err := localcontrol.Stop(ctx, p, id); err != nil {
				return err
			}
			deadline, cancel := context.WithTimeout(ctx, 7*time.Second)
			defer cancel()
			if err := service.WaitStopped(deadline, p); err != nil {
				return err
			}
			return emit(map[string]any{"stopped": true, "instance_id": id.InstanceID})
		}
		actual, err := localcontrol.Verify(ctx, p, id)
		if err != nil {
			return err
		}
		return emit(actual)
	case "service install", "service start":
		return commandError{5, errors.New("OS service installation/start is not implemented; use daemon run in the foreground")}
	default:
		return commandError{5, errors.New("command capability is not implemented: " + command)}
	}
}

// The daemon loop uses this decision only after Runner has joined all owners.
func daemonRestartResult(ctx context.Context, requested bool, runErr, outputErr error, historical *error) (bool, error) {
	if requested && ctx.Err() == nil && outputErr == nil && work.ProviderRestartDiagnostics(runErr) {
		if *historical == nil {
			*historical = runErr
		}
		return true, nil
	}
	if runErr != nil || outputErr != nil {
		return false, errors.Join(*historical, runErr, outputErr)
	}
	if !requested || ctx.Err() != nil {
		return false, *historical
	}
	return true, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	requestID, err := service.NewID()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 5
	}
	opt, err := parse(args)
	emit := func(data any) error {
		// Both modes encode local data safely; --json guarantees the envelope contract.
		if opt.json {
			return json.NewEncoder(stdout).Encode(envelope{Data: data, RequestID: requestID})
		}
		return json.NewEncoder(stdout).Encode(data)
	}
	if err == nil {
		if opt.portSelected && (opt.port < 9000 || opt.port > 9999) {
			fmt.Fprintf(stderr, "Warning: explicitly selected port %d is outside preferred range 9000..9999\n", opt.port)
		}
		err = execute(ctx, opt, emit)
	}
	if err == nil {
		return 0
	}
	code := 5
	var typed commandError
	if errors.As(err, &typed) {
		code = typed.code
	} else if errors.Is(err, service.ErrLocked) {
		code = 4
	} else {
		var op *net.OpError
		if errors.As(err, &op) && errors.Is(err, syscall.EADDRINUSE) {
			code = 4
		}
	}
	var recorded *recordedCommandError
	var data any
	if errors.As(err, &recorded) {
		data = map[string]any{"operation_id": recorded.operation, "original_status": recorded.status}
	}
	var pending interface{ PendingOperationID() string }
	if errors.As(err, &pending) {
		data = map[string]any{"operation_id": pending.PendingOperationID(), "original_status": nil}
	}
	var emitted error
	if opt.json {
		emitted = json.NewEncoder(stdout).Encode(envelope{Data: data, Error: &errorBody{Code: map[int]string{2: "INVALID_INPUT", 3: "DENIED", 4: "CONFLICT", 5: "UNAVAILABLE", 6: "RECONCILIATION_REQUIRED"}[code], Message: err.Error()}, RequestID: requestID})
	}
	_, diagnosticError := fmt.Fprintln(stderr, err)
	if recorded != nil && emitted == nil && diagnosticError == nil && recorded.acknowledge != nil {
		if e := recorded.acknowledge(); e != nil {
			fmt.Fprintln(stderr, e)
			return 5
		}
	}
	return code
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func providerBinarySelection(opt options) (string, error) {
	binary := opt.flags["provider-binary"]
	if binary == "" {
		return "", nil
	}
	if strings.Join(opt.command, " ") != "daemon run" {
		return "", invalid("--provider-binary belongs to local daemon run")
	}
	if !filepath.IsAbs(binary) {
		return "", invalid("--provider-binary requires an absolute path")
	}
	return filepath.Clean(binary), nil
}
