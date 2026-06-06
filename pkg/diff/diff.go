package diff

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type ChangeType string

const (
	ChangeAdded    ChangeType = "added"
	ChangeRemoved  ChangeType = "removed"
	ChangeTypeChanged   ChangeType = "changed"
	ChangeConstraint ChangeType = "constraint"
)

type FieldDiff struct {
	Path        string      `json:"path"`
	ChangeType  ChangeType  `json:"changeType"`
	OldType     string      `json:"oldType,omitempty"`
	NewType     string      `json:"newType,omitempty"`
	OldValue    interface{} `json:"oldValue,omitempty"`
	NewValue    interface{} `json:"newValue,omitempty"`
	Description string      `json:"description"`
}

type DiffResult struct {
	SchemaV1      *ir.Schema  `json:"schemaV1"`
	SchemaV2      *ir.Schema  `json:"schemaV2"`
	Added        []FieldDiff  `json:"added"`
	Removed      []FieldDiff  `json:"removed"`
	TypeChanges  []FieldDiff  `json:"typeChanges"`
	ConstraintChanges []FieldDiff `json:"constraintChanges"`
	CompatLevel   CompatibilityLevel `json:"compatibilityLevel"`
	CompatDesc  string       `json:"compatibilityDescription"`
	Incompatibilities []string `json:"incompatibilities"`
}

type CompatibilityLevel string

const (
	CompatFull       CompatibilityLevel = "full"
	CompatForward    CompatibilityLevel = "forward"
	CompatBackward   CompatibilityLevel = "backward"
	CompatIncompatible CompatibilityLevel = "incompatible"
)

func CompareSchemas(s1, s2 *ir.Schema) *DiffResult {
	result := &DiffResult{
		SchemaV1:          s1,
		SchemaV2:          s2,
		Added:              make([]FieldDiff, 0),
		Removed:            make([]FieldDiff, 0),
		TypeChanges:        make([]FieldDiff, 0),
		ConstraintChanges: make([]FieldDiff, 0),
		Incompatibilities: make([]string, 0),
	}

	fields1 := make(map[string]*ir.Field)
	for _, f := range s1.AllFields() {
		if f.IsLeaf() {
			fields1[f.Path] = f
		}
	}

	fields2 := make(map[string]*ir.Field)
	for _, f := range s2.AllFields() {
		if f.IsLeaf() {
			fields2[f.Path] = f
		}
	}

	for path, f2 := range fields2 {
		if _, ok := fields1[path]; !ok {
			result.Added = append(result.Added, FieldDiff{
				Path:        path,
				ChangeType:  ChangeAdded,
				NewType:     string(f2.Type),
				Description: fmt.Sprintf("Field added: %s (%s)", path, f2.Type),
			})
		}
	}

	for path, f1 := range fields1 {
		f2, ok := fields2[path]
		if !ok {
			result.Removed = append(result.Removed, FieldDiff{
				Path:        path,
				ChangeType:  ChangeRemoved,
				OldType:     string(f1.Type),
				Description: fmt.Sprintf("Field removed: %s (%s)", path, f1.Type),
			})
			if !f1.Nullable && f1.Default == nil {
				result.Incompatibilities = append(result.Incompatibilities,
					fmt.Sprintf("Breaking change: required field removed: %s", path))
			}
		} else {
			if f1.Type != f2.Type {
				result.TypeChanges = append(result.TypeChanges, FieldDiff{
					Path:       path,
					ChangeType:  ChangeTypeChanged,
					OldType:    string(f1.Type),
					NewType:    string(f2.Type),
					Description: fmt.Sprintf("Type changed: %s %s -> %s", path, f1.Type, f2.Type),
				})
				if !isTypeCompatible(f1.Type, f2.Type) {
					result.Incompatibilities = append(result.Incompatibilities,
						fmt.Sprintf("Incompatible type change: %s %s -> %s", path, f1.Type, f2.Type))
				}
			}

			constraintDiff := compareConstraints(f1, f2, path)
			if constraintDiff != nil {
				result.ConstraintChanges = append(result.ConstraintChanges, *constraintDiff)
			}

			if f1.Nullable != f2.Nullable {
				if !f1.Nullable && f2.Nullable {
					result.Incompatibilities = append(result.Incompatibilities,
						fmt.Sprintf("Field became required: %s", path))
				}
			}
		}
	}

	result.CompatLevel, result.CompatDesc = determineCompatibility(result)

	return result
}

