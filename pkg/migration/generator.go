package migration

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/schema-mapper/schema-mapper/pkg/diff"
	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/registry"
)

type RenameCandidate struct {
	OldPath string
	NewPath string
	Score   float64
}

func DetectRenames(removed, added []diff.FieldDiff, threshold float64) []RenameCandidate {
	candidates := make([]RenameCandidate, 0)
	usedOld := make(map[string]bool)
	usedNew := make(map[string]bool)

	for _, r := range removed {
		oldName := path.Base(r.Path)
		bestScore := 0.0
		bestAdd := -1

		for j, a := range added {
			if usedNew[a.Path] {
				continue
			}
			if r.OldType != a.NewType {
				continue
			}
			newName := path.Base(a.Path)
			sim := LevenshteinSimilarity(oldName, newName)
			if sim > bestScore && sim > threshold {
				bestScore = sim
				bestAdd = j
			}
		}

		if bestAdd >= 0 {
			candidates = append(candidates, RenameCandidate{
				OldPath: r.Path,
				NewPath: added[bestAdd].Path,
				Score:   bestScore,
			})
			usedOld[r.Path] = true
			usedNew[added[bestAdd].Path] = true
		}
	}

	return candidates
}

func IsTypeSafe(oldType, newType ir.BaseType) bool {
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

func GenerateMigrationScript(reg *registry.Registry, schemaName, fromV, toV string) (*MigrationScript, *MigrationImpact, error) {
	fromSchema, err := reg.GetSchema(schemaName, fromV)
	if err != nil {
		return nil, nil, err
	}

	toSchema, err := reg.GetSchema(schemaName, toV)
	if err != nil {
		return nil, nil, err
	}

	intermediateVersions, err := reg.GetVersionsBetween(schemaName, fromV, toV)
	if err != nil {
		return nil, nil, err
	}

	renameChains := make(map[string]string)
	if len(intermediateVersions) > 0 {
		renameChains, err = collectRenameChains(reg, schemaName, fromV, toV, intermediateVersions)
		if err != nil {
			return nil, nil, err
		}
	}

	diffResult := diff.CompareSchemas(fromSchema, toSchema)

	renames := DetectRenames(diffResult.Removed, diffResult.Added, 0.7)

	for oldPath, newPath := range renameChains {
		found := false
		for _, r := range renames {
			if r.OldPath == oldPath {
				found = true
				break
			}
		}
		if !found {
			renames = append(renames, RenameCandidate{
				OldPath: oldPath,
				NewPath: newPath,
				Score:   1.0,
			})
		}
	}

	operations := make([]*MigrationOperation, 0)
	removedPaths := make(map[string]bool)
	addedPaths := make(map[string]bool)

	for _, r := range renames {
		removedPaths[r.OldPath] = true
		addedPaths[r.NewPath] = true
		operations = append(operations, &MigrationOperation{
			Op:           OpRenameField,
			FieldPath:    r.OldPath,
			NewFieldPath: r.NewPath,
			RiskLevel:    RiskLow,
			Description:  fmt.Sprintf("Rename field %s -> %s (similarity: %.2f)", r.OldPath, r.NewPath, r.Score),
		})
	}

	for _, r := range diffResult.Removed {
		if removedPaths[r.Path] {
			continue
		}
		operations = append(operations, &MigrationOperation{
			Op:          OpRemoveField,
			FieldPath:   r.Path,
			OldType:     r.OldType,
			RiskLevel:   RiskMedium,
			Description: fmt.Sprintf("Remove field %s", r.Path),
		})
	}

	for _, a := range diffResult.Added {
		if addedPaths[a.Path] {
			continue
		}

		field := toSchema.FindField(a.Path)
		op := &MigrationOperation{
			Op:          OpAddField,
			FieldPath:   a.Path,
			NewType:     a.NewType,
			Description: fmt.Sprintf("Add field %s", a.Path),
		}

		if field != nil && !field.Nullable && field.Default == nil {
			op.DefaultValue = nil
			op.RiskLevel = RiskHigh
			op.Description += " (NOT NULL, requires default value)"
		} else {
			op.RiskLevel = RiskLow
			if field != nil && field.Default != nil {
				op.DefaultValue = field.Default
			}
		}

		operations = append(operations, op)
	}

	for _, tc := range diffResult.TypeChanges {
		oldType := ir.BaseType(tc.OldType)
		newType := ir.BaseType(tc.NewType)
		safe := IsTypeSafe(oldType, newType)

		op := &MigrationOperation{
			Op:          OpChangeType,
			FieldPath:   tc.Path,
			OldType:     tc.OldType,
			NewType:     tc.NewType,
			Description: fmt.Sprintf("Change type %s: %s -> %s", tc.Path, tc.OldType, tc.NewType),
		}

		if safe {
			op.RiskLevel = RiskLow
		} else {
			op.RiskLevel = RiskHigh
			op.FallbackValue = nil
			op.Description += " (unsafe conversion, requires fallback value)"
		}

		operations = append(operations, op)
	}

	for _, cc := range diffResult.ConstraintChanges {
		desc := strings.ToLower(cc.Description)
		if strings.Contains(desc, "added") || strings.Contains(desc, "changed") {
			field := toSchema.FindField(cc.Path)
			if field != nil && field.Constraints != nil {
				operations = append(operations, &MigrationOperation{
					Op:          OpAddConstraint,
					FieldPath:   cc.Path,
					Constraint:  field.Constraints,
					RiskLevel:   RiskMedium,
					Description: fmt.Sprintf("Add/Update constraint on %s: %s", cc.Path, cc.Description),
				})
			}
		} else if strings.Contains(desc, "removed") {
			operations = append(operations, &MigrationOperation{
				Op:          OpRemoveConstraint,
				FieldPath:   cc.Path,
				RiskLevel:   RiskLow,
				Description: fmt.Sprintf("Remove constraint on %s: %s", cc.Path, cc.Description),
			})
		}
	}

	impact := assessImpact(diffResult, operations)

	script := &MigrationScript{
		SchemaName:  schemaName,
		FromVersion: fromV,
		ToVersion:   toV,
		Operations:  operations,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}

	return script, impact, nil
}

func collectRenameChains(reg *registry.Registry, schemaName, fromV, toV string, intermediates []*registry.SemVer) (map[string]string, error) {
	renameChains := make(map[string]string)
	reverseChains := make(map[string]string)

	allVersions := make([]string, 0, len(intermediates)+2)
	allVersions = append(allVersions, fromV)
	for _, v := range intermediates {
		allVersions = append(allVersions, v.String())
	}
	allVersions = append(allVersions, toV)

	for i := 0; i < len(allVersions)-1; i++ {
		v1 := allVersions[i]
		v2 := allVersions[i+1]

		s1, err := reg.GetSchema(schemaName, v1)
		if err != nil {
			return nil, err
		}
		s2, err := reg.GetSchema(schemaName, v2)
		if err != nil {
			return nil, err
		}

		d := diff.CompareSchemas(s1, s2)
		renames := DetectRenames(d.Removed, d.Added, 0.7)

		for _, r := range renames {
			oldPath := r.OldPath
			if orig, ok := reverseChains[oldPath]; ok {
				oldPath = orig
			} else {
				reverseChains[r.NewPath] = oldPath
			}
			renameChains[oldPath] = r.NewPath
			reverseChains[r.NewPath] = oldPath
		}
	}

	return renameChains, nil
}

func assessImpact(dr *diff.DiffResult, ops []*MigrationOperation) *MigrationImpact {
	impact := &MigrationImpact{
		AffectedFields:  len(dr.Added) + len(dr.Removed) + len(dr.TypeChanges) + len(dr.ConstraintChanges),
		BreakingChanges: make([]string, 0),
	}

	impact.HasBreakingChange = len(dr.Incompatibilities) > 0
	impact.BreakingChanges = append(impact.BreakingChanges, dr.Incompatibilities...)

	for _, op := range ops {
		if op.RiskLevel == RiskHigh {
			impact.HasBreakingChange = true
		}
	}

	if !impact.HasBreakingChange && len(ops) == 0 {
		impact.MigrationStrategy = StrategyAuto
		impact.RiskLevel = RiskLow
	} else if !impact.HasBreakingChange {
		impact.MigrationStrategy = StrategyAuto
		impact.RiskLevel = RiskLow
	} else if len(impact.BreakingChanges) > 3 {
		impact.MigrationStrategy = StrategyManual
		impact.RiskLevel = RiskHigh
	} else {
		impact.MigrationStrategy = StrategySemiAuto
		impact.RiskLevel = RiskMedium
	}

	return impact
}
