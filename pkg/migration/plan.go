package migration

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/schema-mapper/schema-mapper/pkg/converter"
	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/registry"
)

const (
	MaxSampleRecords = 1000
)

func GenerateMigrationPlan(reg *registry.Registry, schemaName, fromV, toV, dataSamplePath string) (*MigrationPlan, error) {
	script, _, err := GenerateMigrationScript(reg, schemaName, fromV, toV)
	if err != nil {
		return nil, err
	}

	riskStats := calculateRiskStats(script.Operations)

	plan := &MigrationPlan{
		SchemaName:     schemaName,
		FromVersion:    fromV,
		ToVersion:      toV,
		TotalSteps:     len(script.Operations),
		RiskStats:      riskStats,
		ExecutionState: string(StatePending),
		Operations:     script.Operations,
		CreatedAt:      time.Now().Format(time.RFC3339),
	}

	if dataSamplePath != "" {
		fromSchema, err := reg.GetSchema(schemaName, fromV)
		if err != nil {
			return nil, fmt.Errorf("failed to load source schema: %w", err)
		}

		qualityInfo, warnings, err := AnalyzeDataSample(dataSamplePath, fromSchema)
		if err != nil {
			return nil, fmt.Errorf("failed to analyze data sample: %w", err)
		}

		plan.FieldQualityInfo = qualityInfo
		plan.DataQualityWarnings = warnings

		_, affectedRecords, successRate := estimateImpact(qualityInfo, script.Operations)
		plan.EstimatedAffectedRecords = affectedRecords
		plan.EstimatedSuccessRate = successRate
	}

	return plan, nil
}

func calculateRiskStats(ops []*MigrationOperation) RiskStats {
	stats := RiskStats{}
	for _, op := range ops {
		switch op.RiskLevel {
		case RiskLow:
			stats.Low++
		case RiskMedium:
			stats.Medium++
		case RiskHigh:
			stats.High++
		}
	}
	return stats
}

func AnalyzeDataSample(dataFile string, schema *ir.Schema) ([]*FieldQualityInfo, []*DataQualityWarning, error) {
	records, err := loadSampleRecords(dataFile)
	if err != nil {
		return nil, nil, err
	}

	if len(records) > MaxSampleRecords {
		records = records[:MaxSampleRecords]
	}

	totalRecords := len(records)
	fieldStats := make(map[string]*fieldStat)

	for _, rec := range records {
		recordMap, ok := rec.(map[string]interface{})
		if !ok {
			continue
		}
		analyzeRecordFields("", recordMap, fieldStats)
	}

	qualityInfo := make([]*FieldQualityInfo, 0, len(fieldStats))
	warnings := make([]*DataQualityWarning, 0)

	for path, stat := range fieldStats {
		nonNullRate := float64(stat.nonNullCount) / float64(totalRecords)

		typeDist := make(map[string]int)
		for t, count := range stat.types {
			typeDist[t] = count
		}

		schemaField := schema.FindField(path)
		hasMismatch := false
		expectedType := ""

		if schemaField != nil {
			expectedType = string(schemaField.Type)
			hasMismatch = checkTypeMismatch(schemaField.Type, stat.types)
		}

		info := &FieldQualityInfo{
			FieldPath:        path,
			NonNullRate:      nonNullRate,
			TypeDistribution: typeDist,
			ExpectedType:     expectedType,
			HasTypeMismatch:  hasMismatch,
		}
		qualityInfo = append(qualityInfo, info)

		if hasMismatch {
			actualTypes := make([]string, 0, len(stat.types))
			for t := range stat.types {
				actualTypes = append(actualTypes, t)
			}
			warnings = append(warnings, &DataQualityWarning{
				FieldPath: path,
				Message:   fmt.Sprintf("Type mismatch: schema declares %s but data contains %s", expectedType, strings.Join(actualTypes, ", ")),
				Severity:  "high",
			})
		}

		if nonNullRate < 0.5 && schemaField != nil && !schemaField.Nullable {
			warnings = append(warnings, &DataQualityWarning{
				FieldPath: path,
				Message:   fmt.Sprintf("High null rate (%.1f%%) on non-nullable field", (1-nonNullRate)*100),
				Severity:  "medium",
			})
		}
	}

	return qualityInfo, warnings, nil
}

type fieldStat struct {
	nonNullCount int
	types        map[string]int
}

func analyzeRecordFields(prefix string, data map[string]interface{}, stats map[string]*fieldStat) {
	for key, val := range data {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		if _, ok := stats[path]; !ok {
			stats[path] = &fieldStat{
				types: make(map[string]int),
			}
		}

		if val != nil {
			stats[path].nonNullCount++
			typeName := getTypeName(val)
			stats[path].types[typeName]++
		}

		if nested, ok := val.(map[string]interface{}); ok {
			analyzeRecordFields(path, nested, stats)
		}
	}
}

func getTypeName(val interface{}) string {
	if val == nil {
		return "null"
	}

	switch v := val.(type) {
	case bool:
		return "bool"
	case float64:
		if v == float64(int64(v)) {
			return "int"
		}
		return "float"
	case int, int32, int64:
		return "int"
	case float32:
		return "float"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return reflect.TypeOf(val).Kind().String()
	}
}

func checkTypeMismatch(expected ir.BaseType, actualTypes map[string]int) bool {
	for t := range actualTypes {
		if !typeMatches(expected, t) {
			return true
		}
	}
	return false
}

