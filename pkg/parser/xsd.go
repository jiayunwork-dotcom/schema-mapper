package parser

import (
	"encoding/xml"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type XSDParser struct{}

type xsdSchema struct {
	XMLName      xml.Name        `xml:"schema"`
	TargetNS     string          `xml:"targetNamespace,attr"`
	Elements     []xsdElement    `xml:"element"`
	ComplexTypes []xsdComplexType `xml:"complexType"`
	SimpleTypes  []xsdSimpleType  `xml:"simpleType"`
}

type xsdElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	MinOccurs   string          `xml:"minOccurs,attr"`
	MaxOccurs   string          `xml:"maxOccurs,attr"`
	ComplexType *xsdComplexType `xml:"complexType"`
	SimpleType  *xsdSimpleType  `xml:"simpleType"`
	Annotation  *xsdAnnotation  `xml:"annotation"`
}

type xsdComplexType struct {
	Name      string       `xml:"name,attr"`
	Sequence  *xsdSequence `xml:"sequence"`
	Choice    *xsdChoice   `xml:"choice"`
	All       *xsdSequence `xml:"all"`
	Attributes []xsdAttribute `xml:"attribute"`
}

type xsdSequence struct {
	Elements []xsdElement `xml:"element"`
}

type xsdChoice struct {
	Elements []xsdElement `xml:"element"`
	MinOccurs string      `xml:"minOccurs,attr"`
	MaxOccurs string      `xml:"maxOccurs,attr"`
}

type xsdAttribute struct {
	Name       string         `xml:"name,attr"`
	Type       string         `xml:"type,attr"`
	Use        string         `xml:"use,attr"`
	SimpleType *xsdSimpleType `xml:"simpleType"`
	Annotation *xsdAnnotation `xml:"annotation"`
}

type xsdSimpleType struct {
	Name        string          `xml:"name,attr"`
	Restriction *xsdRestriction `xml:"restriction"`
}

type xsdRestriction struct {
	Base         string           `xml:"base,attr"`
	MinLength    *xsdMinMaxLength `xml:"minLength"`
	MaxLength    *xsdMinMaxLength `xml:"maxLength"`
	MinInclusive *xsdMinMax       `xml:"minInclusive"`
	MaxInclusive *xsdMinMax       `xml:"maxInclusive"`
	Pattern      *xsdPattern      `xml:"pattern"`
	Enumerations []xsdEnumeration `xml:"enumeration"`
}

type xsdMinMaxLength struct {
	Value int64 `xml:"value,attr"`
}

type xsdMinMax struct {
	Value float64 `xml:"value,attr"`
}

type xsdPattern struct {
	Value string `xml:"value,attr"`
}

type xsdEnumeration struct {
	Value string `xml:"value,attr"`
}

type xsdAnnotation struct {
	Documentation string `xml:"documentation"`
}

func (p *XSDParser) Parse(content []byte) (*ir.Schema, error) {
	var schema xsdSchema
	if err := xml.Unmarshal(content, &schema); err != nil {
		return nil, &ParseError{Msg: "invalid XSD: " + err.Error()}
	}

	types := make(map[string]interface{})
	for _, st := range schema.SimpleTypes {
		types[st.Name] = st
	}
	for _, ct := range schema.ComplexTypes {
		types[ct.Name] = ct
	}

	s := ir.NewSchema("xsd-schema", "xsd")
	s.Namespace = schema.TargetNS

	for _, elem := range schema.Elements {
		field := p.parseElement(elem, "", types)
		if field != nil {
			s.Fields = append(s.Fields, field)
		}
	}

	return s, nil
}

func (p *XSDParser) parseElement(elem xsdElement, parentPath string, types map[string]interface{}) *ir.Field {
	path := ir.JoinPath(parentPath, elem.Name)
	field := ir.NewField(path, elem.Name, ir.TypeString)

	field.Nullable = elem.MinOccurs != "1"
	if elem.MaxOccurs == "unbounded" || elem.MaxOccurs > "1" {
		field.Type = ir.TypeArray
		field.ItemType = &ir.Field{
			Path: path + ".items",
			Name: "items",
		}
	}

	if elem.Annotation != nil {
		field.Description = elem.Annotation.Documentation
	}

	if elem.ComplexType != nil {
		field.Type = ir.TypeStruct
		p.parseComplexType(elem.ComplexType, path, field, types)
		return field
	}

	if elem.SimpleType != nil {
		p.applySimpleType(elem.SimpleType, field)
		return field
	}

	if elem.Type != "" {
		typ := p.resolveXSDType(elem.Type, types)
		field.Type = typ
		if t, ok := types[elem.Type]; ok {
			if ct, ok := t.(xsdComplexType); ok {
				field.Type = ir.TypeStruct
				p.parseComplexType(&ct, path, field, types)
			} else if st, ok := t.(xsdSimpleType); ok {
				p.applySimpleType(&st, field)
			}
		}
	}

	return field
}

