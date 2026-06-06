package report

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/schema-mapper/schema-mapper/pkg/mapper"
	"github.com/schema-mapper/schema-mapper/pkg/parser"
)

type CoverageReport struct {
	SourceSchemaName string               `json:"sourceSchema"`
	TargetSchemaName string               `json:"targetSchema"`
	MappingFile      string               `json:"mappingFile"`
	SourceTotal      int                  `json:"sourceTotalFields"`
	SourceMapped     int                  `json:"sourceMappedFields"`
	SourceCoverage   float64              `json:"sourceCoverage"`
	TargetTotal      int                  `json:"targetTotalFields"`
	TargetCovered    int                  `json:"targetCoveredFields"`
	TargetCoverage   float64              `json:"targetCoverage"`
	UnmappedSources  []string             `json:"unmappedSourceFields"`
	UncoveredTargets []string             `json:"uncoveredTargetFields"`
	Warnings         []string             `json:"warnings"`
}

func GenerateCoverageReport(mappingPath string) (*CoverageReport, error) {
	rules, err := mapper.LoadMappingRules(mappingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load mapping rules: %w", err)
	}

	registry := parser.NewParserRegistry()

	report := &CoverageReport{
		MappingFile:     mappingPath,
		UnmappedSources: make([]string, 0),
		UncoveredTargets: make([]string, 0),
		Warnings:        make([]string, 0),
	}

	if rules.SourceSchema != "" {
		sourceSchema, err := registry.ParseFile(rules.SourceSchema, "")
		if err == nil {
			report.SourceSchemaName = sourceSchema.Name
			sourceFields := make(map[string]bool)
			for _, f := range sourceSchema.AllFields() {
				if f.IsLeaf() {
					sourceFields[f.Path] = true
				}
			}
			report.SourceTotal = len(sourceFields)

			mappedSources := make(map[string]bool)
			for _, rule := range rules.Mappings {
				if rule.Source != "" {
					mappedSources[rule.Source] = true
				}
				for _, src := range rule.Sources {
					mappedSources[src] = true
				}
			}
			report.SourceMapped = len(mappedSources)

			for src := range sourceFields {
				if !mappedSources[src] {
					report.UnmappedSources = append(report.UnmappedSources, src)
				}
			}

			if report.SourceTotal > 0 {
				report.SourceCoverage = float64(report.SourceMapped) / float64(report.SourceTotal) * 100
			}
		}
	}

	if rules.TargetSchema != "" {
		targetSchema, err := registry.ParseFile(rules.TargetSchema, "")
		if err == nil {
			report.TargetSchemaName = targetSchema.Name
			targetFields := make(map[string]bool)
			for _, f := range targetSchema.AllFields() {
				if f.IsLeaf() {
					targetFields[f.Path] = true
				}
			}
			report.TargetTotal = len(targetFields)

			coveredTargets := make(map[string]bool)
			for _, rule := range rules.Mappings {
				if rule.Target != "" {
					coveredTargets[rule.Target] = true
				}
				for _, tgt := range rule.Targets {
					coveredTargets[tgt] = true
				}
			}
			report.TargetCovered = len(coveredTargets)

			for tgt := range targetFields {
				if !coveredTargets[tgt] {
					report.UncoveredTargets = append(report.UncoveredTargets, tgt)
				}
			}

			if report.TargetTotal > 0 {
				report.TargetCoverage = float64(report.TargetCovered) / float64(report.TargetTotal) * 100
			}
		}
	}

	for _, rule := range rules.Mappings {
		if rule.NeedsReview {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("Mapping %s -> %s needs manual review (confidence: %.2f)", rule.Source, rule.Target, 0.0))
		}
		if rule.Transform == mapper.TransformCast {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("Type casting: %s -> %s (format: %s)", rule.Source, rule.Target, rule.Format))
		}
	}

	return report, nil
}

func (r *CoverageReport) Print() {
	fmt.Printf("\n=== Mapping Coverage Report\n")
	fmt.Printf("Mapping file: %s\n", r.MappingFile)
	if r.SourceSchemaName != "" {
		fmt.Printf("Source schema: %s\n", r.SourceSchemaName)
	}
	if r.TargetSchemaName != "" {
		fmt.Printf("Target schema: %s\n", r.TargetSchemaName)
	}
	fmt.Println()

	if r.SourceTotal > 0 {
		sourceColor := "\033[32m"
		if r.SourceCoverage < 80 {
			sourceColor = "\033[33m"
		}
		if r.SourceCoverage < 50 {
			sourceColor = "\033[31m"
		}
		fmt.Printf("Source Coverage: %s%.1f%%\033[0m (%d/%d fields mapped)\n",
			sourceColor, r.SourceCoverage, r.SourceMapped, r.SourceTotal)
	}

	if r.TargetTotal > 0 {
		targetColor := "\033[32m"
		if r.TargetCoverage < 80 {
			targetColor = "\033[33m"
		}
		if r.TargetCoverage < 50 {
			targetColor = "\033[31m"
		}
		fmt.Printf("Target Coverage: %s%.1f%%\033[0m (%d/%d fields covered)\n",
			targetColor, r.TargetCoverage, r.TargetCovered, r.TargetTotal)
	}
	fmt.Println()

	if len(r.UnmappedSources) > 0 {
		fmt.Printf("\033[33mUnmapped source fields (%d):\033[0m\n", len(r.UnmappedSources))
		for _, f := range r.UnmappedSources[:min(len(r.UnmappedSources), 10)] {
			fmt.Printf("  - %s\n", f)
		}
		if len(r.UnmappedSources) > 10 {
			fmt.Printf("  ... and %d more\n", len(r.UnmappedSources)-10)
		}
		fmt.Println()
	}

	if len(r.UncoveredTargets) > 0 {
		fmt.Printf("\033[33mUncovered target fields (%d):\033[0m\n", len(r.UncoveredTargets))
		for _, f := range r.UncoveredTargets[:min(len(r.UncoveredTargets), 10)] {
			fmt.Printf("  - %s\n", f)
		}
		if len(r.UncoveredTargets) > 10 {
			fmt.Printf("  ... and %d more\n", len(r.UncoveredTargets)-10)
		}
		fmt.Println()
	}

	if len(r.Warnings) > 0 {
		fmt.Printf("\033[33mWarnings (%d):\033[0m\n", len(r.Warnings))
		for _, w := range r.Warnings[:min(len(r.Warnings), 10)] {
			fmt.Printf("  ⚠ %s\n", w)
		}
		if len(r.Warnings) > 10 {
			fmt.Printf("  ... and %d more\n", len(r.Warnings)-10)
		}
		fmt.Println()
	}

	overall := (r.SourceCoverage + r.TargetCoverage) / 2
	if overall >= 80 {
		fmt.Printf("\033[32m✓ Coverage is good (overall: %.1f%%)\033[0m\n", overall)
	} else if overall >= 50 {
		fmt.Printf("\033[33m⚠ Coverage needs improvement (overall: %.1f%%)\033[0m\n", overall)
	} else {
		fmt.Printf("\033[31m✗ Coverage is low (overall: %.1f%%)\033[0m\n", overall)
	}
}

func (r *CoverageReport) SaveJSON(outputPath string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(outputPath)
	if dir != "" && dir != "." {
		os.MkdirAll(dir, 0755)
	}

	return ioutil.WriteFile(outputPath, b, 0644)
}

func (r *CoverageReport) ToJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
