package parser

import (
	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type Parser interface {
	Parse(content []byte) (*ir.Schema, error)
	ParseFile(filePath string) (*ir.Schema, error)
	Supports(filePath string) bool
}

type ParserRegistry struct {
	parsers []Parser
}

func NewParserRegistry() *ParserRegistry {
	pr := &ParserRegistry{
		parsers: make([]Parser, 0),
	}
	pr.Register(&JSONSchemaParser{})
	pr.Register(&AvroParser{})
	pr.Register(&ProtobufParser{})
	pr.Register(&SQLDDLParser{})
	pr.Register(NewCSVParser())
	pr.Register(&XSDParser{})
	return pr
}

func (pr *ParserRegistry) Register(p Parser) {
	pr.parsers = append(pr.parsers, p)
}

func (pr *ParserRegistry) GetParserForFile(filePath string) Parser {
	for _, p := range pr.parsers {
		if p.Supports(filePath) {
			return p
		}
	}
	return nil
}

func (pr *ParserRegistry) ParseFile(filePath string, format string) (*ir.Schema, error) {
	if format != "" {
		switch format {
		case "json-schema", "jsonschema":
			return (&JSONSchemaParser{}).ParseFile(filePath)
		case "avro":
			return (&AvroParser{}).ParseFile(filePath)
		case "protobuf", "proto":
			return (&ProtobufParser{}).ParseFile(filePath)
		case "sql", "ddl":
			return (&SQLDDLParser{}).ParseFile(filePath)
		case "csv":
			return NewCSVParser().ParseFile(filePath)
		case "xsd", "xml-schema":
			return (&XSDParser{}).ParseFile(filePath)
		}
	}
	parser := pr.GetParserForFile(filePath)
	if parser == nil {
		return nil, ErrUnsupportedFormat
	}
	return parser.ParseFile(filePath)
}

var ErrUnsupportedFormat = &ParseError{Msg: "unsupported file format"}

type ParseError struct {
	Msg  string
	File string
	Line int
}

func (e *ParseError) Error() string {
	if e.File != "" {
		return e.File + ": " + e.Msg
	}
	return e.Msg
}