func (p *XSDParser) parseComplexType(ct *xsdComplexType, parentPath string, parent *ir.Field, types map[string]interface{}) {
	seq := ct.Sequence
	if seq == nil {
		seq = ct.All
	}
	if seq != nil {
		for _, child := range seq.Elements {
			childField := p.parseElement(child, parentPath, types)
			if childField != nil {
				parent.AddField(childField)
			}
		}
	}

	for _, attr := range ct.Attributes {
		path := ir.JoinPath(parentPath, attr.Name)
		field := ir.NewField(path, attr.Name, p.resolveXSDType(attr.Type, types))
		field.Nullable = attr.Use != "required"
		if attr.Annotation != nil {
			field.Description = attr.Annotation.Documentation
		}
		if attr.SimpleType != nil {
			p.applySimpleType(attr.SimpleType, field)
		}
		parent.AddField(field)
	}
}

func (p *XSDParser) applySimpleType(st *xsdSimpleType, field *ir.Field) {
	if st.Restriction != nil {
		r := st.Restriction
		field.Type = p.resolveXSDType(r.Base, nil)
		constraints := &ir.Constraint{}
		hasConstraints := false

		if r.MinLength != nil {
			constraints.MinLength = &r.MinLength.Value
			hasConstraints = true
		}
		if r.MaxLength != nil {
			constraints.MaxLength = &r.MaxLength.Value
			hasConstraints = true
		}
		if r.MinInclusive != nil {
			constraints.Minimum = &r.MinInclusive.Value
			hasConstraints = true
		}
		if r.MaxInclusive != nil {
			constraints.Maximum = &r.MaxInclusive.Value
			hasConstraints = true
		}
		if r.Pattern != nil {
			constraints.Pattern = r.Pattern.Value
			hasConstraints = true
		}
		if len(r.Enumerations) > 0 {
			field.Type = ir.TypeEnum
			enums := make([]string, len(r.Enumerations))
			for i, e := range r.Enumerations {
				enums[i] = e.Value
			}
			constraints.Enum = enums
			hasConstraints = true
		}

		if hasConstraints {
			field.Constraints = constraints
		}
	}
}

func (p *XSDParser) resolveXSDType(xsdType string, types map[string]interface{}) ir.BaseType {
	typ := strings.TrimPrefix(xsdType, "xs:")
	typ = strings.TrimPrefix(typ, "xsd:")

	switch strings.ToLower(typ) {
	case "int", "integer", "short", "byte":
		return ir.TypeInt32
	case "long", "negativeinteger", "nonnegativeinteger", "positiveinteger", "nonpositiveinteger":
		return ir.TypeInt64
	case "float", "decimal":
		return ir.TypeFloat32
	case "double":
		return ir.TypeFloat64
	case "string", "normalizedstring", "token", "nmtoken", "nmtokens":
		return ir.TypeString
	case "boolean":
		return ir.TypeBool
	case "base64binary", "hexbinary":
		return ir.TypeBytes
	case "date":
		return ir.TypeDate
	case "datetime", "time", "duration", "gyearmonth", "gyear", "gmonthday", "gday", "gmonth":
		return ir.TypeDateTime
	default:
		if types != nil {
			if _, ok := types[typ]; ok {
				return ir.TypeStruct
			}
		}
		return ir.TypeString
	}
}

func (p *XSDParser) ParseFile(filePath string) (*ir.Schema, error) {
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
		if schema.Name == "xsd-schema" || schema.Name == "" {
			base := filepath.Base(filePath)
			schema.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return schema, nil
}

func (p *XSDParser) Supports(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".xsd" || ext == ".xs"
}
