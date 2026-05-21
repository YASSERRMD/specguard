package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/YASSERRMD/specguard/internal/adapters/grpc"
	"github.com/YASSERRMD/specguard/internal/adapters/rest"
	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/YASSERRMD/specguard/internal/server"
	"github.com/YASSERRMD/specguard/internal/store"
)

const version = "0.1.0"

func main() {
	if os.Getenv("BE_CLI_RUN") == "1" {
		newArgs := []string{os.Args[0]}
		found := false
		for _, arg := range os.Args {
			if found {
				newArgs = append(newArgs, arg)
			} else if arg == "--" {
				found = true
			}
		}
		if found {
			os.Args = newArgs
		}
	}

	if os.Getenv("BE_CRASH_TEST") == "1" {
		fmt.Printf("Specguard version %s\n", version)
		os.Exit(0)
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "version":
		fmt.Printf("Specguard version %s\n", version)
		os.Exit(0)

	case "server":
		runServer()

	case "spec":
		handleSpecCmd()

	case "mock":
		handleMockCmd()

	case "contract":
		handleContractCmd()

	case "report":
		handleReportCmd()

	case "hash":
		handleHashCmd()

	case "diff":
		handleDiffCmd()

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: specguard <command> [args]")
	fmt.Println("Commands:")
	fmt.Println("  server                    Start the Specguard API server")
	fmt.Println("  spec add <id> <file>      Add a new API specification")
	fmt.Println("  spec list                 List all registered API specifications")
	fmt.Println("  mock start <id>           Start a mock server for a spec")
	fmt.Println("  mock stop <id>            Stop a mock server for a spec")
	fmt.Println("  contract run <id> <url>   Run contract checks against a SUT")
	fmt.Println("  report show <run_id>      Show drift report findings")
	fmt.Println("  hash <file>               Generate structural hash of a spec")
	fmt.Println("  diff <spec-a> <spec-b>    Show structural drift between two specs")
	fmt.Println("  version                   Print the CLI version")
}

func runServer() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	configPath := serverCmd.String("config", "", "Path to configuration file")
	_ = serverCmd.Parse(os.Args[2:])

	cfg, err := server.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dbStore, err := store.NewSQLiteStore(cfg.DBDSN)
	if err != nil {
		logger.Error("failed to initialize store", "error", err)
		os.Exit(1)
	}
	defer dbStore.Close()

	srv := server.NewServer(cfg, dbStore, logger)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	logger.Info("server started successfully", "port", cfg.Port)
	<-sigChan

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped gracefully")
	os.Exit(0)
}

func makeRequest(method, path string, body interface{}) ([]byte, int, error) {
	serverAddr := os.Getenv("SPECGUARD_SERVER_URL")
	if serverAddr == "" {
		serverAddr = "http://localhost:8080"
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, serverAddr+path, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	return respBody, resp.StatusCode, nil
}

func handleSpecCmd() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: specguard spec <add|list> [args]")
		os.Exit(1)
	}

	subCmd := os.Args[2]
	switch subCmd {
	case "add":
		if len(os.Args) < 5 {
			fmt.Println("Usage: specguard spec add <id> <file>")
			os.Exit(1)
		}
		id := os.Args[3]
		filePath := os.Args[4]

		content, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			os.Exit(1)
		}

		var payload map[string]interface{}
		var spec core.NormalizedSpec
		if err := json.Unmarshal(content, &spec); err == nil && len(spec.Operations) > 0 {
			payload = map[string]interface{}{
				"id":   id,
				"spec": spec,
			}
		} else {
			payload = map[string]interface{}{
				"id":  id,
				"raw": string(content),
			}
		}

		resp, status, err := makeRequest("POST", "/api/specs", payload)
		if err != nil {
			fmt.Printf("API request failed: %v\n", err)
			os.Exit(1)
		}

		if status != http.StatusCreated {
			fmt.Printf("Error: %s (status code %d)\n", string(resp), status)
			os.Exit(1)
		}

		fmt.Printf("Specification %q added successfully.\n", id)

	case "list":
		resp, status, err := makeRequest("GET", "/api/specs", nil)
		if err != nil {
			fmt.Printf("API request failed: %v\n", err)
			os.Exit(1)
		}

		if status != http.StatusOK {
			fmt.Printf("Error: %s\n", string(resp))
			os.Exit(1)
		}

		var specs []string
		_ = json.Unmarshal(resp, &specs)
		fmt.Println("Registered Specifications:")
		for _, s := range specs {
			fmt.Printf("  - %s\n", s)
		}

	default:
		fmt.Println("Unknown spec subcommand. Use 'add' or 'list'.")
		os.Exit(1)
	}
}

