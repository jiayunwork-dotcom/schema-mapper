package migration

import (
	"encoding/json"
	"fmt"
	"io/ioutil"

	"gopkg.in/yaml.v3"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type OperationType string

const (
	OpAddField      OperationType = "add_field"
	OpRemoveField   OperationType = "remove_field"
	OpRenameField   OperationType = "rename_field"
	OpChangeType    OperationType = "change_type"
	OpAddConstraint OperationType = "add_constraint"
	OpRemoveConstraint OperationType = "remove_constraint"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type MigrationOperation struct {
	Op            OperationType   `yaml:"op" json:"op"`
	FieldPath     string          `yaml:"fieldPath" json:"fieldPath"`
	NewFieldPath  string          `yaml:"newFieldPath,omitempty" json:"newFieldPath,omitempty"`
	NewType       string          `yaml:"newType,omitempty" json:"newType,omitempty"`
	OldType       string          `yaml:"oldType,omitempty" json:"oldType,omitempty"`
	DefaultValue  interface{}     `yaml:"defaultValue,omitempty" json:"defaultValue,omitempty"`
	FallbackValue interface{}     `yaml:"fallbackValue,omitempty" json:"fallbackValue,omitempty"`
	RiskLevel     RiskLevel       `yaml:"riskLevel,omitempty" json:"riskLevel,omitempty"`
	Constraint    *ir.Constraint  `yaml:"constraint,omitempty" json:"constraint,omitempty"`
	Description   string          `yaml:"description,omitempty" json:"description,omitempty"`
}

type MigrationScript struct {
	SchemaName string              `yaml:"schemaName" json:"schemaName"`
	FromVersion string             `yaml:"fromVersion" json:"fromVersion"`
	ToVersion   string             `yaml:"toVersion" json:"toVersion"`
	Operations  []*MigrationOperation `yaml:"operations" json:"operations"`
	CreatedAt   string             `yaml:"createdAt" json:"createdAt"`
}

func (ms *MigrationScript) Save(filePath string) error {
	data, err := yaml.Marshal(ms)
	if err != nil {
		return fmt.Errorf("failed to marshal migration script: %w", err)
	}
	if err := ioutil.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write migration script: %w", err)
	}
	return nil
}

func LoadMigrationScript(filePath string) (*MigrationScript, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration script: %w", err)
	}

	var ms MigrationScript
	if err := yaml.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse migration script: %w", err)
	}
	return &ms, nil
}

func (ms *MigrationScript) ToJSON() (string, error) {
	b, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (ms *MigrationScript) ToYAML() (string, error) {
	b, err := yaml.Marshal(ms)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type MigrationImpact struct {
	AffectedFields   int       `json:"affectedFields"`
	HasBreakingChange bool      `json:"hasBreakingChange"`
	BreakingChanges  []string  `json:"breakingChanges,omitempty"`
	MigrationStrategy string    `json:"migrationStrategy"`
	RiskLevel        RiskLevel `json:"riskLevel"`
}

const (
	StrategyAuto     = "auto"
	StrategySemiAuto = "semi-automatic"
	StrategyManual   = "manual"
)

type ValidationError struct {
	OperationIndex int
	Message        string
}

func (ve ValidationError) String() string {
	return fmt.Sprintf("operation[%d]: %s", ve.OperationIndex, ve.Message)
}
