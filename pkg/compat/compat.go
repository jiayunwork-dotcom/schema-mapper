package compat

import (
	"encoding/json"
	"fmt"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

type CompatibilityIssueSeverity string

const (
	SeveritySafe     CompatibilityIssueSeverity = "safe"
	SeverityWarning CompatibilityIssueSeverity = "warning"
	SeverityError CompatibilityIssueSeverity = "error"
)

type CompatibilityIssue struct {
	SourcePath   string                    `json:"sourcePath"`
	TargetPath   string                    `json:"targetPath"`
	Severity     CompatibilityIssueSeverity `json:"severity"`
	IssueType      string                    `json:"issueType"`
	Description    string                    `json:"description"`
	ScorePenalty int                      `json:"scorePenalty"`
}

type CompatibilityResult struct {
	SourceSchema *ir.Schema             `json:"sourceSchema"`
	TargetSchema *ir.Schema             `json:"targetSchema"`
	SafeFields     []string                 `json:"safeFields"`
	WarningFields  []CompatibilityIssue `json:"warningFields"`
	ErrorFields    []CompatibilityIssue `json:"errorFields"`
	UnmappedFields []string                `json:"unmappedFields"`
	Issues        []CompatibilityIssue     `json:"allIssues"`
	Score         int                        `json:"score"`
	HasIncompatible bool                 `json:"hasIncompatible"`
}

func CheckCompatibility(source, target *ir.Schema) *CompatibilityResult {
	result := &CompatibilityResult{
		SourceSchema: source,
		TargetSchema: target,
		SafeFields:     make([]string, 0),
		WarningFields:  make([]CompatibilityIssue, 0),
		ErrorFields:    make([]CompatibilityIssue, 0),
		UnmappedFields: make([]string, 0),
		Issues:        make([]CompatibilityIssue, 0),
	}

	autoMapper := mapper.NewAutoMapper()
	mapResult := autoMapper.GenerateMapping(source, target)

	totalFields := 0
	targetLeafFields := make([]*ir.Field, 0)
	for _, f := range target.AllFields() {
		if f.IsLeaf() {
			totalFields++
			targetLeafFields = append(targetLeafFields, f)
		}
	}

	mappedTargets := make(map[string]bool)

	for _, mapping := range mapResult.Mappings {
		srcField := source.FindField(mapping.SourcePath)
		tgtField := target.FindField(mapping.TargetPath)

		if srcField == nil || tgtField == nil {
			continue
		}

		mappedTargets[mapping.TargetPath] = true

		issue := checkFieldCompatibility(srcField, tgtField, mapping)
		if issue != nil {
			result.Issues = append(result.Issues, *issue)
			switch issue.Severity {
			case SeverityWarning:
				result.WarningFields = append(result.WarningFields, *issue)
			case SeverityError:
				result.ErrorFields = append(result.ErrorFields, *issue)
			}
		} else {
			result.SafeFields = append(result.SafeFields, mapping.TargetPath)
		}
	}

	for _, tgt := range targetLeafFields {
		if !mappedTargets[tgt.Path] {
			result.UnmappedFields = append(result.UnmappedFields, tgt.Path)
			if !tgt.Nullable && tgt.Default == nil {
				issue := CompatibilityIssue{
					SourcePath:   "",
					TargetPath:   tgt.Path,
					Severity:     SeverityError,
					IssueType:      "missing_required_field",
					Description:    fmt.Sprintf("Required target field has no mapping: %s", tgt.Path),
					ScorePenalty: 20,
				}
				result.Issues = append(result.Issues, issue)
				result.ErrorFields = append(result.ErrorFields, issue)
			}
		}
	}

	totalPossibleScore := totalFields * 20
	penalty := 0
	for _, issue := range result.Issues {
		penalty += issue.ScorePenalty
	}

	score := 100
	if totalPossibleScore > 0 {
		score = 100 - (penalty * 100 / totalPossibleScore)
		if score < 0 {
			score = 0
		}
	}
	result.Score = score

	for _, issue := range result.Issues {
		if issue.Severity == SeverityError {
			result.HasIncompatible = true
			break
		}
	}

	return result
}

func checkFieldCompatibility(src, tgt *ir.Field, mapping *mapper.MappingSuggestion) *CompatibilityIssue {
	if src.Type == tgt.Type {
		return checkConstraintsAndNullable(src, tgt)
	}

	compat := mapper.CheckTypeCompatibility(src.Type, tgt.Type)

	switch compat {
	case "safe":
		return checkConstraintsAndNullable(src, tgt)

	case "warning":
		issue := &CompatibilityIssue{
			SourcePath:   src.Path,
			TargetPath:   tgt.Path,
			Severity:     SeverityWarning,
			IssueType:    "precision_loss",
			Description:  fmt.Sprintf("Type conversion %s -> %s may lose precision", src.Type, tgt.Type),
			ScorePenalty: penaltyFor(src.Type, tgt.Type),
		}
		return issue

	default:
		return &CompatibilityIssue{
			SourcePath:   src.Path,
			TargetPath:   tgt.Path,
			Severity:     SeverityError,
			IssueType:    "incompatible_types",
			Description:  fmt.Sprintf("Type conversion %s -> %s is incompatible", src.Type, tgt.Type),
			ScorePenalty: 15,
		}
	}
}

func checkConstraintsAndNullable(src, tgt *ir.Field) *CompatibilityIssue {
	if !tgt.Nullable && src.Nullable && tgt.Default == nil {
		return &CompatibilityIssue{
			SourcePath:   src.Path,
			TargetPath:   tgt.Path,
			Severity:     SeverityWarning,
			IssueType:      "nullable_mismatch",
			Description:    fmt.Sprintf("Source is nullable, target requires non-null"),
			ScorePenalty: 10,
		}
	}

	if tgt.Constraints != nil {
		if tgt.Constraints.MaxLength != nil && src.Constraints != nil {
			if src.Constraints.MaxLength == nil || *src.Constraints.MaxLength > *tgt.Constraints.MaxLength {
				return &CompatibilityIssue{
					SourcePath:   src.Path,
					TargetPath:   tgt.Path,
					Severity:     SeverityWarning,
					IssueType:      "length_truncation",
					Description:    fmt.Sprintf("Source max length exceeds target: source may exceed target max length"),
					ScorePenalty: 10,
				}
			}
		}
	}

	return nil
}

func penaltyFor(src, tgt ir.BaseType) int {
	switch {
	case src == ir.TypeFloat64 && tgt == ir.TypeFloat32:
		return 10
	case src == ir.TypeInt64 && tgt == ir.TypeInt32:
		return 10
	case src == ir.TypeString && (tgt == ir.TypeDate || tgt == ir.TypeDateTime):
		return 15
	case src == ir.TypeString && (tgt == ir.TypeInt32 || tgt == ir.TypeInt64):
		return 15
	default:
		return 5
	}
}

func (r *CompatibilityResult) Print() {
	fmt.Printf("\n=== Compatibility Check Report\n")
	fmt.Printf("Source: %s -> Target: %s\n", r.SourceSchema.Name, r.TargetSchema.Name)

	scoreColor := "\033[32m"
	if r.Score >= 80 {
		scoreColor = "\033[32m"
	} else if r.Score >= 50 {
		scoreColor = "\033[33m"
	} else {
		scoreColor = "\033[31m"
	}
	fmt.Printf("Compatibility Score: %s%d/100\033[0m\n\n", scoreColor, r.Score)

	if len(r.SafeFields) > 0 {
		fmt.Printf("\033[32m=== Fully Compatible Fields (%d) ===\033[0m\n", len(r.SafeFields))
		for _, f := range r.SafeFields[:min(len(r.SafeFields), 10)] {
			fmt.Printf("  ✓ %s\n", f)
		}
		if len(r.SafeFields) > 10 {
			fmt.Printf("  ... and %d more\n", len(r.SafeFields)-10)
		}
		fmt.Println()
	}

	if len(r.WarningFields) > 0 {
		fmt.Printf("\033[33m=== Warnings (%d) ===\033[0m\n", len(r.WarningFields))
		for _, w := range r.WarningFields {
			fmt.Printf("  ⚠ %s: %s\n", w.TargetPath, w.Description)
		}
		fmt.Println()
	}

	if len(r.ErrorFields) > 0 {
		fmt.Printf("\033[31m=== Errors (%d) ===\033[0m\n", len(r.ErrorFields))
		for _, e := range r.ErrorFields {
			fmt.Printf("  ✗ %s: %s\n", e.TargetPath, e.Description)
		}
		fmt.Println()
	}

	if len(r.UnmappedFields) > 0 {
		fmt.Printf("\033[33m=== Unmapped Target Fields (%d) ===\033[0m\n", len(r.UnmappedFields))
		for _, u := range r.UnmappedFields {
			fmt.Printf("  ? %s\n", u)
		}
		fmt.Println()
	}

	if r.HasIncompatible {
		fmt.Println("\033[31m✗ Incompatible fields detected - conversion may fail or lose data\033[0m")
	} else if len(r.WarningFields) > 0 {
		fmt.Println("\033[33m⚠ Compatible with warnings - conversion may lose some precision\033[0m")
	} else {
		fmt.Println("\033[32m✓ Fully compatible - conversion is safe\033[0m")
	}
}

func (r *CompatibilityResult) ToJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
