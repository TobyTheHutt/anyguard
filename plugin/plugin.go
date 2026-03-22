// Package plugin exposes anyguard as a golangci-lint module plugin.
package plugin

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/golangci/plugin-module-register/register"
	"github.com/tobythehutt/anyguard/v2/internal/validation"
	"golang.org/x/tools/go/analysis"
)

const (
	// Name is the stable plugin identifier used in golangci-lint custom config.
	Name = validation.AnalyzerName
)

var _ = registerPlugin()

func registerPlugin() int {
	register.Plugin(Name, New)
	return 0
}

// Settings defines the module-plugin configuration accepted from .golangci.yml.
type Settings struct {
	Allowlist string       `json:"allowlist"`
	Roots     rootsSetting `json:"roots"`
	RepoRoot  string       `json:"repo-root"`
}

type rootsSetting []string

func (r *rootsSetting) UnmarshalJSON(data []byte) error {
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*r = normalizeRoots(many)
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*r = splitRootCSV(single)
		return nil
	}

	return fmt.Errorf("roots must be a string or list of strings")
}

// ModulePlugin wires analyzer instances for golangci-lint module plugin loading.
type ModulePlugin struct {
	settings Settings
}

// New constructs the module plugin instance from golangci-lint custom settings.
func New(rawSettings any) (register.LinterPlugin, error) {
	if rawSettings == nil {
		rawSettings = map[string]any{}
	}

	settings, err := register.DecodeSettings[Settings](rawSettings)
	if err != nil {
		return nil, err
	}

	return &ModulePlugin{settings: settings}, nil
}

// BuildAnalyzers creates the analyzer list consumed by golangci-lint.
func (p *ModulePlugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	analyzer := validation.NewAnalyzer()

	if p.settings.Allowlist != "" {
		if err := analyzer.Flags.Set("allowlist", p.settings.Allowlist); err != nil {
			return nil, fmt.Errorf("set allowlist flag: %w", err)
		}
	}

	if len(p.settings.Roots) > 0 {
		if err := analyzer.Flags.Set("roots", strings.Join(p.settings.Roots, ",")); err != nil {
			return nil, fmt.Errorf("set roots flag: %w", err)
		}
	}

	if p.settings.RepoRoot != "" {
		if err := analyzer.Flags.Set("repo-root", p.settings.RepoRoot); err != nil {
			return nil, fmt.Errorf("set repo-root flag: %w", err)
		}
	}

	return []*analysis.Analyzer{analyzer}, nil
}

// GetLoadMode declares the package loading mode required by this analyzer.
func (p *ModulePlugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}

func normalizeRoots(roots []string) []string {
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root != "" {
			normalized = append(normalized, root)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func splitRootCSV(value string) []string {
	return normalizeRoots(strings.Split(value, ","))
}
