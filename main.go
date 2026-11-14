// Command duplicate-finder finds duplicate files in a folder tree
// and shows how much space the extra copies waste.
//
// Exit codes:
//
//	0 - no duplicate groups were found
//	1 - at least one duplicate group was found
//	2 - usage error (unknown flag, bad value, wrong arguments),
//	    an unusable scan root, or an unwritable report file
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// exitError carries a process exit code out of the run. When err is
// nil the exit is silent (the report already said everything); when
// err is set it is printed plainly to stderr, with no usage dump.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

type options struct {
	path         string
	workers      int
	minSizeText  string
	includeEmpty bool
	json         bool
	output       string
}

func newRootCmd() *cobra.Command {
	opts := &options{}
	cmd := &cobra.Command{
		Use:   "duplicate-finder",
		Short: "Find duplicate files and the space their extra copies waste",
		Long: `Find duplicate files in a folder tree and show how much space
the extra copies waste.

The scan works in two phases. First it walks the tree collecting
file sizes; only files that share a size with another file can
possibly be duplicates. Then it hashes just those candidates with
SHA-256, streaming the contents, and groups the files whose hashes
match. Every hashed file is checked again afterwards, so a file
that changed during the scan is dropped instead of mis-grouped.

The tool is read-only: it never modifies, moves, or deletes
anything it scans. Symlinks are never followed and are counted as
skipped. Zero-byte files are all identical by definition, so they
are skipped unless --include-empty is set.

Exit codes: 0 means no duplicates were found, 1 means at least one
duplicate group was found, 2 means a usage error, an unusable scan
root, or an unwritable report file.`,
		Example: `  duplicate-finder
  duplicate-finder --path ~/Downloads
  duplicate-finder --path /data --min-size 1MiB --json
  duplicate-finder --path ~/Pictures --include-empty --output report.txt`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       "1.0.0",
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := run(cmd, opts)
			if err != nil {
				return err
			}
			if code != 0 {
				return &exitError{code: code}
			}
			return nil
		},
	}

	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.path, "path", ".", "folder tree to scan")
	flags.IntVar(&opts.workers, "workers", workers, "workers for the walk and hash pools")
	flags.StringVar(&opts.minSizeText, "min-size", "1", "smallest file size to consider (plain bytes, or a suffix like 100KB, 1MiB)")
	flags.BoolVar(&opts.includeEmpty, "include-empty", false, "include zero-byte files (all identical) instead of skipping them")
	flags.BoolVar(&opts.json, "json", false, "print the whole report as JSON")
	flags.StringVar(&opts.output, "output", "", "also write the text report to this file")
	return cmd
}

// execute runs the root command and turns its outcome into a process
// exit code. Cobra-level errors (unknown flags, bad values, wrong
// argument counts) are usage errors: the error and the usage text go
// to stderr and the exit code is 2. Run outcomes travel in an
// exitError and never trigger a usage dump.
func execute() int {
	cmd := newRootCmd()
	ranCmd, err := cmd.ExecuteC()
	if err == nil {
		return 0
	}
	var exitErr *exitError
	if errors.As(err, &exitErr) {
		if exitErr.err != nil {
			fmt.Fprintln(os.Stderr, "duplicate-finder:", exitErr.err)
		}
		return exitErr.code
	}
	fmt.Fprintln(os.Stderr, "duplicate-finder:", err)
	if ranCmd != nil {
		fmt.Fprint(os.Stderr, ranCmd.UsageString())
	}
	return 2
}

func main() {
	os.Exit(execute())
}

// run performs the scan and prints the report. It returns the exit
// code for the search outcome (0 or 1); setup and usage problems
// come back as an *exitError carrying code 2.
func run(cmd *cobra.Command, opts *options) (int, error) {
	minSize, err := ParseSize(opts.minSizeText)
	if err != nil {
		return 0, &exitError{code: 2, err: fmt.Errorf("invalid --min-size: %w", err)}
	}
	if opts.workers < 1 {
		return 0, &exitError{code: 2, err: fmt.Errorf("--workers must be at least 1, got %d", opts.workers)}
	}
	info, err := os.Stat(opts.path)
	if err != nil {
		return 0, &exitError{code: 2, err: fmt.Errorf("cannot scan %q: %w", opts.path, err)}
	}
	if !info.IsDir() {
		return 0, &exitError{code: 2, err: fmt.Errorf("cannot scan %q: not a directory", opts.path)}
	}

	report, err := newFinder(opts.workers, minSize, opts.includeEmpty).find(opts.path)
	if err != nil {
		return 0, &exitError{code: 2, err: fmt.Errorf("cannot scan %q: %w", opts.path, err)}
	}

	text := report.renderText()
	if opts.output != "" {
		if err := os.WriteFile(opts.output, []byte(text), 0o644); err != nil {
			return 0, &exitError{code: 2, err: fmt.Errorf("cannot write report to %q: %w", opts.output, err)}
		}
	}

	out := cmd.OutOrStdout()
	if opts.json {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return 0, &exitError{code: 2, err: fmt.Errorf("cannot render the report as JSON: %w", err)}
		}
		fmt.Fprintln(out, string(data))
	} else {
		fmt.Fprint(out, text)
	}

	if report.GroupCount > 0 {
		return 1, nil
	}
	return 0, nil
}