func compareConstraints(f1, f2 *ir.Field, path string) *FieldDiff {
	c1 := f1.Constraints
	c2 := f2.Constraints

	changes := make([]string, 0)

	if (c1 == nil && c2 == nil) {
		return nil
	}

	if c1 == nil {
		c1 = &ir.Constraint{}
	}
	if c2 == nil {
		c2 = &ir.Constraint{}
	}

	if c1.MaxLength != nil && c2.MaxLength == nil {
		changes = append(changes, fmt.Sprintf("maxLength removed"))
	} else if c1.MaxLength == nil && c2.MaxLength != nil {
		changes = append(changes, fmt.Sprintf("maxLength added: %d", *c2.MaxLength))
	} else if c1.MaxLength != nil && c2.MaxLength != nil && *c1.MaxLength != *c2.MaxLength {
		changes = append(changes, fmt.Sprintf("maxLength changed: %d -> %d", *c1.MaxLength, *c2.MaxLength))
	}

	if c1.MinLength != nil && c2.MinLength == nil {
		changes = append(changes, fmt.Sprintf("minLength removed"))
	} else if c1.MinLength == nil && c2.MinLength != nil {
		changes = append(changes, fmt.Sprintf("minLength added: %d", *c2.MinLength))
	} else if c1.MinLength != nil && c2.MinLength != nil && *c1.MinLength != *c2.MinLength {
		changes = append(changes, fmt.Sprintf("minLength changed: %d -> %d", *c1.MinLength, *c2.MinLength))
	}

	if c1.Maximum != nil && c2.Maximum == nil {
		changes = append(changes, fmt.Sprintf("maximum removed"))
	} else if c1.Maximum == nil && c2.Maximum != nil {
		changes = append(changes, fmt.Sprintf("maximum added: %f", *c2.Maximum))
	} else if c1.Maximum != nil && c2.Maximum != nil && *c1.Maximum != *c2.Maximum {
		changes = append(changes, fmt.Sprintf("maximum changed: %f -> %f", *c1.Maximum, *c2.Maximum))
	}

	if c1.Minimum != nil && c2.Minimum == nil {
		changes = append(changes, fmt.Sprintf("minimum removed"))
	} else if c1.Minimum == nil && c2.Minimum != nil {
		changes = append(changes, fmt.Sprintf("minimum added: %f", *c2.Minimum))
	} else if c1.Minimum != nil && c2.Minimum != nil && *c1.Minimum != *c2.Minimum {
		changes = append(changes, fmt.Sprintf("minimum changed: %f -> %f", *c1.Minimum, *c2.Minimum))
	}

	if c1.Pattern != c2.Pattern {
		if c1.Pattern != "" && c2.Pattern != "" {
			changes = append(changes, fmt.Sprintf("pattern changed: %s -> %s", c1.Pattern, c2.Pattern))
		} else if c1.Pattern != "" {
			changes = append(changes, "pattern removed")
		} else {
			changes = append(changes, fmt.Sprintf("pattern added: %s", c2.Pattern))
		}
	}

	if f1.Nullable != f2.Nullable {
		if f1.Nullable && !f2.Nullable {
			changes = append(changes, "NOT NULL constraint added")
		} else {
			changes = append(changes, "NOT NULL constraint removed")
		}
	}

	if len(changes) == 0 {
		return nil
	}

	return &FieldDiff{
		Path:        path,
		ChangeType:  ChangeConstraint,
		Description: strings.Join(changes, "; "),
	}
}

