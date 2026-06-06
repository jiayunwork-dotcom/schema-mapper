package parser

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type JSONSchemaParser struct{}

type jsonSchemaDoc struct {
	Schema      string                 `json:"$schema"`
	ID          string                 `json:"$id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Type        interface{}            `json:"type"`
	Properties  map[string]*jsonSchemaDoc `json:"properties"`
	Items       *jsonSchemaDoc         `json:"items"`
	Required    []string               `json:"required"`
	Default     interface{}            `json:"default"`
	Enum        []interface{}          `json:"enum"`
	Format      string                 `json:"format"`
	MinLength   *int64                 `json:"minLength"`
	MaxLength   *int64                 `json:"maxLength"`
	Minimum     *float64               `json:"minimum"`
	Maximum     *float64               `json:"maximum"`
	Pattern     string                 `json:"pattern"`
	Ref         string                 `json:"$ref"`
	Definitions map[string]*jsonSchemaDoc `json:"definitions"`
	OneOf       []*jsonSchemaDoc       `json:"oneOf"`
	AnyOf       []*jsonSchemaDoc       `json:"anyOf"`
	AllOf       []*jsonSchemaDoc       `json:"allOf"`
}

func (p *JSONSchemaParser) Parse(content []byte) (*ir.Schema, error) {
	var doc jsonSchemaDoc
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, &ParseError{Msg: fmt.Sprintf("invalid JSON: %v", err)}
	}

	name := doc.Title
	if name == "" {
		name = doc.ID
	}
	if name == "" {
		name = "schema"
	}

	schema := ir.NewSchema(name, "json-schema")
	schema.Description = doc.Description
	schema.Version = doc.Schema

	for propName, prop := range doc.Properties {
		field := p.parseProperty(propName, prop, "", doc.Definitions)
		if field != nil {
			nullable := true
			for _, req := range doc.Required {
				if req == propName {
					nullable = false
					break
				}
			}
			field.Nullable = nullable
			schema.Fields = append(schema.Fields, field)
		}
	}

	return schema, nil
}

func (p *JSONSchemaParser) parseProperty(name string, prop *jsonSchemaDoc, parentPath string, defs map[string]*jsonSchemaDoc) *ir.Field {
	path := ir.JoinPath(parentPath, name)

	if prop.Ref != "" {
		ref := strings.TrimPrefix(prop.Ref, "#/definitions/")
		if def, ok := defs[ref]; ok {
			prop = def
		}
	}

	typ := p.resolveType(prop)
	field := ir.NewField(path, name, typ)
	field.Description = prop.Description
	field.Default = prop.Default
	field.OriginalType = fmt.Sprintf("%v", prop.Type)

	if len(prop.Enum) > 0 {
		field.Type = ir.TypeEnum
		enumVals := make([]string, len(prop.Enum))
		for i, v := range prop.Enum {
			enumVals[i] = fmt.Sprintf("%v", v)
		}
		if field.Constraints == nil {
			field.Constraints = &ir.Constraint{}
		}
		field.Constraints.Enum = enumVals
	}

	constraints := &ir.Constraint{}
	hasConstraints := false
	if prop.Format != "" {
		constraints.Format = prop.Format
		hasConstraints = true
		if prop.Format == "date-time" {
			field.Type = ir.TypeDateTime
		} else if prop.Format == "date" {
			field.Type = ir.TypeDate
		} else if prop.Format == "email" || prop.Format == "uri" {
			constraints.Format = prop.Format
			hasConstraints = true
		}
	}
	if prop.MinLength != nil {
		constraints.MinLength = prop.MinLength
		hasConstraints = true
	}
	if prop.MaxLength != nil {
		constraints.MaxLength = prop.MaxLength
		hasConstraints = true
	}
	if prop.Minimum != nil {
		constraints.Minimum = prop.Minimum
		hasConstraints = true
	}
	if prop.Maximum != nil {
		constraints.Maximum = prop.Maximum
		hasConstraints = true
	}
	if prop.Pattern != "" {
		constraints.Pattern = prop.Pattern
		hasConstraints = true
	}
	if hasConstraints {
		field.Constraints = constraints
	}

	if typ == ir.TypeArray && prop.Items != nil {
		itemField := p.parseProperty("items", prop.Items, path, defs)
		if itemField != nil {
			field.ItemType = itemField
		}
	}

	if typ == ir.TypeStruct && prop.Properties != nil {
		for childName, childProp := range prop.Properties {
			childField := p.parseProperty(childName, childProp, path, defs)
			if childField != nil {
				childNullable := true
				for _, req := range prop.Required {
					if req == childName {
						childNullable = false
						break
					}
				}
				childField.Nullable = childNullable
				field.AddField(childField)
			}
		}
	}

	return field
}

func (p *JSONSchemaParser) resolveType(prop *jsonSchemaDoc) ir.BaseType {
	typeStr := ""
	switch t := prop.Type.(type) {
	case string:
		typeStr = t
	case []interface{}:
		for _, v := range t {
			if str, ok := v.(string); ok && str != "null" {
				typeStr = str
				break
			}
		}
	}

	if typeStr == "" && len(prop.OneOf) > 0 {
		for _, opt := range prop.OneOf {
			if t := p.resolveType(opt); t != ir.TypeUnknown {
				return t
			}
		}
	}

	if typeStr == "" && prop.Properties != nil {
		return ir.TypeStruct
	}

	switch typeStr {
	case "integer":
		return ir.TypeInt64
	case "number":
		return ir.TypeFloat64
	case "string":
		return ir.TypeString
	case "boolean":
		return ir.TypeBool
	case "array":
		return ir.TypeArray
	case "object":
		return ir.TypeStruct
	case "null":
		return ir.TypeString
	default:
		return ir.TypeUnknown
	}
}

func (p *JSONSchemaParser) ParseFile(filePath string) (*ir.Schema, error) {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, &ParseError{Msg: err.Error(), File: filePath}
	}
	schema, err := p.Parse(content)
	if err != nil {
		if pe, ok := err.(*ParseError); ok {
			pe.File = filePath
		}
		return nil, err
	}
	if schema != nil {
		if schema.Name == "schema" || schema.Name == "" {
			base := filepath.Base(filePath)
			schema.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return schema, nil
}

func (p *JSONSchemaParser) Supports(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".json" || ext == ".jsonschema" || ext == ".schema"
}
