// Command anyguard enforces the any-usage allowlist in Go source code.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tobythehutt/anyguard/internal/validation"
)

const (
	defaultAllowlistPath = "internal/ci/any_allowlist.yaml"
	defaultRoots         = "./..."
)

var (
	exitFunc     = os.Exit
	getwd        = os.Getwd
	validateFunc = validation.ValidateAnyUsageFromFile
)

func main() {
	exitFunc(run(os.Args, os.Stderr, validateFunc))
}

func run(args []string, stderr io.Writer, validate validatorFn) int {
	if len(args) == 0 {
		return 1
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	allowlist := flags.String("allowlist", defaultAllowlistPath, "path to any usage allowlist (yaml)")
	rootsFlag := flags.String("roots", defaultRoots, "comma-separated roots to scan")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}

	roots := splitRoots(*rootsFlag)
	if len(roots) == 0 {
		_, _ = fmt.Fprintln(stderr, "no roots provided for any usage validation")
		return 1
	}

	baseDir, err := getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return 1
	}

	violations, err := validate(*allowlist, baseDir, roots)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "any usage guard failed: %v\n", err)
		return 1
	}
	if len(violations) == 0 {
		return 0
	}

	if printViolations(stderr, violations) != nil {
		return 1
	}
	return 1
}

type validatorFn func(string, string, []string) ([]validation.Error, error)

func splitRoots(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	rawRoots := strings.Split(value, ",")
	roots := make([]string, 0, len(rawRoots))
	for _, root := range rawRoots {
		root = strings.TrimSpace(root)
		if root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	return roots
}

func printViolations(stderr io.Writer, violations []validation.Error) error {
	if _, err := fmt.Fprintf(stderr, "Found %d disallowed any usages:\n\n", len(violations)); err != nil {
		return err
	}
	for _, violation := range violations {
		if _, err := fmt.Fprintf(stderr, "%s:%d\n", violation.File, violation.Line); err != nil {
			return err
		}
		if violation.Message != "" {
			if _, err := fmt.Fprintf(stderr, "  %s\n", violation.Message); err != nil {
				return err
			}
		}
		if violation.Code != "" {
			if _, err := fmt.Fprintf(stderr, "  Code: %s\n", violation.Code); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(stderr); err != nil {
			return err
		}
	}
	return nil
}
