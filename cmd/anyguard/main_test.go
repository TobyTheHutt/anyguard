package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tobythehutt/anyguard/internal/validation"
)

type runErrorCase struct {
	name      string
	args      []string
	getwdErr  error
	validate  validatorFn
	wantInLog string
}

var runErrorCases = []runErrorCase{
	{
		name:      "flag parse error",
		args:      []string{"cmd", "-badflag"},
		validate:  okValidator,
		wantInLog: "flag provided but not defined",
	},
	{
		name:      "no roots provided",
		args:      []string{"cmd", "-roots="},
		validate:  okValidator,
		wantInLog: "no roots provided",
	},
	{
		name:      "getwd failure",
		args:      []string{"cmd"},
		getwdErr:  errors.New("boom"),
		validate:  okValidator,
		wantInLog: "resolve working directory",
	},
	{
		name:      "validator error",
		args:      []string{"cmd"},
		validate:  errorValidator,
		wantInLog: "any usage guard failed",
	},
	{
		name:      "violations",
		args:      []string{"cmd"},
		validate:  violationValidator,
		wantInLog: "Found 1 disallowed any usages",
	},
}

func TestRunUsesDefaults(t *testing.T) {
	var gotAllowlist string
	var gotRoots []string
	var gotBase string

	exitCode := run([]string{"cmd"}, &bytes.Buffer{}, func(allowlistPath, baseDir string, roots []string) ([]validation.Error, error) {
		gotAllowlist = allowlistPath
		gotRoots = roots
		gotBase = baseDir
		return nil, nil
	})

	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if gotAllowlist != defaultAllowlistPath {
		t.Fatalf("expected allowlist %q, got %q", defaultAllowlistPath, gotAllowlist)
	}
	if strings.Join(gotRoots, ",") != defaultRoots {
		t.Fatalf("expected roots %q, got %q", defaultRoots, strings.Join(gotRoots, ","))
	}
	if gotBase == "" {
		t.Fatalf("expected base directory")
	}
}

func TestMainUsesExitCode(t *testing.T) {
	originalExit := exitFunc
	originalValidate := validateFunc
	originalGetwd := getwd
	originalArgs := os.Args
	t.Cleanup(func() {
		exitFunc = originalExit
		validateFunc = originalValidate
		getwd = originalGetwd
		os.Args = originalArgs
	})

	gotExit := -1
	exitFunc = func(code int) { gotExit = code }
	validateFunc = func(string, string, []string) ([]validation.Error, error) { return nil, nil }
	getwd = func() (string, error) { return t.TempDir(), nil }
	os.Args = []string{"anyguard"}

	main()

	if gotExit != 0 {
		t.Fatalf("expected exit code 0, got %d", gotExit)
	}
}

func TestRunErrorPaths(t *testing.T) {
	for _, testCase := range runErrorCases {
		t.Run(testCase.name, func(t *testing.T) {
			originalGetwd := getwd
			t.Cleanup(func() { getwd = originalGetwd })
			if testCase.getwdErr != nil {
				getwd = func() (string, error) { return "", testCase.getwdErr }
			}

			var stderr bytes.Buffer
			exitCode := run(testCase.args, &stderr, testCase.validate)
			if exitCode != 1 {
				t.Fatalf("expected exit code 1, got %d", exitCode)
			}
			if !strings.Contains(stderr.String(), testCase.wantInLog) {
				t.Fatalf("expected %q in log %q", testCase.wantInLog, stderr.String())
			}
		})
	}
}

func TestRunNoArgs(t *testing.T) {
	exitCode := run([]string{}, &bytes.Buffer{}, okValidator)
	if exitCode != 1 {
		t.Fatalf("expected exit 1, got %d", exitCode)
	}
}

func TestSplitRoots(t *testing.T) {
	roots := splitRoots(" ./..., pkg/api , ,internal ")
	if len(roots) != 3 {
		t.Fatalf("expected 3 roots, got %d", len(roots))
	}
	if roots[0] != "./..." || roots[1] != "pkg/api" || roots[2] != "internal" {
		t.Fatalf("unexpected roots: %v", roots)
	}
	if splitRoots("   ") != nil {
		t.Fatalf("expected nil for blank roots")
	}
}

func okValidator(string, string, []string) ([]validation.Error, error) {
	return nil, nil
}

func errorValidator(string, string, []string) ([]validation.Error, error) {
	return nil, errors.New("validation failed")
}

func violationValidator(string, string, []string) ([]validation.Error, error) {
	return []validation.Error{
		{File: "pkg/sample.go", Line: 3, Message: "disallowed", Code: "var x any"},
	}, nil
}
