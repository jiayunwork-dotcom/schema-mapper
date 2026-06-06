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
	OpAddField         OperationType = "add_field"
	OpRemoveField      OperationType = "remove_field"
	OpRenameField      OperationType = "rename_field"
	OpChangeType       OperationType = "change_type"
	OpAddConstraint    OperationType = "add_constraint"
	OpRemoveConstraint OperationType = "remove_constraint"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type MigrationOperation struct {
	Op            OperationType  `yaml:"op" json:"op"`
	FieldPath     string         `yaml:"fieldPath" json:"fieldPath"`
	NewFieldPath  string         `yaml:"newFieldPath,omitempty" json:"newFieldPath,omitempty"`
	NewType       string         `yaml:"newType,omitempty" json:"newType,omitempty"`
	OldType       string         `yaml:"oldType,omitempty" json:"oldType,omitempty"`
	DefaultValue  interface{}    `yaml:"defaultValue,omitempty" json:"defaultValue,omitempty"`
	FallbackValue interface{}    `yaml:"fallbackValue,omitempty" json:"fallbackValue,omitempty"`
	RiskLevel     RiskLevel      `yaml:"riskLevel,omitempty" json:"riskLevel,omitempty"`
	Constraint    *ir.Constraint `yaml:"constraint,omitempty" json:"constraint,omitempty"`
	Description   string         `yaml:"description,omitempty" json:"description,omitempty"`
}

type MigrationScript struct {
	SchemaName  string                `yaml:"schemaName" json:"schemaName"`
	FromVersion string                `yaml:"fromVersion" json:"fromVersion"`
	ToVersion   string                `yaml:"toVersion" json:"toVersion"`
	Operations  []*MigrationOperation `yaml:"operations" json:"operations"`
	CreatedAt   string                `yaml:"createdAt" json:"createdAt"`
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
	AffectedFields    int       `json:"affectedFields"`
	HasBreakingChange bool      `json:"hasBreakingChange"`
	BreakingChanges   []string  `json:"breakingChanges,omitempty"`
	MigrationStrategy string    `json:"migrationStrategy"`
	RiskLevel         RiskLevel `json:"riskLevel"`
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

type ExecutionState string

const (
	StatePending   ExecutionState = "pending"
	StatePartial   ExecutionState = "partial"
	StateCompleted ExecutionState = "completed"
	StateFailed    ExecutionState = "failed"
)

type RiskStats struct {
	Low    int `yaml:"low" json:"low"`
	Medium int `yaml:"medium" json:"medium"`
	High   int `yaml:"high" json:"high"`
}

type FieldQualityInfo struct {
	FieldPath        string         `yaml:"fieldPath" json:"fieldPath"`
	NonNullRate      float64        `yaml:"nonNullRate" json:"nonNullRate"`
	TypeDistribution map[string]int `yaml:"typeDistribution" json:"typeDistribution"`
	ExpectedType     string         `yaml:"expectedType,omitempty" json:"expectedType,omitempty"`
	HasTypeMismatch  bool           `yaml:"hasTypeMismatch" json:"hasTypeMismatch"`
}

type DataQualityWarning struct {
	FieldPath string `yaml:"fieldPath" json:"fieldPath"`
	Message   string `yaml:"message" json:"message"`
	Severity  string `yaml:"severity" json:"severity"`
}

type MigrationPlan struct {
	SchemaName               string                `yaml:"schemaName" json:"schemaName"`
	FromVersion              string                `yaml:"fromVersion" json:"fromVersion"`
	ToVersion                string                `yaml:"toVersion" json:"toVersion"`
	TotalSteps               int                   `yaml:"totalSteps" json:"totalSteps"`
	RiskStats                RiskStats             `yaml:"riskStats" json:"riskStats"`
	EstimatedAffectedRecords int64                 `yaml:"estimatedAffectedRecords,omitempty" json:"estimatedAffectedRecords,omitempty"`
	EstimatedSuccessRate     float64               `yaml:"estimatedSuccessRate,omitempty" json:"estimatedSuccessRate,omitempty"`
	ExecutionState           string                `yaml:"executionState" json:"executionState"`
	Operations               []*MigrationOperation `yaml:"operations" json:"operations"`
	CreatedAt                string                `yaml:"createdAt" json:"createdAt"`
	FieldQualityInfo         []*FieldQualityInfo   `yaml:"fieldQualityInfo,omitempty" json:"fieldQualityInfo,omitempty"`
	DataQualityWarnings      []*DataQualityWarning `yaml:"dataQualityWarnings,omitempty" json:"dataQualityWarnings,omitempty"`
}

func (mp *MigrationPlan) Save(filePath string) error {
	data, err := yaml.Marshal(mp)
	if err != nil {
		return fmt.Errorf("failed to marshal migration plan: %w", err)
	}
	if err := ioutil.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write migration plan: %w", err)
	}
	return nil
}

