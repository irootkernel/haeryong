// Haeryong is the headless local CLI and foreground daemon. Product API, browser
// sessions and OS service installation have separate implementing Tasks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/adapters/localcontrol"
	"haeryong.local/haeryong/internal/application/service"
)

type options struct {
	selection    service.Selection
	port         int
	portSelected bool
	endpoint     string
	json         bool
	command      []string
}
type commandError struct {
	code int
	err  error
}

func (e commandError) Error() string { return e.err.Error() }
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
	opt := options{selection: service.Selection{Profile: "default"}, port: service.DefaultPort}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			opt.command = append(opt.command, arg)
			continue
		}
		key, value, equals := strings.Cut(arg, "=")
		if key == "--json" || key == "--no-input" {
			if equals {
				return opt, invalid("boolean flags do not accept a value")
			}
			if key == "--json" {
				opt.json = true
			}
			continue
		}
		switch key {
		case "--profile", "--data-dir", "--volume-root", "--port", "--endpoint":
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
		if value == "" {
			return opt, invalid("empty flag value: " + key)
		}
		switch key {
		case "--profile":
			opt.selection.Profile = value
		case "--data-dir":
			opt.selection.DataDir = value
		case "--volume-root":
			opt.selection.VolumeRoot = value
		case "--endpoint":
			opt.endpoint = value
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
	if opt.endpoint != "" && (command == "init" || command == "daemon run") {
		return invalid("initialization and daemon startup require local profile settings")
	}
	switch command {
	case "version":
		return emit(map[string]string{"binary": "development", "local_control_api": service.APIVersion, "product_api": "not_implemented", "package": "not_implemented", "skill": "not_implemented"})
	case "init":
		p, err := service.Initialize(opt.selection)
		if err != nil {
			return err
		}
		return emit(map[string]string{"library_id": p.LibraryID, "profile_id": p.ProfileID, "profile": p.Name, "data_dir": p.DataDir})
	case "daemon run":
		var outputErr error
		runner := service.Runner{Selection: opt.selection, Port: opt.port, Storage: service.PendingStorage{}, Endpoint: &localcontrol.HTTP{}}
		err := runner.Run(ctx, func(id service.Identity) { outputErr = emit(id) })
		return errors.Join(err, outputErr)
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
	if opt.json {
		json.NewEncoder(stdout).Encode(envelope{Error: &errorBody{Code: map[int]string{2: "INVALID_INPUT", 3: "DENIED", 4: "CONFLICT", 5: "UNAVAILABLE"}[code], Message: err.Error()}, RequestID: requestID})
	}
	fmt.Fprintln(stderr, err)
	return code
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
