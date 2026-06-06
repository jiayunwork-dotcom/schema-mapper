package migration

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"
	"time"

	"github.com/schema-mapper/schema-mapper/pkg/converter"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

func DryRunMigration(planPath, sourceFile string) (*DryRunReport, error) {
	plan, err := LoadMigrationPlan(planPath)
	if err != nil {
		return nil, err
	}

	records, err := loadAllRecords(sourceFile)
	if err != nil {
		return nil, err
	}

	totalRecords := int64(len(records))

	report := &DryRunReport{
		PlanPath:                planPath,
		SourceFile:              sourceFile,
		TotalRecords:            totalRecords,
		SkippedFieldStats:       make(map[string]int64),
		MissingFallbackFailures: 0,
		OperationResults:        make([]*OperationDryRunStats, 0, len(plan.Operations)),
		CreatedAt:               time.Now().Format(time.RFC3339),
	}

	fieldAliases := make(map[string]string)
	fieldValuesSnapshot := make(map[string]interface{})

	for opIdx, op := range plan.Operations {
		opStats := &OperationDryRunStats{
			StepIndex:      opIdx + 1,
			Operation:      op,
			TotalRecords:   totalRecords,
			FailureReasons: make(map[string]int),
			TopFailures:    make([]*FailureReason, 0),
		}

		successCount := int64(0)
		failedCount := int64(0)
		skippedCount := int64(0)

		for recIdx, record := range records {
			recordMap, ok := record.(map[string]interface{})
			if !ok {
				failedCount++
				opStats.FailureReasons["invalid record format"]++
				continue
			}

			result, err := applyOperationDryRun(op, recordMap, fieldAliases, fieldValuesSnapshot)
			if err != nil {
				failedCount++
				errMsg := err.Error()
				opStats.FailureReasons[errMsg]++

				if strings.Contains(errMsg, "fallback") || strings.Contains(errMsg, "fallback_value") {
					report.MissingFallbackFailures++
				}
				continue
			}

			if result.skipped {
				skippedCount++
				report.SkippedFieldStats[result.skippedField]++
				continue
			}

			successCount++

			if recIdx < 10 {
				updateFieldAliases(op, fieldAliases)
			}
		}

		opStats.SuccessRecords = successCount
		opStats.FailedRecords = failedCount
		opStats.SkippedFields = skippedCount

		if totalRecords > 0 {
			opStats.PassRate = float64(successCount) / float64(totalRecords)
		}

		opStats.TopFailures = getTopFailures(opStats.FailureReasons, 3)
		report.OperationResults = append(report.OperationResults, opStats)
	}

	report.SuccessRecords = calculateOverallSuccess(report.OperationResults, totalRecords)
	report.FailedRecords = totalRecords - report.SuccessRecords

	report.Recommendations = generateSuggestions(report.OperationResults)

	return report, nil
}

type dryRunResult struct {
	skipped      bool
	skippedField string
}

func applyOperationDryRun(op *MigrationOperation, record map[string]interface{}, aliases map[string]string, snapshot map[string]interface{}) (*dryRunResult, error) {
	fieldPath := op.FieldPath
	if alias, ok := aliases[fieldPath]; ok {
		fieldPath = alias
	}

	val, exists := mapper.GetNestedValue(record, fieldPath)

	switch op.Op {
	case OpAddField:
		if exists && val != nil {
			return &dryRunResult{}, nil
		}
		if op.DefaultValue == nil {
			return nil, fmt.Errorf("field %s is NOT NULL and no default_value provided", fieldPath)
		}
		return &dryRunResult{}, nil

	case OpRemoveField:
		if !exists {
			return &dryRunResult{skipped: true, skippedField: fieldPath}, nil
		}
		snapshot[fieldPath] = val
		return &dryRunResult{}, nil

	case OpRenameField:
		if !exists {
			return &dryRunResult{skipped: true, skippedField: fieldPath}, nil
		}
		return &dryRunResult{}, nil

	case OpChangeType:
		if !exists {
			return &dryRunResult{skipped: true, skippedField: fieldPath}, nil
		}
		if val == nil {
			return &dryRunResult{}, nil
		}

		_, err := castValueDryRun(val, op.NewType)
		if err != nil {
			if op.FallbackValue == nil {
				return nil, fmt.Errorf("type conversion failed for field %s: %v (no fallback_value provided)", fieldPath, err)
			}
		}
		return &dryRunResult{}, nil

	case OpAddConstraint, OpRemoveConstraint:
		return &dryRunResult{}, nil

	default:
		return nil, fmt.Errorf("unknown operation type: %s", op.Op)
	}
}

func castValueDryRun(val interface{}, targetType string) (interface{}, error) {
	strVal := fmt.Sprintf("%v", val)
	switch strings.ToLower(targetType) {
	case "int", "int32", "integer", "int64", "long":
		_, err := parseInt64(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to %s", strVal, targetType)
		}
	case "float", "float32", "float64", "double":
		_, err := parseFloat64(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to %s", strVal, targetType)
		}
	case "bool", "boolean":
		_, err := parseBool(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to %s", strVal, targetType)
		}
	case "string", "str", "date", "datetime", "timestamp":
		return strVal, nil
	default:
		return val, nil
	}
	return val, nil
}

func parseInt64(s string) (int64, error) {
	var result int64
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}

func parseFloat64(s string) (float64, error) {
	var result float64
	_, err := fmt.Sscanf(s, "%f", &result)
	return result, err
}

func parseBool(s string) (bool, error) {
	s = strings.ToLower(s)
	switch s {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value: %s", s)
	}
}

func findFieldInRecord(record map[string]interface{}, path string) interface{} {
	val, ok := mapper.GetNestedValue(record, path)
	if !ok {
		return nil
	}
	return val
}