func isTypeCompatible(oldType, newType ir.BaseType) bool {
	if oldType == newType {
		return true
	}

	safeWidening := map[ir.BaseType][]ir.BaseType{
		ir.TypeInt32:   {ir.TypeInt64, ir.TypeFloat64},
		ir.TypeInt64:   {ir.TypeFloat64},
		ir.TypeFloat32: {ir.TypeFloat64},
		ir.TypeDate:    {ir.TypeDateTime, ir.TypeString},
	}

	if targets, ok := safeWidening[oldType]; ok {
		for _, t := range targets {
			if t == newType {
				return true
			}
		}
	}

	if oldType.IsStringLike() && newType.IsStringLike() {
		return true
	}

	return false
}

func determineCompatibility(result *DiffResult) (CompatibilityLevel, string) {
	if len(result.Incompatibilities) > 0 {
		return CompatIncompatible, "Schema changes are incompatible"
	}

	hasAddedRequired := false
	hasRemovedOptional := false

	for _, a := range result.Added {
		path := a.Path
		f := result.SchemaV2.FindField(path)
		if f != nil && !f.Nullable && f.Default == nil {
			hasAddedRequired = true
		}
	}

	for range result.Removed {
		hasRemovedOptional = true
	}

	hasTypeChange := len(result.TypeChanges) > 0

	if !hasAddedRequired && !hasRemovedOptional && !hasTypeChange {
		return CompatFull, "Fully compatible - all changes are backward and forward compatible"
	}

	if hasAddedRequired && !hasRemovedOptional {
		return CompatBackward, "Backward compatible - new schema can read old data, but old schema cannot read all new data (new required fields)"
	}

	if !hasAddedRequired {
		return CompatForward, "Forward compatible - old schema can read new data, but new schema may not read all old data"
	}

	return CompatIncompatible, "Incompatible changes detected"
}

func (r *DiffResult) PrintColored() {
	fmt.Printf("=== Schema Diff Report\n")
	fmt.Printf("V1: %s (source: %s)\n", r.SchemaV1.Name, r.SchemaV1.SourceType)
	fmt.Printf("V2: %s (source: %s)\n\n", r.SchemaV2.Name, r.SchemaV2.SourceType)

	compatColor := "\033[32m"
	switch r.CompatLevel {
	case CompatFull:
		compatColor = "\033[32m"
	case CompatForward, CompatBackward:
		compatColor = "\033[33m"
	case CompatIncompatible:
		compatColor = "\033[31m"
	}

	fmt.Printf("Compatibility: %s%s\033[0m\n", compatColor, r.CompatLevel)
	fmt.Printf("%s\n\n", r.CompatDesc)

	if len(r.Added) > 0 {
		fmt.Println("\033[32m=== Added Fields ===\033[0m")
		for _, a := range r.Added {
			fmt.Printf("  \033[32m+ %s (%s)\033[0m\n", a.Path, a.NewType)
		}
	}

	if len(r.Removed) > 0 {
		fmt.Println("\033[31m=== Removed Fields ===\033[0m")
		for _, d := range r.Removed {
			fmt.Printf("  \033[31m- %s (%s)\033[0m\n", d.Path, d.OldType)
		}
	}

	if len(r.TypeChanges) > 0 {
		fmt.Println("\033[33m=== Type Changes ===\033[33m")
		for _, c := range r.TypeChanges {
			fmt.Printf("  \033[33m~ %s: %s -> %s\033[0m\n", c.Path, c.OldType, c.NewType)
		}
	}

	if len(r.ConstraintChanges) > 0 {
		fmt.Println("\033[33m=== Constraint Changes ===\033[0m")
		for _, c := range r.ConstraintChanges {
			fmt.Printf("  \033[33m~ %s: %s\033[0m\n", c.Path, c.Description)
		}
	}

	if len(r.Incompatibilities) > 0 {
		fmt.Println("\033[31m=== Incompatibilities ===\033[0m")
		for _, inc := range r.Incompatibilities {
			fmt.Printf("  \033[31m! %s\033[0m\n", inc)
		}
	}
}

func (r *DiffResult) ToJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}
