package main

import (
	"bytes"
	"os"
	"testing"
)

// TestExecute verifies successful and failing CLI exit codes.
func TestExecute(t *testing.T) {
	var stderr bytes.Buffer
	if code := execute([]string{"--help"}, &stderr); code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	if code := execute([]string{"unknown"}, &stderr); code != 1 {
		t.Fatalf("error exit code = %d", code)
	}
}

// TestMain verifies that main delegates the computed status to the process exit function.
func TestMain(t *testing.T) {
	originalExit := exit
	originalArgs := os.Args
	defer func() { exit = originalExit; os.Args = originalArgs }()
	os.Args = []string{"proxykube", "--help"}
	called := false
	exit = func(code int) {
		called = true
		if code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	}
	main()
	if !called {
		t.Fatal("exit was not called")
	}
}
