package ir

import (
	"encoding/json"
	"fmt"
	"strings"
)

type BaseType string

const (
	TypeInt32     BaseType = "int32"
	TypeInt64     BaseType = "int64"
	TypeFloat32   BaseType = "float32"
	TypeFloat64   BaseType = "float64"
	TypeString    BaseType = "string"
	TypeBool      BaseType = "bool"
	TypeBytes     BaseType = "bytes"
	TypeDate      BaseType = "date"
	TypeDateTime  BaseType = "datetime"
	TypeEnum      BaseType = "enum"
	TypeArray     BaseType = "array"
	TypeMap       BaseType = "map"
	TypeStruct    BaseType = "struct"
	TypeUnknown   BaseType = "unknown"
)

type Constraint struct {
	MaxLength *int64       `json:"maxLength,omitempty"`
	MinLength *int64       `json:"minLength,omitempty"`
	Maximum   *float64     `json:"maximum,omitempty"`
	Minimum   *float64     `json:"minimum,omitempty"`
	Pattern   string       `json:"pattern,omitempty"`
	Enum      []string     `json:"enum,omitempty"`
	Format    string       `json:"format,omitempty"`
}

type Field struct {
	Path        string       `json:"path"`
	Name        string       `json:"name"`
	Type        BaseType     `json:"type"`
	Nullable    bool         `json:"nullable"`
	Default     interface{}  `json:"default,omitempty"`
	Constraints *Constraint  `json:"constraints,omitempty"`
	Description string       `json:"description,omitempty"`
	Alias       []string     `json:"alias,omitempty"`
	ItemType    *Field       `json:"itemType,omitempty"`
	KeyType     *Field       `json:"keyType,omitempty"`
	ValueType   *Field       `json:"valueType,omitempty"`
	Fields      []*Field     `json:"fields,omitempty"`
	Namespace   string       `json:"namespace,omitempty"`
	OriginalType string      `json:"originalType,omitempty"`
}

type Schema struct {
	Name        string       `json:"name"`
	Namespace   string       `json:"namespace,omitempty"`
	SourceType  string       `json:"sourceType"`
	Version     string       `json:"version,omitempty"`
	Fields      []*Field     `json:"fields"`
	Description string       `json:"description,omitempty"`
}

func NewSchema(name, sourceType string) *Schema {
	return &Schema{
		Name:       name,
		SourceType: sourceType,
		Fields:     make([]*Field, 0),
	}
}

func NewField(path, name string, typ BaseType) *Field {
	return &Field{
		Path:     path,
		Name:     name,
		Type:     typ,
		Nullable: true,
	}
}

func (f *Field) AddField(child *Field) {
	if f.Fields == nil {
		f.Fields = make([]*Field, 0)
	}
	f.Fields = append(f.Fields, child)
}

func (s *Schema) AllFields() []*Field {
	result := make([]*Field, 0)
	for _, f := range s.Fields {
		result = append(result, collectFields(f)...)
	}
	return result
}

func collectFields(f *Field) []*Field {
	result := []*Field{f}
	if f.Type == TypeStruct && f.Fields != nil {
		for _, child := range f.Fields {
			result = append(result, collectFields(child)...)
		}
	}
	if f.Type == TypeArray && f.ItemType != nil {
		result = append(result, collectFields(f.ItemType)...)
	}
	if f.Type == TypeMap {
		if f.KeyType != nil {
			result = append(result, collectFields(f.KeyType)...)
		}
		if f.ValueType != nil {
			result = append(result, collectFields(f.ValueType)...)
		}
	}
	return result
}

func (s *Schema) FindField(path string) *Field {
	for _, f := range s.AllFields() {
		if f.Path == path {
			return f
		}
	}
	return nil
}

func (f *Field) IsLeaf() bool {
	return f.Type != TypeStruct && f.Type != TypeArray && f.Type != TypeMap
}

func JoinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func (bt BaseType) IsNumeric() bool {
	return bt == TypeInt32 || bt == TypeInt64 || bt == TypeFloat32 || bt == TypeFloat64
}

func (bt BaseType) IsStringLike() bool {
	return bt == TypeString || bt == TypeDate || bt == TypeDateTime
}

func (s *Schema) ToJSON() (string, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Schema) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Schema: %s (source: %s)\n", s.Name, s.SourceType))
	for _, f := range s.Fields {
		sb.WriteString(f.print(0))
	}
	return sb.String()
}

func (f *Field) print(indent int) string {
	var sb strings.Builder
	pad := strings.Repeat("  ", indent)
	sb.WriteString(fmt.Sprintf("%s%s: %s", pad, f.Path, f.Type))
	if f.Nullable {
		sb.WriteString(" (nullable)")
	}
	if f.Description != "" {
		sb.WriteString(fmt.Sprintf(" - %s", f.Description))
	}
	sb.WriteString("\n")
	if f.Fields != nil {
		for _, child := range f.Fields {
			sb.WriteString(child.print(indent + 1))
		}
	}
	if f.ItemType != nil {
		sb.WriteString(fmt.Sprintf("%s  items: ", pad))
		sb.WriteString(f.ItemType.print(indent + 1))
	}
	return sb.String()
}
