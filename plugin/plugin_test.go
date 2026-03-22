package plugin

import (
	"reflect"
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

const (
	errNewPlugin           = "new plugin: %v"
	errBuildAnalyzers      = "build analyzers: %v"
	errExpectedOneAnalyzer = "expected one analyzer, got %d"
	flagAllowlist          = "allowlist"
	flagRepoRoot           = "repo-root"
	flagRoots              = "roots"
	valueAnyAllowlist      = "ci/allowlist.yaml"
	valueRepoRoot          = "/repo/root"
	valueRoots             = "./...,pkg/api"
)

func TestInitRegistersPlugin(t *testing.T) {
	newPlugin, err := register.GetPlugin(Name)
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}

	instance, err := newPlugin(nil)
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}
	if instance == nil {
		t.Fatalf("expected plugin instance")
	}
}

func TestNewRejectsUnknownSettings(t *testing.T) {
	_, err := New(map[string]any{"unknown": true})
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestModulePluginBuildAnalyzers(t *testing.T) {
	pluginInstance, err := New(map[string]any{
		flagAllowlist: valueAnyAllowlist,
		flagRepoRoot:  valueRepoRoot,
		flagRoots:     []any{"./...", "pkg/api"},
	})
	if err != nil {
		t.Fatalf(errNewPlugin, err)
	}

	analyzers, err := pluginInstance.BuildAnalyzers()
	if err != nil {
		t.Fatalf(errBuildAnalyzers, err)
	}
	if len(analyzers) != 1 {
		t.Fatalf(errExpectedOneAnalyzer, len(analyzers))
	}

	analyzer := analyzers[0]
	if got, want := analyzer.Name, Name; got != want {
		t.Fatalf("unexpected analyzer name: got %q want %q", got, want)
	}

	if got, want := analyzer.Flags.Lookup(flagAllowlist).Value.String(), valueAnyAllowlist; got != want {
		t.Fatalf("allowlist flag mismatch: got %q want %q", got, want)
	}
	if got, want := analyzer.Flags.Lookup(flagRepoRoot).Value.String(), valueRepoRoot; got != want {
		t.Fatalf("repo-root flag mismatch: got %q want %q", got, want)
	}
	if got, want := analyzer.Flags.Lookup(flagRoots).Value.String(), valueRoots; got != want {
		t.Fatalf("roots flag mismatch: got %q want %q", got, want)
	}
}

func TestModulePluginGetLoadMode(t *testing.T) {
	pluginInstance, err := New(nil)
	if err != nil {
		t.Fatalf(errNewPlugin, err)
	}

	if got, want := pluginInstance.GetLoadMode(), register.LoadModeTypesInfo; got != want {
		t.Fatalf("unexpected load mode: got %q want %q", got, want)
	}
}

func TestRootsSettingUnmarshal(t *testing.T) {
	testCases := []struct {
		name string
		raw  any
		want []string
	}{
		{
			name: "list",
			raw:  map[string]any{"roots": []any{"./...", " pkg/api ", ""}},
			want: []string{"./...", "pkg/api"},
		},
		{
			name: "csv",
			raw:  map[string]any{"roots": " ./..., pkg/api "},
			want: []string{"./...", "pkg/api"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pluginInstance, err := New(testCase.raw)
			if err != nil {
				t.Fatalf(errNewPlugin, err)
			}

			modulePlugin, ok := pluginInstance.(*ModulePlugin)
			if !ok {
				t.Fatalf("unexpected plugin type: %T", pluginInstance)
			}
			if got := []string(modulePlugin.settings.Roots); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("roots mismatch: got %v want %v", got, testCase.want)
			}
		})
	}
}
