package migration

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	"github.com/schema-mapper/schema-mapper/pkg/converter"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

type ExecuteResult struct {
	PlanPath           string
	SourceFile         string
	OutputFile         string
	FromStep           int
	ToStep             int
	TotalSteps         int
	TotalRecords       int64
	SuccessRecords     int64
	FailedRecords      int64
	Duration           time.Duration
	RollbackScriptPath string
	Errors             []string
}

func ExecuteMigrationPlan(planPath, sourceFile, outputFile string, targetStep int, opts converter.ConversionOptions) (*ExecuteResult, error) {
	plan, err := LoadMigrationPlan(planPath)
	if err != nil {
		return nil, err
	}

	executedSteps, totalSteps, err := plan.ParseExecutionState()
	if err != nil {
		return nil, err
	}

	if targetStep <= 0 || targetStep > totalSteps {
		targetStep = totalSteps
	}

	if targetStep <= executedSteps {
		return nil, fmt.Errorf("target step %d is already executed (current: %d)", targetStep, executedSteps)
	}

	fromStep := executedSteps
	toStep := targetStep

	records, err := loadAllRecords(sourceFile)
	if err != nil {
		return nil, err
	}

	rollbackOps, err := executeOperations(plan.Operations[fromStep:toStep], records, fromStep)
	if err != nil {
		return nil, err
	}

	result := &ExecuteResult{
		PlanPath:       planPath,
		SourceFile:     sourceFile,
		OutputFile:     outputFile,
		FromStep:       fromStep,
		ToStep:         toStep,
		TotalSteps:     totalSteps,
		TotalRecords:   int64(len(records)),
		SuccessRecords: int64(len(records)),
		Errors:         make([]string, 0),
	}

	if err := writeOutput(records, outputFile, opts); err != nil {
		return nil, fmt.Errorf("failed to write output: %w", err)
	}
	result.OutputPath(outputFile)

	rollbackScript := &RollbackScript{
		PlanPath:   planPath,
		FromStep:   fromStep + 1,
		ToStep:     toStep,
		Operations: rollbackOps,
		CreatedAt:  time.Now().Format(time.RFC3339),
		SourceFile: sourceFile,
	}

	rollbackPath := generateRollbackPath(planPath)
	if err := rollbackScript.Save(rollbackPath); err != nil {
		return nil, fmt.Errorf("failed to save rollback script: %w", err)
	}
	result.RollbackScriptPath = rollbackPath

	plan.UpdateExecutionState(toStep)
	if err := plan.Save(planPath); err != nil {
		return nil, fmt.Errorf("failed to update plan: %w", err)
	}

	return result, nil
}

func executeOperations(ops []*MigrationOperation, records []interface{}, startIdx int) ([]*RollbackOperation, error) {
	rollbackOps := make([]*RollbackOperation, 0, len(ops))
	fieldAliases := make(map[string]string)
	sampleSnapshots := make(map[string]interface{})

	for opIdx, op := range ops {
		stepNum := startIdx + opIdx + 1

		rollbackOp, err := generateRollbackOperation(op, records, fieldAliases, sampleSnapshots)
		if err != nil {
			return nil, fmt.Errorf("failed to generate rollback for step %d: %w", stepNum, err)
		}
		rollbackOps = append([]*RollbackOperation{rollbackOp}, rollbackOps...)

		if err := applyOperationToRecords(op, records, fieldAliases); err != nil {
			return nil, fmt.Errorf("failed to execute step %d: %w", stepNum, err)
		}

		updateAliases(op, fieldAliases)
	}

	return rollbackOps, nil
}

func generateRollbackOperation(op *MigrationOperation, records []interface{}, aliases map[string]string, snapshots map[string]interface{}) (*RollbackOperation, error) {
	fieldPath := op.FieldPath
	if alias, ok := aliases[fieldPath]; ok {
		fieldPath = alias
	}

	rollbackOp := &RollbackOperation{
		Description: fmt.Sprintf("Rollback: %s", op.Description),
	}

	switch op.Op {
	case OpRenameField:
		rollbackOp.Op = OpRenameField
		rollbackOp.FieldPath = op.NewFieldPath
		rollbackOp.NewFieldPath = op.FieldPath
		rollbackOp.Description = fmt.Sprintf("Rename field %s back to %s", op.NewFieldPath, op.FieldPath)

	case OpAddField:
		rollbackOp.Op = OpRemoveField
		rollbackOp.FieldPath = op.FieldPath
		rollbackOp.Description = fmt.Sprintf("Remove added field %s", op.FieldPath)

	case OpRemoveField:
		rollbackOp.Op = OpAddField
		rollbackOp.FieldPath = op.FieldPath
		rollbackOp.OldType = op.OldType

		if len(records) > 0 {
			if firstRec, ok := records[0].(map[string]interface{}); ok {
				if val, exists := mapper.GetNestedValue(firstRec, fieldPath); exists {
					snapshots[fieldPath] = deepCopy(val)
				}
			}
		}
		rollbackOp.ValueSnapshot = snapshots[fieldPath]
		rollbackOp.Description = fmt.Sprintf("Restore removed field %s with value snapshot", op.FieldPath)

	case OpChangeType:
		rollbackOp.Op = OpChangeType
		rollbackOp.FieldPath = fieldPath
		rollbackOp.OldType = op.NewType
		rollbackOp.NewType = op.OldType
		if op.FallbackValue != nil {
			rollbackOp.DefaultValue = op.FallbackValue
		}
		rollbackOp.Description = fmt.Sprintf("Change type %s back to %s", op.NewType, op.OldType)

	case OpAddConstraint:
		rollbackOp.Op = OpRemoveConstraint
		rollbackOp.FieldPath = fieldPath
		rollbackOp.Description = fmt.Sprintf("Remove added constraint on %s", fieldPath)

	case OpRemoveConstraint:
		rollbackOp.Op = OpAddConstraint
		rollbackOp.FieldPath = fieldPath
		rollbackOp.Description = fmt.Sprintf("Restore removed constraint on %s", fieldPath)

	default:
		return nil, fmt.Errorf("unknown operation type: %s", op.Op)
	}

	return rollbackOp, nil
}

