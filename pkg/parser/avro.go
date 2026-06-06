package parser

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type AvroParser struct{}

type avroSchemaDoc struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Namespace   string                 `json:"namespace"`
	Doc         string                 `json:"doc"`
	Fields      []avroField            `json:"fields"`
	Items       interface{}            `json:"items"`
	Values      interface{}            `json:"values"`
	Symbols     []string               `json:"symbols"`
	Size        int                    `json:"size"`
	Aliases     []string               `json:"aliases"`
	LogicalType string                 `json:"logicalType"`
}

type avroField struct {
	Name    string      `json:"name"`
	Type    interface{} `json:"type"`
	Doc     string      `json:"doc"`
	Default interface{} `json:"default"`
	Aliases []string    `json:"aliases"`
}

func (p *AvroParser) Parse(content []byte) (*ir.Schema, error) {
	var doc avroSchemaDoc
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, &ParseError{Msg: fmt.Sprintf("invalid Avro JSON: %v", err)}
	}

	name := doc.Name
	if name == "" {
		name = "avro-schema"
	}

	schema := ir.NewSchema(name, "avro")
	schema.Namespace = doc.Namespace
	schema.Description = doc.Doc

	for _, field := range doc.Fields {
		f := p.parseField(field, "", schema.Namespace)
		if f != nil {
			schema.Fields = append(schema.Fields, f)
		}
	}

	return schema, nil
}

func (p *AvroParser) parseField(af avroField, parentPath, namespace string) *ir.Field {
	path := ir.JoinPath(parentPath, af.Name)
	typ, nullable := p.resolveType(af.Type, path, namespace)

	field := ir.NewField(path, af.Name, typ)
	field.Nullable = nullable
	field.Description = af.Doc
	field.Default = af.Default
	field.Alias = af.Aliases
	field.Namespace = namespace

	return field
}

func (p *AvroParser) resolveType(t interface{}, parentPath, namespace string) (ir.BaseType, bool) {
	switch v := t.(type) {
	case string:
		if v == "null" {
			return ir.TypeString, true
		}
		return p.avroPrimitiveToIR(v), false
	case []interface{}:
		nullable := false
		var nonNullType ir.BaseType = ir.TypeUnknown
		for _, opt := range v {
			if opt == "null" {
				nullable = true
			} else if str, ok := opt.(string); ok {
				nonNullType = p.avroPrimitiveToIR(str)
			} else if _, ok := opt.(map[string]interface{}); ok {
				nonNullType = ir.TypeStruct
			}
		}
		return nonNullType, nullable
	case map[string]interface{}:
		typeStr, _ := v["type"].(string)
		switch typeStr {
		case "record":
			return ir.TypeStruct, false
		case "array":
			return ir.TypeArray, false
		case "map":
			return ir.TypeMap, false
		case "enum":
			return ir.TypeEnum, false
		case "fixed":
			return ir.TypeBytes, false
		default:
			return p.avroPrimitiveToIR(typeStr), false
		}
	}
	return ir.TypeUnknown, false
}

func (p *AvroParser) avroPrimitiveToIR(avroType string) ir.BaseType {
	switch strings.ToLower(avroType) {
	case "int":
		return ir.TypeInt32
	case "long":
		return ir.TypeInt64
	case "float":
		return ir.TypeFloat32
	case "double":
		return ir.TypeFloat64
	case "string":
		return ir.TypeString
	case "boolean":
		return ir.TypeBool
	case "bytes":
		return ir.TypeBytes
	case "record":
		return ir.TypeStruct
	case "array":
		return ir.TypeArray
	case "map":
		return ir.TypeMap
	case "enum":
		return ir.TypeEnum
	case "fixed":
		return ir.TypeBytes
	case "timestamp-millis", "timestamp-micros":
		return ir.TypeDateTime
	case "date":
		return ir.TypeDate
	default:
		return ir.TypeUnknown
	}
}

func (p *AvroParser) ParseFile(filePath string) (*ir.Schema, error) {
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
		if schema.Name == "avro-schema" || schema.Name == "" {
			base := filepath.Base(filePath)
			schema.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return schema, nil
}

func (p *AvroParser) Supports(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".avsc" || ext == ".avro"
}