func isNullableField(field interface{}) bool {
	if field == nil {
		return true
	}
	return true
}

func updateFieldAliases(op *MigrationOperation, aliases map[string]string) {
	if op.Op == OpRenameField {
		aliases[op.FieldPath] = op.NewFieldPath
		for k, v := range aliases {
			if v == op.FieldPath {
				aliases[k] = op.NewFieldPath
			}
		}
	}
}

func getTopFailures(reasons map[string]int, topN int) []*FailureReason {
	if len(reasons) == 0 {
		return []*FailureReason{}
	}

	type kv struct {
		Reason string
		Count  int
	}

	list := make([]kv, 0, len(reasons))
	for k, v := range reasons {
		list = append(list, kv{k, v})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Count > list[j].Count
	})

	result := make([]*FailureReason, 0, min(topN, len(list)))
	for i := 0; i < min(topN, len(list)); i++ {
		result = append(result, &FailureReason{
			Reason: list[i].Reason,
			Count:  list[i].Count,
		})
	}

	return result
}

func calculateOverallSuccess(stats []*OperationDryRunStats, totalRecords int64) int64 {
	if len(stats) == 0 {
		return totalRecords
	}

	minSuccess := totalRecords
	for _, s := range stats {
		if s.SuccessRecords < minSuccess {
			minSuccess = s.SuccessRecords
		}
	}
	return minSuccess
}

func generateSuggestions(stats []*OperationDryRunStats) []*Suggestion {
	suggestions := make([]*Suggestion, 0)

	for _, s := range stats {
		if s.Operation.RiskLevel != RiskHigh {
			continue
		}

		failureRate := 1.0 - s.PassRate
		if failureRate <= 0.05 {
			continue
		}

		topReason := ""
		if len(s.TopFailures) > 0 {
			topReason = s.TopFailures[0].Reason
		}

		action := ""
		if s.Operation.Op == OpChangeType {
			action = fmt.Sprintf("Provide fallback_value for field %s or adjust type conversion strategy", s.Operation.FieldPath)
		} else if s.Operation.Op == OpAddField {
			action = fmt.Sprintf("Provide default_value for new field %s", s.Operation.FieldPath)
		} else {
			action = fmt.Sprintf("Review operation %s on field %s", s.Operation.Op, s.Operation.FieldPath)
		}

		suggestions = append(suggestions, &Suggestion{
			StepIndex:   s.StepIndex,
			FailureRate: failureRate,
			TopReason:   topReason,
			Action:      action,
		})
	}

	return suggestions
}

func loadAllRecords(dataFile string) ([]interface{}, error) {
	format := converter.DetectFormat(dataFile)

	content, err := ioutil.ReadFile(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read data file: %w", err)
	}

	var records []interface{}

	switch format {
	case converter.FormatJSON:
		var data interface{}
		if err := json.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
		if arr, ok := data.([]interface{}); ok {
			records = arr
		} else {
			records = []interface{}{data}
		}
	case converter.FormatNDJSON:
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		records = make([]interface{}, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec interface{}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			records = append(records, rec)
		}
	case converter.FormatCSV:
		return nil, fmt.Errorf("CSV format not supported for dry-run yet")
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	return records, nil
}

func (dr *DryRunReport) Print() {
	fmt.Printf("\n=== Dry-Run Report ===\n")
	fmt.Printf("Plan: %s\n", dr.PlanPath)
	fmt.Printf("Source: %s\n", dr.SourceFile)
	fmt.Printf("Total records: %d\n", dr.TotalRecords)
	fmt.Printf("Success: %d (%.1f%%)\n", dr.SuccessRecords, float64(dr.SuccessRecords)/float64(dr.TotalRecords)*100)
	fmt.Printf("Failed: %d (%.1f%%)\n", dr.FailedRecords, float64(dr.FailedRecords)/float64(dr.TotalRecords)*100)
	fmt.Printf("Missing fallback failures: %d\n", dr.MissingFallbackFailures)

	if len(dr.SkippedFieldStats) > 0 {
		fmt.Printf("\nSkipped fields (by path):\n")
		for path, count := range dr.SkippedFieldStats {
			fmt.Printf("  %s: %d\n", path, count)
		}
	}

	fmt.Printf("\n=== Operation Details ===\n")
	for _, s := range dr.OperationResults {
		riskColor := "\033[32m"
		switch s.Operation.RiskLevel {
		case RiskMedium:
			riskColor = "\033[33m"
		case RiskHigh:
			riskColor = "\033[31m"
		}

		fmt.Printf("\n  Step %d: %s%s\033[0m - %s\n", s.StepIndex, riskColor, s.Operation.Op, s.Operation.FieldPath)
		fmt.Printf("    Pass rate: %.1f%% (%d/%d)\n", s.PassRate*100, s.SuccessRecords, s.TotalRecords)
		fmt.Printf("    Failed: %d, Skipped: %d\n", s.FailedRecords, s.SkippedFields)

		if len(s.TopFailures) > 0 {
			fmt.Printf("    Top failures:\n")
			for _, f := range s.TopFailures {
				fmt.Printf("      - %dx: %s\n", f.Count, f.Reason)
			}
		}
	}

	if len(dr.Recommendations) > 0 {
		fmt.Printf("\n=== Recommendations ===\n")
		for _, s := range dr.Recommendations {
			fmt.Printf("\n  Step %d (failure rate: %.1f%%):\n", s.StepIndex, s.FailureRate*100)
			fmt.Printf("    Top reason: %s\n", s.TopReason)
			fmt.Printf("    Suggestion: %s\n", s.Action)
		}
	}
	fmt.Println()
}
