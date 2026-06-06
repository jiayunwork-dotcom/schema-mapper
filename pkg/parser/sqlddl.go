package parser

import (
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type SQLDDLParser struct{}

var (
	sqlCreateTableRe = regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([\w.]+)\s*\(`)
	sqlColumnRe      = regexp.MustCompile(`(?i)^\s*(\w+)\s+([\w()]+(?:\s+UNSIGNED)?)\s*(.*?)(?:,|$)`)
	sqlConstraintRe  = regexp.MustCompile(`(?i)^\s*(PRIMARY\s+KEY|UNIQUE|FOREIGN\s+KEY|CHECK|CONSTRAINT)\s+`)
	sqlDefaultRe     = regexp.MustCompile(`(?i)DEFAULT\s+(.+?)(?:\s+|$)`)
	sqlCommentRe     = regexp.MustCompile(`(?i)COMMENT\s+'([^']*)'`)
)

func (p *SQLDDLParser) Parse(content []byte) (*ir.Schema, error) {
	text := string(content)

	tableMatch := sqlCreateTableRe.FindStringSubmatch(text)
	if len(tableMatch) < 2 {
		return nil, &ParseError{Msg: "no CREATE TABLE statement found"}
	}

	tableName := tableMatch[1]
	if idx := strings.Index(tableName, "."); idx >= 0 {
		tableName = tableName[idx+1:]
	}

	bodyStart := strings.Index(text, tableMatch[0]) + len(tableMatch[0]) - 1
	bodyEnd := findMatchingParen(text, bodyStart)
	if bodyEnd == -1 {
		return nil, &ParseError{Msg: "malformed CREATE TABLE: missing closing parenthesis"}
	}

	body := text[bodyStart+1 : bodyEnd]
	columns := p.parseColumns(body)

	schema := ir.NewSchema(tableName, "sql-ddl")
	schema.Fields = columns

	return schema, nil
}

func findMatchingParen(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (p *SQLDDLParser) parseColumns(body string) []*ir.Field {
	fields := make([]*ir.Field, 0)
	lines := strings.Split(body, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}

		if sqlConstraintRe.MatchString(line) {
			continue
		}

		if colMatch := sqlColumnRe.FindStringSubmatch(line); len(colMatch) >= 3 {
			name := colMatch[1]
			sqlType := colMatch[2]
			modifiers := colMatch[3]

			field := ir.NewField(name, name, p.sqlTypeToIR(sqlType))
			field.OriginalType = sqlType

			modifiersLower := strings.ToLower(modifiers)
			field.Nullable = !strings.Contains(modifiersLower, "not null")

			if defMatch := sqlDefaultRe.FindStringSubmatch(modifiers); len(defMatch) > 1 {
				field.Default = strings.Trim(defMatch[1], "'\"")
			}

			if commentMatch := sqlCommentRe.FindStringSubmatch(modifiers); len(commentMatch) > 1 {
				field.Description = commentMatch[1]
			}

			if strings.Contains(modifiersLower, "primary key") {
				field.Nullable = false
			}

			fields = append(fields, field)
		}
	}

	return fields
}

func (p *SQLDDLParser) sqlTypeToIR(sqlType string) ir.BaseType {
	typ := strings.ToLower(sqlType)
	typ = regexp.MustCompile(`\(\d+(?:,\d+)?\)`).ReplaceAllString(typ, "")
	typ = strings.TrimSpace(typ)

	switch {
	case strings.Contains(typ, "int"):
		if strings.Contains(typ, "big") {
			return ir.TypeInt64
		}
		return ir.TypeInt32
	case strings.Contains(typ, "float") || strings.Contains(typ, "double") || strings.Contains(typ, "real"):
		if strings.Contains(typ, "double") || strings.Contains(typ, "precision") {
			return ir.TypeFloat64
		}
		return ir.TypeFloat32
	case strings.Contains(typ, "decimal") || strings.Contains(typ, "numeric"):
		return ir.TypeFloat64
	case strings.Contains(typ, "char") || strings.Contains(typ, "text") || strings.Contains(typ, "clob"):
		return ir.TypeString
	case strings.Contains(typ, "bool"):
		return ir.TypeBool
	case strings.Contains(typ, "blob") || strings.Contains(typ, "binary") || strings.Contains(typ, "bytea"):
		return ir.TypeBytes
	case strings.Contains(typ, "timestamp") || strings.Contains(typ, "datetime"):
		return ir.TypeDateTime
	case strings.Contains(typ, "date"):
		return ir.TypeDate
	case strings.Contains(typ, "enum"):
		return ir.TypeEnum
	default:
		return ir.TypeString
	}
}

func (p *SQLDDLParser) ParseFile(filePath string) (*ir.Schema, error) {
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

func (p *SQLDDLParser) Supports(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".sql" || ext == ".ddl"
}
