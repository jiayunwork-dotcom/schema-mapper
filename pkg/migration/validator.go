package migration

import (
	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/registry"
)

func ValidateMigrationScript(script *MigrationScript, reg *registry.Registry) []ValidationError {
	errors := make([]ValidationError, 0)

	fromSchema, err := reg.GetSchema(script.SchemaName, script.FromVersion)
	if err != nil {
		errors = append(errors, ValidationError{
			OperationIndex: -1,
			Message:        "cannot load from version schema: " + err.Error(),
		})
		return errors
	}

	toSchema, err := reg.GetSchema(script.SchemaName, script.ToVersion)
	if err != nil {
		errors = append(errors, ValidationError{
			OperationIndex: -1,
			Message:        "cannot load to version schema: " + err.Error(),
		})
		return errors
	}

	fieldState := make(map[string]bool)
	for _, f := range fromSchema.AllFields() {
		if f.IsLeaf() {
			fieldState[f.Path] = true
		}
	}

	renamedFields := make(map[string]string)

	for i, op := range script.Operations {
		fieldPath := op.FieldPath

		if currentName, ok := renamedFields[fieldPath]; ok {
			fieldPath = currentName
		}

		switch op.Op {
		case OpAddField:
			if fieldState[fieldPath] {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "field already exists: " + fieldPath,
				})
			}

			field := toSchema.FindField(fieldPath)
			if field != nil && !field.Nullable && field.Default == nil && op.DefaultValue == nil {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "NOT NULL field requires default_value: " + fieldPath,
				})
			}

			fieldState[fieldPath] = true

		case OpRemoveField:
			if !fieldState[fieldPath] {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "field does not exist in source schema: " + fieldPath,
				})
			}
			fieldState[fieldPath] = false

		case OpRenameField:
			if !fieldState[fieldPath] {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "field does not exist in source schema: " + fieldPath,
				})
			}

			newPath := op.NewFieldPath
			if fieldState[newPath] {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "target field already exists: " + newPath,
				})
			}

			fieldState[fieldPath] = false
			fieldState[newPath] = true
			renamedFields[fieldPath] = newPath

			for k, v := range renamedFields {
				if v == fieldPath {
					renamedFields[k] = newPath
				}
			}

		case OpChangeType:
			if !fieldState[fieldPath] {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "field does not exist: " + fieldPath,
				})
			}

			oldType := ir.BaseType(op.OldType)
			newType := ir.BaseType(op.NewType)
			if !IsTypeSafe(oldType, newType) && op.FallbackValue == nil {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "unsafe type change requires fallback_value: " + fieldPath,
				})
			}

		case OpAddConstraint, OpRemoveConstraint:
			if !fieldState[fieldPath] {
				errors = append(errors, ValidationError{
					OperationIndex: i,
					Message:        "field does not exist: " + fieldPath,
				})
			}
		}
	}

	errors = append(errors, checkDependencyConflicts(script)...)

	return errors
}

func checkDependencyConflicts(script *MigrationScript) []ValidationError {
	errors := make([]ValidationError, 0)

	for i := 0; i < len(script.Operations); i++ {
		for j := i + 1; j < len(script.Operations); j++ {
			op1 := script.Operations[i]
			op2 := script.Operations[j]

			if op1.Op == OpRenameField && op2.Op == OpChangeType {
				if op1.NewFieldPath == op2.FieldPath {
					errors = append(errors, ValidationError{
						OperationIndex: j,
						Message: "dependency conflict: rename at [" +
							op1.FieldPath + " -> " + op1.NewFieldPath +
							"] should come after change_type at [" + op2.FieldPath + "]",
					})
				}
			}

			if op1.Op == OpChangeType && op2.Op == OpRenameField {
				if op1.FieldPath == op2.FieldPath {
					errors = append(errors, ValidationError{
						OperationIndex: j,
						Message: "dependency conflict: change_type at [" + op1.FieldPath +
							"] should come before rename at [" + op2.FieldPath + " -> " + op2.NewFieldPath + "]",
					})
				}
			}

			if op1.Op == OpRenameField && op2.Op == OpRenameField {
				if op1.NewFieldPath == op2.FieldPath {
					errors = append(errors, ValidationError{
						OperationIndex: j,
						Message: "rename chaining detected: [" + op1.FieldPath + " -> " + op1.NewFieldPath +
							"] and [" + op2.FieldPath + " -> " + op2.NewFieldPath + "], should combine into single rename",
					})
				}
			}

			if op1.Op == OpRemoveField && op2.Op != OpRemoveField {
				if op1.FieldPath == op2.FieldPath {
					errors = append(errors, ValidationError{
						OperationIndex: j,
						Message: "field " + op1.FieldPath + " was removed at operation[" +
							string(rune(i)) + "], cannot be used in operation[" + string(rune(j)) + "]",
					})
				}
			}
		}
	}

	return errors
}