func LoadMigrationPlan(filePath string) (*MigrationPlan, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration plan: %w", err)
	}

	var mp MigrationPlan
	if err := yaml.Unmarshal(data, &mp); err != nil {
		return nil, fmt.Errorf("failed to parse migration plan: %w", err)
	}
	return &mp, nil
}

func (mp *MigrationPlan) ToJSON() (string, error) {
	b, err := json.MarshalIndent(mp, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type RollbackOperation struct {
	Op            OperationType `yaml:"op" json:"op"`
	FieldPath     string        `yaml:"fieldPath" json:"fieldPath"`
	NewFieldPath  string        `yaml:"newFieldPath,omitempty" json:"newFieldPath,omitempty"`
	NewType       string        `yaml:"newType,omitempty" json:"newType,omitempty"`
	OldType       string        `yaml:"oldType,omitempty" json:"oldType,omitempty"`
	DefaultValue  interface{}   `yaml:"defaultValue,omitempty" json:"defaultValue,omitempty"`
	ValueSnapshot interface{}   `yaml:"valueSnapshot,omitempty" json:"valueSnapshot,omitempty"`
	Description   string        `yaml:"description,omitempty" json:"description,omitempty"`
}

type RollbackScript struct {
	PlanPath   string               `yaml:"planPath" json:"planPath"`
	FromStep   int                  `yaml:"fromStep" json:"fromStep"`
	ToStep     int                  `yaml:"toStep" json:"toStep"`
	Operations []*RollbackOperation `yaml:"operations" json:"operations"`
	CreatedAt  string               `yaml:"createdAt" json:"createdAt"`
	SourceFile string               `yaml:"sourceFile" json:"sourceFile"`
}

func (rs *RollbackScript) Save(filePath string) error {
	data, err := yaml.Marshal(rs)
	if err != nil {
		return fmt.Errorf("failed to marshal rollback script: %w", err)
	}
	if err := ioutil.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write rollback script: %w", err)
	}
	return nil
}

func LoadRollbackScript(filePath string) (*RollbackScript, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read rollback script: %w", err)
	}

	var rs RollbackScript
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("failed to parse rollback script: %w", err)
	}
	return &rs, nil
}

type OperationDryRunStats struct {
	StepIndex      int                 `yaml:"stepIndex" json:"stepIndex"`
	Operation      *MigrationOperation `yaml:"operation" json:"operation"`
	TotalRecords   int64               `yaml:"totalRecords" json:"totalRecords"`
	SuccessRecords int64               `yaml:"successRecords" json:"successRecords"`
	FailedRecords  int64               `yaml:"failedRecords" json:"failedRecords"`
	SkippedFields  int64               `yaml:"skippedFields" json:"skippedFields"`
	PassRate       float64             `yaml:"passRate" json:"passRate"`
	FailureReasons map[string]int      `yaml:"failureReasons" json:"failureReasons"`
	TopFailures    []*FailureReason    `yaml:"topFailures" json:"topFailures"`
}

type FailureReason struct {
	Reason string `yaml:"reason" json:"reason"`
	Count  int    `yaml:"count" json:"count"`
}

type Suggestion struct {
	StepIndex   int     `yaml:"stepIndex" json:"stepIndex"`
	FailureRate float64 `yaml:"failureRate" json:"failureRate"`
	TopReason   string  `yaml:"topReason" json:"topReason"`
	Action      string  `yaml:"action" json:"action"`
}

type DryRunReport struct {
	PlanPath                string                  `yaml:"planPath" json:"planPath"`
	SourceFile              string                  `yaml:"sourceFile" json:"sourceFile"`
	TotalRecords            int64                   `yaml:"totalRecords" json:"totalRecords"`
	SuccessRecords          int64                   `yaml:"successRecords" json:"successRecords"`
	FailedRecords           int64                   `yaml:"failedRecords" json:"failedRecords"`
	SkippedFieldStats       map[string]int64        `yaml:"skippedFieldStats" json:"skippedFieldStats"`
	MissingFallbackFailures int64                   `yaml:"missingFallbackFailures" json:"missingFallbackFailures"`
	OperationResults        []*OperationDryRunStats `yaml:"operationResults" json:"operationResults"`
	Recommendations         []*Suggestion           `yaml:"recommendations,omitempty" json:"recommendations,omitempty"`
	CreatedAt               string                  `yaml:"createdAt" json:"createdAt"`
}

func (dr *DryRunReport) ToJSON() (string, error) {
	b, err := json.MarshalIndent(dr, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (dr *DryRunReport) Save(filePath string) error {
	data, err := json.MarshalIndent(dr, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dry-run report: %w", err)
	}
	if err := ioutil.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write dry-run report: %w", err)
	}
	return nil
}