func deepCopy(val interface{}) interface{} {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, vv := range v {
			result[k] = deepCopy(vv)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, vv := range v {
			result[i] = deepCopy(vv)
		}
		return result
	default:
		return v
	}
}

func applyOperationToRecords(op *MigrationOperation, records []interface{}, aliases map[string]string) error {
	fieldPath := op.FieldPath
	if alias, ok := aliases[fieldPath]; ok {
		fieldPath = alias
	}

	for _, record := range records {
		recordMap, ok := record.(map[string]interface{})
		if !ok {
			continue
		}

		switch op.Op {
		case OpAddField:
			if _, exists := mapper.GetNestedValue(recordMap, fieldPath); !exists {
				mapper.SetNestedValue(recordMap, fieldPath, op.DefaultValue)
			}

		case OpRemoveField:
			removeNestedValue(recordMap, fieldPath)

		case OpRenameField:
			val, exists := mapper.GetNestedValue(recordMap, fieldPath)
			if exists {
				mapper.SetNestedValue(recordMap, op.NewFieldPath, val)
				removeNestedValue(recordMap, fieldPath)
			}

		case OpChangeType:
			val, exists := mapper.GetNestedValue(recordMap, fieldPath)
			if exists && val != nil {
				newVal, err := castValueForExecution(val, op.NewType, op.FallbackValue)
				if err != nil {
					if op.FallbackValue != nil {
						newVal = op.FallbackValue
					} else {
						return fmt.Errorf("type conversion failed for field %s: %w", fieldPath, err)
					}
				}
				mapper.SetNestedValue(recordMap, fieldPath, newVal)
			}

		case OpAddConstraint, OpRemoveConstraint:
			continue
		}
	}

	return nil
}

func castValueForExecution(val interface{}, targetType string, fallback interface{}) (interface{}, error) {
	strVal := fmt.Sprintf("%v", val)
	switch strings.ToLower(targetType) {
	case "int", "int32", "integer":
		var result int64
		_, err := fmt.Sscanf(strVal, "%d", &result)
		if err != nil {
			if fallback != nil {
				return fallback, nil
			}
			return nil, err
		}
		return int32(result), nil
	case "int64", "long":
		var result int64
		_, err := fmt.Sscanf(strVal, "%d", &result)
		if err != nil {
			if fallback != nil {
				return fallback, nil
			}
			return nil, err
		}
		return result, nil
	case "float", "float32":
		var result float64
		_, err := fmt.Sscanf(strVal, "%f", &result)
		if err != nil {
			if fallback != nil {
				return fallback, nil
			}
			return nil, err
		}
		return float32(result), nil
	case "float64", "double":
		var result float64
		_, err := fmt.Sscanf(strVal, "%f", &result)
		if err != nil {
			if fallback != nil {
				return fallback, nil
			}
			return nil, err
		}
		return result, nil
	case "string", "str":
		return strVal, nil
	case "bool", "boolean":
		s := strings.ToLower(strVal)
		switch s {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no":
			return false, nil
		default:
			if fallback != nil {
				return fallback, nil
			}
			return nil, fmt.Errorf("invalid boolean: %s", strVal)
		}
	default:
		return val, nil
	}
}

func removeNestedValue(data map[string]interface{}, path string) {
	parts := strings.Split(path, ".")
	if len(parts) == 1 {
		delete(data, parts[0])
		return
	}

	current := data
	for i := 0; i < len(parts)-1; i++ {
		next, ok := current[parts[i]]
		if !ok {
			return
		}
		if m, ok := next.(map[string]interface{}); ok {
			current = m
		} else {
			return
		}
	}
	delete(current, parts[len(parts)-1])
}

func updateAliases(op *MigrationOperation, aliases map[string]string) {
	if op.Op == OpRenameField {
		aliases[op.FieldPath] = op.NewFieldPath
		for k, v := range aliases {
			if v == op.FieldPath {
				aliases[k] = op.NewFieldPath
			}
		}
	}
}