func typeMatches(expected ir.BaseType, actual string) bool {
	switch expected {
	case ir.TypeInt32, ir.TypeInt64:
		return actual == "int"
	case ir.TypeFloat32, ir.TypeFloat64:
		return actual == "int" || actual == "float"
	case ir.TypeString, ir.TypeDate, ir.TypeDateTime:
		return actual == "string"
	case ir.TypeBool:
		return actual == "bool"
	case ir.TypeArray:
		return actual == "array"
	case ir.TypeMap, ir.TypeStruct:
		return actual == "object"
	case ir.TypeBytes:
		return actual == "string"
	default:
		return true
	}
}

func loadSampleRecords(dataFile string) ([]interface{}, error) {
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
		return nil, fmt.Errorf("CSV format not supported for data sampling yet")
	default:
		return nil, fmt.Errorf("unsupported format for data sampling: %s", format)
	}

	return records, nil
}

func estimateImpact(qualityInfo []*FieldQualityInfo, ops []*MigrationOperation) (int64, int64, float64) {
	affectedFields := make(map[string]bool)
	for _, op := range ops {
		affectedFields[op.FieldPath] = true
		if op.NewFieldPath != "" {
			affectedFields[op.NewFieldPath] = true
		}
	}

	qualityMap := make(map[string]*FieldQualityInfo)
	for _, q := range qualityInfo {
		qualityMap[q.FieldPath] = q
	}

	totalNonNull := 0
	affectedNonNull := 0
	mismatchCount := 0

	for _, q := range qualityInfo {
		fieldRecords := int(q.NonNullRate * 100)
		totalNonNull += fieldRecords
		if affectedFields[q.FieldPath] {
			affectedNonNull += fieldRecords
			if q.HasTypeMismatch {
				mismatchCount += fieldRecords
			}
		}
	}

	successRate := 1.0
	if affectedNonNull > 0 {
		successRate = float64(affectedNonNull-mismatchCount) / float64(affectedNonNull)
	}

	return int64(totalNonNull), int64(affectedNonNull), successRate
}

func (mp *MigrationPlan) ParseExecutionState() (int, int, error) {
	if mp.ExecutionState == string(StatePending) {
		return 0, mp.TotalSteps, nil
	}
	if mp.ExecutionState == string(StateCompleted) {
		return mp.TotalSteps, mp.TotalSteps, nil
	}
	if strings.HasPrefix(mp.ExecutionState, string(StatePartial)+":") {
		parts := strings.Split(strings.TrimPrefix(mp.ExecutionState, string(StatePartial)+":"), "/")
		if len(parts) == 2 {
			var executed, total int
			_, err1 := fmt.Sscanf(parts[0], "%d", &executed)
			_, err2 := fmt.Sscanf(parts[1], "%d", &total)
			if err1 == nil && err2 == nil {
				return executed, total, nil
			}
		}
	}
	return 0, mp.TotalSteps, fmt.Errorf("unknown execution state: %s", mp.ExecutionState)
}

func (mp *MigrationPlan) UpdateExecutionState(executedSteps int) {
	if executedSteps >= mp.TotalSteps {
		mp.ExecutionState = string(StateCompleted)
	} else if executedSteps > 0 {
		mp.ExecutionState = fmt.Sprintf("%s:%d/%d", string(StatePartial), executedSteps, mp.TotalSteps)
	} else {
		mp.ExecutionState = string(StatePending)
	}
}

func (mp *MigrationPlan) Print() {
	fmt.Printf("\n=== Migration Plan: %s %s -> %s\n", mp.SchemaName, mp.FromVersion, mp.ToVersion)
	fmt.Printf("Created at: %s\n", mp.CreatedAt)
	fmt.Printf("Total steps: %d\n", mp.TotalSteps)
	fmt.Printf("Execution state: %s\n", mp.ExecutionState)
	if strings.HasPrefix(mp.ExecutionState, string(StatePartial)+":") {
		executed, total, err := mp.ParseExecutionState()
		if err == nil {
			fmt.Printf("Executed steps: %d/%d\n", executed, total)
		}
	}
	fmt.Printf("\nRisk distribution:\n")
	fmt.Printf("  Low:    %d\n", mp.RiskStats.Low)
	fmt.Printf("  Medium: %d\n", mp.RiskStats.Medium)
	fmt.Printf("  High:   %d\n", mp.RiskStats.High)
	if mp.EstimatedAffectedRecords > 0 {
		fmt.Printf("\nEstimated affected records: %d\n", mp.EstimatedAffectedRecords)
		fmt.Printf("Estimated success rate: %.1f%%\n", mp.EstimatedSuccessRate*100)
	}
	if len(mp.DataQualityWarnings) > 0 {
		fmt.Printf("\nData Quality Warnings (%d):\n", len(mp.DataQualityWarnings))
		for _, w := range mp.DataQualityWarnings {
			fmt.Printf("  [%s] %s: %s\n", w.Severity, w.FieldPath, w.Message)
		}
	}
	fmt.Printf("\nOperations (%d):\n", len(mp.Operations))
	for i, op := range mp.Operations {
		riskColor := "\033[32m"
		switch op.RiskLevel {
		case RiskMedium:
			riskColor = "\033[33m"
		case RiskHigh:
			riskColor = "\033[31m"
		}
		fmt.Printf("\n  \033[1m[%d] %s%s\033[0m\n", i+1, riskColor, op.Op)
		fmt.Printf("      Field: %s\n", op.FieldPath)
		if op.NewFieldPath != "" {
			fmt.Printf("      New field: %s\n", op.NewFieldPath)
		}
		if op.NewType != "" {
			fmt.Printf("      Type: %s -> %s\n", op.OldType, op.NewType)
		}
		fmt.Printf("      Risk: %s%s\033[0m\n", riskColor, op.RiskLevel)
		fmt.Printf("      %s\n", op.Description)
	}
	fmt.Println()
}

func (mp *MigrationPlan) ToYAML() (string, error) {
	b, err := yaml.Marshal(mp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