func handleMockCmd() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: specguard mock <start|stop> <id>")
		os.Exit(1)
	}

	subCmd := os.Args[2]
	id := os.Args[3]

	var path string
	if subCmd == "start" {
		path = "/api/mocks/start"
	} else if subCmd == "stop" {
		path = "/api/mocks/stop"
	} else {
		fmt.Println("Unknown mock subcommand. Use 'start' or 'stop'.")
		os.Exit(1)
	}

	payload := map[string]string{"id": id}
	resp, status, err := makeRequest("POST", path, payload)
	if err != nil {
		fmt.Printf("API request failed: %v\n", err)
		os.Exit(1)
	}

	if status == http.StatusNotImplemented {
		var errResp map[string]string
		_ = json.Unmarshal(resp, &errResp)
		fmt.Printf("Not Implemented: %s\n", errResp["error"])
		os.Exit(0) // Exit 0 for Phase 4 stubs as specified
	}

	fmt.Printf("Status: %d, Response: %s\n", status, string(resp))
}

func handleContractCmd() {
	if len(os.Args) < 5 {
		fmt.Println("Usage: specguard contract run <id> <url>")
		os.Exit(1)
	}

	subCmd := os.Args[2]
	if subCmd != "run" {
		fmt.Println("Unknown contract subcommand. Use 'run'.")
		os.Exit(1)
	}

	id := os.Args[3]
	targetURL := os.Args[4]

	payload := map[string]string{"id": id, "target_url": targetURL}
	resp, status, err := makeRequest("POST", "/api/contract/run", payload)
	if err != nil {
		fmt.Printf("API request failed: %v\n", err)
		os.Exit(1)
	}

	if status == http.StatusNotImplemented {
		var errResp map[string]string
		_ = json.Unmarshal(resp, &errResp)
		fmt.Printf("Not Implemented: %s\n", errResp["error"])
		os.Exit(0) // Exit 0 for Phase 4 stubs as specified
	}

	fmt.Printf("Status: %d, Response: %s\n", status, string(resp))
}

func handleReportCmd() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: specguard report show <run_id>")
		os.Exit(1)
	}

	subCmd := os.Args[2]
	if subCmd != "show" {
		fmt.Println("Unknown report subcommand. Use 'show'.")
		os.Exit(1)
	}

	runID := os.Args[3]

	resp, status, err := makeRequest("GET", "/api/reports/"+runID, nil)
	if err != nil {
		fmt.Printf("API request failed: %v\n", err)
		os.Exit(1)
	}

	if status == http.StatusNotFound {
		fmt.Printf("Report not found for run %q.\n", runID)
		os.Exit(0)
	}

	fmt.Printf("Report (Status %d):\n%s\n", status, string(resp))
}

func loadSpecFromFile(filePath string) (*core.NormalizedSpec, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var spec core.NormalizedSpec
	if err := json.Unmarshal(content, &spec); err == nil && len(spec.Operations) > 0 {
		return &spec, nil
	}

	isProto := strings.HasSuffix(filePath, ".proto") || strings.Contains(string(content), "syntax = \"proto") || strings.Contains(string(content), "syntax = 'proto") || strings.Contains(string(content), "service ")
	if isProto {
		adapter := grpc.NewAdapter()
		return adapter.LoadSpec(content)
	}

	adapter := rest.NewAdapter()
	return adapter.LoadSpec(content)
}

func handleHashCmd() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: specguard hash <spec-file>")
		os.Exit(1)
	}
	filePath := os.Args[2]
	spec, err := loadSpecFromFile(filePath)
	if err != nil {
		fmt.Printf("Error loading specification: %v\n", err)
		os.Exit(1)
	}
	hashVal, err := core.HashSpec(spec)
	if err != nil {
		fmt.Printf("Error computing spec hash: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(hashVal)
}

func handleDiffCmd() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: specguard diff <spec-a> <spec-b>")
		os.Exit(1)
	}
	fileA := os.Args[2]
	fileB := os.Args[3]
	specA, err := loadSpecFromFile(fileA)
	if err != nil {
		fmt.Printf("Error loading specification A: %v\n", err)
		os.Exit(1)
	}
	specB, err := loadSpecFromFile(fileB)
	if err != nil {
		fmt.Printf("Error loading specification B: %v\n", err)
		os.Exit(1)
	}
	report, err := core.DiffSpecs(specA, specB)
	if err != nil {
		fmt.Printf("Error computing spec diff: %v\n", err)
		os.Exit(1)
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Printf("Error serializing drift report: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