func writeOutput(records []interface{}, outputFile string, opts converter.ConversionOptions) error {
	outputFormat := opts.OutputFormat
	if outputFormat == "" {
		outputFormat = converter.DetectFormat(outputFile)
	}

	var data []byte
	var err error

	switch outputFormat {
	case converter.FormatJSON:
		if len(records) == 1 {
			data, err = json.MarshalIndent(records[0], "", "  ")
		} else {
			data, err = json.MarshalIndent(records, "", "  ")
		}
	case converter.FormatNDJSON:
		var sb strings.Builder
		for _, rec := range records {
			recBytes, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			sb.Write(recBytes)
			sb.WriteString("\n")
		}
		data = []byte(sb.String())
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}

	if err != nil {
		return err
	}

	if err := ioutil.WriteFile(outputFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	return nil
}

func generateRollbackPath(planPath string) string {
	dir := filepath.Dir(planPath)
	timestamp := time.Now().Format("20060102_150405")
	return filepath.Join(dir, fmt.Sprintf("rollback_%s.yaml", timestamp))
}

func (r *ExecuteResult) OutputPath(p string) {
	r.OutputFile = p
}

func (r *ExecuteResult) Print() {
	fmt.Printf("\n=== Migration Execution Result ===\n")
	fmt.Printf("Plan: %s\n", r.PlanPath)
	fmt.Printf("Steps executed: %d - %d (total: %d)\n", r.FromStep+1, r.ToStep, r.TotalSteps)
	fmt.Printf("Source: %s\n", r.SourceFile)
	fmt.Printf("Output: %s\n", r.OutputFile)
	fmt.Printf("Total records: %d\n", r.TotalRecords)
	fmt.Printf("Success: %d\n", r.SuccessRecords)
	fmt.Printf("Failed: %d\n", r.FailedRecords)
	fmt.Printf("Rollback script: %s\n", r.RollbackScriptPath)

	if len(r.Errors) > 0 {
		fmt.Printf("\nErrors (first %d):\n", min(len(r.Errors), 10))
		for i, e := range r.Errors {
			if i >= 10 {
				break
			}
			fmt.Printf("  %s\n", e)
		}
	}
	fmt.Println()
}

func (r *ExecuteResult) ToJSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}

func RollbackMigration(rollbackPath, sourceFile, outputFile string, opts converter.ConversionOptions) (*ExecuteResult, error) {
	script, err := LoadRollbackScript(rollbackPath)
	if err != nil {
		return nil, err
	}

	records, err := loadAllRecords(sourceFile)
	if err != nil {
		return nil, err
	}

	aliases := make(map[string]string)

	for _, op := range script.Operations {
		if err := applyRollbackOperation(op, records, aliases); err != nil {
			return nil, fmt.Errorf("rollback failed: %w", err)
		}
	}

	if err := writeOutput(records, outputFile, opts); err != nil {
		return nil, fmt.Errorf("failed to write output: %w", err)
	}

	result := &ExecuteResult{
		PlanPath:       script.PlanPath,
		SourceFile:     sourceFile,
		OutputFile:     outputFile,
		FromStep:       script.FromStep,
		ToStep:         script.ToStep,
		TotalSteps:     len(script.Operations),
		TotalRecords:   int64(len(records)),
		SuccessRecords: int64(len(records)),
		Errors:         make([]string, 0),
	}

	return result, nil
}

func applyRollbackOperation(op *RollbackOperation, records []interface{}, aliases map[string]string) error {
	fieldPath := op.FieldPath
	if alias, ok := aliases[fieldPath]; ok {
		fieldPath = alias
	}

	for _, record := range records {
		recordMap, ok := record.(map[string]interface{})
		if !ok {
			continue
		}

		switch op.Op {
		case OpAddField:
			if op.ValueSnapshot != nil {
				mapper.SetNestedValue(recordMap, fieldPath, deepCopy(op.ValueSnapshot))
			} else if op.DefaultValue != nil {
				mapper.SetNestedValue(recordMap, fieldPath, op.DefaultValue)
			} else {
				mapper.SetNestedValue(recordMap, fieldPath, nil)
			}

		case OpRemoveField:
			removeNestedValue(recordMap, fieldPath)

		case OpRenameField:
			val, exists := mapper.GetNestedValue(recordMap, fieldPath)
			if exists {
				mapper.SetNestedValue(recordMap, op.NewFieldPath, val)
				removeNestedValue(recordMap, fieldPath)
			}

		case OpChangeType:
			val, exists := mapper.GetNestedValue(recordMap, fieldPath)
			if exists && val != nil {
				newVal, err := castValueForExecution(val, op.NewType, op.DefaultValue)
				if err != nil {
					if op.DefaultValue != nil {
						newVal = op.DefaultValue
					} else {
						return fmt.Errorf("type conversion failed for field %s: %w", fieldPath, err)
					}
				}
				mapper.SetNestedValue(recordMap, fieldPath, newVal)
			}

		case OpAddConstraint, OpRemoveConstraint:
			continue
		}
	}

	if op.Op == OpRenameField {
		aliases[op.FieldPath] = op.NewFieldPath
	}

	return nil
}
