package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runWithOpts runs the scan the way the root command does, capturing
// the report output in a buffer.
func runWithOpts(t *testing.T, opts *options) (int, string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	code, err := run(cmd, opts)
	return code, buf.String(), err
}

func defaultOpts(path string) *options {
	return &options{path: path, workers: 2, minSizeText: "1"}
}

func TestRunExitCodeNoDuplicates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "one")
	writeFile(t, filepath.Join(root, "b.txt"), "two!")

	code, out, err := runWithOpts(t, defaultOpts(root))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "No duplicate files found.") {
		t.Errorf("output missing the no-duplicates line:\n%s", out)
	}
}

func TestRunExitCodeDuplicatesFound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello world")
	writeFile(t, filepath.Join(root, "b.txt"), "hello world")

	code, out, err := runWithOpts(t, defaultOpts(root))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out, "Duplicate groups:    1") {
		t.Errorf("output missing the group summary:\n%s", out)
	}
}

func TestRunJSONOutput(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello world")
	writeFile(t, filepath.Join(root, "b.txt"), "hello world")

	opts := defaultOpts(root)
	opts.json = true
	code, out, err := runWithOpts(t, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	var report Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if report.GroupCount != 1 || report.TotalWastedBytes != 11 {
		t.Errorf("JSON report = %+v", report)
	}
}

func TestRunUsageErrorsAreCode2(t *testing.T) {
	cases := []struct {
		name string
		opts *options
	}{
		{"bad min-size", &options{path: t.TempDir(), workers: 2, minSizeText: "lots"}},
		{"zero workers", &options{path: t.TempDir(), workers: 0, minSizeText: "1"}},
		{"missing root", &options{path: filepath.Join(t.TempDir(), "nope"), workers: 2, minSizeText: "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runWithOpts(t, tc.opts)
			var exitErr *exitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("err = %v, want an *exitError", err)
			}
			if exitErr.code != 2 {
				t.Errorf("exit code = %d, want 2", exitErr.code)
			}
		})
	}
}

func TestRunRootIsAFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plain.txt")
	writeFile(t, file, "content")

	_, _, err := runWithOpts(t, defaultOpts(file))
	var exitErr *exitError
	if !errors.As(err, &exitErr) || exitErr.code != 2 {
		t.Errorf("err = %v, want *exitError with code 2", err)
	}
}

func TestRunOutputFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello world")
	writeFile(t, filepath.Join(root, "b.txt"), "hello world")

	outFile := filepath.Join(t.TempDir(), "report.txt")
	opts := defaultOpts(root)
	opts.output = outFile
	code, _, err := runWithOpts(t, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	dataBytes, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read report file: %v", err)
	}
	data := string(dataBytes)
	if !strings.Contains(data, "Duplicate finder report") {
		t.Errorf("report file missing the report:\n%s", data)
	}
}

// TestExecuteClassifiesErrors drives the real root command: usage
// problems (unknown flag, positional arguments) must surface as
// errors that are not *exitError, so execute() maps them to exit 2
// with a usage dump, while --help succeeds cleanly.
func TestExecuteClassifiesErrors(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"extra-positional"}} {
		cmd := newRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		_, err := cmd.ExecuteC()
		if err == nil {
			t.Fatalf("args %v: expected an error, got nil", args)
		}
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			t.Errorf("args %v: err = %v, must not be an *exitError (it is a usage error)", args, err)
		}
	}

	cmd := newRootCmd()
	var help bytes.Buffer
	cmd.SetOut(&help)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--help"})
	if _, err := cmd.ExecuteC(); err != nil {
		t.Fatalf("--help: unexpected error %v", err)
	}
	if !strings.Contains(help.String(), "Find duplicate files") {
		t.Errorf("--help output missing the tool description:\n%s", help.String())
	}
}
