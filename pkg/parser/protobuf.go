package parser

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type ProtobufParser struct{}

type protoMessage struct {
	Name   string
	Fields []*ir.Field
	Nested []*protoMessage
	Enums  []*protoEnum
}

type protoEnum struct {
	Name    string
	Values  []string
}

var (
	protoSyntaxRe   = regexp.MustCompile(`syntax\s*=\s*"([^"]+)"`)
	protoPackageRe  = regexp.MustCompile(`package\s+([\w.]+)\s*;`)
	protoMessageRe  = regexp.MustCompile(`message\s+(\w+)\s*\{`)
	protoEnumRe     = regexp.MustCompile(`enum\s+(\w+)\s*\{`)
	protoFieldRe    = regexp.MustCompile(`(optional|repeated|required)?\s*(\w+)\s+(\w+)\s*=\s*(\d+)\s*(\[[^\]]+\])?\s*;`)
	protoOneofRe    = regexp.MustCompile(`oneof\s+(\w+)\s*\{`)
)

func (p *ProtobufParser) Parse(content []byte) (*ir.Schema, error) {
	text := string(content)

	syntaxMatch := protoSyntaxRe.FindStringSubmatch(text)
	if len(syntaxMatch) > 1 && syntaxMatch[1] != "proto3" {
		return nil, &ParseError{Msg: fmt.Sprintf("unsupported proto syntax: %s (only proto3 is supported)", syntaxMatch[1])}
	}

	pkgMatch := protoPackageRe.FindStringSubmatch(text)
	namespace := ""
	if len(pkgMatch) > 1 {
		namespace = pkgMatch[1]
	}

	messages := p.parseMessages(text, namespace)
	if len(messages) == 0 {
		return nil, &ParseError{Msg: "no message definitions found"}
	}

	topMsg := messages[0]
	schema := ir.NewSchema(topMsg.Name, "protobuf")
	schema.Namespace = namespace
	schema.Fields = topMsg.Fields

	return schema, nil
}

func (p *ProtobufParser) parseMessages(text, namespace string) []*protoMessage {
	messages := make([]*protoMessage, 0)
	lines := strings.Split(text, "\n")
	stack := make([]*protoMessage, 0)
	currentEnum := (*protoEnum)(nil)
	enumDepth := 0

	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])

		if currentEnum != nil {
			if strings.Contains(line, "{") {
				enumDepth++
			}
			if strings.Contains(line, "}") {
				enumDepth--
				if enumDepth == 0 {
					currentEnum = nil
				}
			} else {
				enumValRe := regexp.MustCompile(`(\w+)\s*=\s*\d+\s*;`)
				if m := enumValRe.FindStringSubmatch(line); len(m) > 1 {
					currentEnum.Values = append(currentEnum.Values, m[1])
				}
			}
			i++
			continue
		}

		if msgMatch := protoMessageRe.FindStringSubmatch(line); len(msgMatch) > 1 {
			msg := &protoMessage{
				Name:   msgMatch[1],
				Fields: make([]*ir.Field, 0),
			}
			if len(stack) > 0 {
				stack[len(stack)-1].Nested = append(stack[len(stack)-1].Nested, msg)
			}
			stack = append(stack, msg)
			depth := strings.Count(line, "{") - strings.Count(line, "}")
			i++
			for depth > 0 && i < len(lines) {
				depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
				i++
			}
			i--
		} else if enumMatch := protoEnumRe.FindStringSubmatch(line); len(enumMatch) > 1 {
			enum := &protoEnum{
				Name: enumMatch[1],
			}
			if len(stack) > 0 {
				stack[len(stack)-1].Enums = append(stack[len(stack)-1].Enums, enum)
			}
			currentEnum = enum
			enumDepth = strings.Count(line, "{") - strings.Count(line, "}")
			i++
			continue
		} else if fieldMatch := protoFieldRe.FindStringSubmatch(line); len(fieldMatch) > 4 {
			label := fieldMatch[1]
			typ := fieldMatch[2]
			name := fieldMatch[3]

			if len(stack) > 0 {
				field := p.parseProtoField(name, typ, label, namespace)
				if field != nil {
					stack[len(stack)-1].Fields = append(stack[len(stack)-1].Fields, field)
				}
			}
		}

		if strings.Contains(line, "}") && len(stack) > 0 {
			messages = append(messages, stack[len(stack)-1])
			stack = stack[:len(stack)-1]
		}

		i++
	}

	return messages
}

func (p *ProtobufParser) parseProtoField(name, typ, label, namespace string) *ir.Field {
	field := ir.NewField(name, name, p.protoTypeToIR(typ))
	field.Namespace = namespace
	field.OriginalType = typ

	if label == "repeated" {
		field.Type = ir.TypeArray
		field.ItemType = ir.NewField(name+".items", "items", p.protoTypeToIR(typ))
	}
	field.Nullable = label != "required"

	return field
}

func (p *ProtobufParser) protoTypeToIR(protoType string) ir.BaseType {
	switch strings.ToLower(protoType) {
	case "int32", "sint32", "uint32", "fixed32", "sfixed32":
		return ir.TypeInt32
	case "int64", "sint64", "uint64", "fixed64", "sfixed64":
		return ir.TypeInt64
	case "float":
		return ir.TypeFloat32
	case "double":
		return ir.TypeFloat64
	case "string":
		return ir.TypeString
	case "bool":
		return ir.TypeBool
	case "bytes":
		return ir.TypeBytes
	case "timestamp":
		return ir.TypeDateTime
	default:
		return ir.TypeStruct
	}
}

func (p *ProtobufParser) ParseFile(filePath string) (*ir.Schema, error) {
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
		if schema.Name == "" {
			base := filepath.Base(filePath)
			schema.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return schema, nil
}

func (p *ProtobufParser) Supports(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".proto"
}
