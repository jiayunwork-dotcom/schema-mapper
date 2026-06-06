package migration

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/schema-mapper/schema-mapper/pkg/converter"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

type ApplyResult struct {
	TotalRecords   int64
	SuccessRecords int64
	FailedRecords  int64
	Duration       time.Duration
	OutputPath     string
	Errors         []string
}

func ApplyMigrationScript(scriptPath, sourceFile, outputFile string, opts converter.ConversionOptions) (*ApplyResult, error) {
	script, err := LoadMigrationScript(scriptPath)
	if err != nil {
		return nil, err
	}

	rules := migrationToMappingRules(script)

	tmpMappingFile, err := ioutil.TempFile("", "migration-mapping-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp mapping file: %w", err)
	}
	defer os.Remove(tmpMappingFile.Name())

	yamlData, err := yaml.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mapping rules: %w", err)
	}
	if _, err := tmpMappingFile.Write(yamlData); err != nil {
		return nil, fmt.Errorf("failed to write temp mapping file: %w", err)
	}
	tmpMappingFile.Close()

	dc := converter.NewDataConverter(nil, opts)
	result, err := dc.ConvertFile(sourceFile, tmpMappingFile.Name(), outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to apply migration: %w", err)
	}

	applyResult := &ApplyResult{
		TotalRecords:   result.TotalRecords,
		SuccessRecords: result.SuccessRecords,
		FailedRecords:  result.FailedRecords,
		Duration:       result.Duration,
		OutputPath:     result.OutputPath,
		Errors:         make([]string, 0),
	}

	for _, e := range result.Errors {
		applyResult.Errors = append(applyResult.Errors,
			fmt.Sprintf("Row %d: %s -> %s: %s", e.RowIndex, e.SourcePath, e.TargetPath, e.Message))
	}

	return applyResult, nil
}

func migrationToMappingRules(script *MigrationScript) *mapper.MappingRules {
	rules := &mapper.MappingRules{
		SourceSchema: script.SchemaName + "@" + script.FromVersion,
		TargetSchema: script.SchemaName + "@" + script.ToVersion,
		Mappings:     make([]*mapper.MappingRule, 0),
	}

	fieldAliases := make(map[string]string)

	for _, op := range script.Operations {
		switch op.Op {
		case OpAddField:
			rule := &mapper.MappingRule{
				Source:      op.FieldPath,
				Target:      op.FieldPath,
				Transform:   mapper.TransformDefault,
				Description: op.Description,
			}
			if op.DefaultValue != nil {
				rule.Default = op.DefaultValue
			}
			rules.Mappings = append(rules.Mappings, rule)

		case OpRemoveField:
			continue

		case OpRenameField:
			rule := &mapper.MappingRule{
				Source:      op.FieldPath,
				Target:      op.NewFieldPath,
				Transform:   mapper.TransformDirect,
				Description: op.Description,
			}
			rules.Mappings = append(rules.Mappings, rule)
			fieldAliases[op.FieldPath] = op.NewFieldPath

		case OpChangeType:
			sourcePath := op.FieldPath
			if alias, ok := fieldAliases[op.FieldPath]; ok {
				sourcePath = alias
			}

			rule := &mapper.MappingRule{
				Source:      sourcePath,
				Target:      sourcePath,
				Transform:   mapper.TransformCast,
				Format:      op.NewType,
				Description: op.Description,
			}
			if op.FallbackValue != nil {
				rule.Default = op.FallbackValue
			}
			rules.Mappings = append(rules.Mappings, rule)

		case OpAddConstraint, OpRemoveConstraint:
			continue
		}
	}

	passthroughFields := collectPassthroughFields(script, fieldAliases)
	for _, field := range passthroughFields {
		target := field
		if alias, ok := fieldAliases[field]; ok {
			target = alias
		}
		exists := false
		for _, r := range rules.Mappings {
			if r.Source == field || r.Target == target {
				exists = true
				break
			}
		}
		if !exists {
			rules.Mappings = append(rules.Mappings, &mapper.MappingRule{
				Source:    field,
				Target:    target,
				Transform: mapper.TransformDirect,
			})
		}
	}

	return rules
}

func collectPassthroughFields(script *MigrationScript, aliases map[string]string) []string {
	fields := make(map[string]bool)

	for _, op := range script.Operations {
		switch op.Op {
		case OpAddField:
			fields[op.FieldPath] = true
		case OpRenameField:
			fields[op.NewFieldPath] = true
		case OpChangeType:
			path := op.FieldPath
			if alias, ok := aliases[op.FieldPath]; ok {
				path = alias
			}
			fields[path] = true
		}
	}

	result := make([]string, 0, len(fields))
	for f := range fields {
		result = append(result, f)
	}
	return result
}

func (r *ApplyResult) Print() {
	fmt.Printf("\n=== Migration Apply Result ===\n")
	fmt.Printf("Total records:   %d\n", r.TotalRecords)
	fmt.Printf("Success:         %d\n", r.SuccessRecords)
	fmt.Printf("Failed:          %d\n", r.FailedRecords)
	fmt.Printf("Duration:        %v\n", r.Duration)
	fmt.Printf("Output file:     %s\n", r.OutputPath)

	if len(r.Errors) > 0 {
		fmt.Printf("\n=== Errors (first %d) ===\n", min(len(r.Errors), 10))
		for i, e := range r.Errors {
			if i >= 10 {
				break
			}
			fmt.Printf("  %s\n", e)
		}
	}
}

func (r *ApplyResult) ToJSON() string {
	b, _ := yaml.Marshal(r)
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func detectFormat(path string) converter.DataFormat {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return converter.FormatJSON
	case ".csv":
		return converter.FormatCSV
	case ".ndjson", ".jsonl":
		return converter.FormatNDJSON
	default:
		return converter.FormatJSON
	}
}
