package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("BE_CRASH_TEST") != "1" {
		return
	}
	main()
}

func TestMainExitCode(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(), "BE_CRASH_TEST=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		t.Fatalf("Main exited with error: %v. Stderr: %s, Stdout: %s", err, stderr.String(), stdout.String())
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Specguard version 0.1.0") {
		t.Errorf("Expected output to contain 'Specguard version 0.1.0', got: %q", outStr)
	}
}
