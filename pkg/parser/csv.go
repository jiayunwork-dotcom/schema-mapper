package parser

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

type CSVParser struct {
	SampleRows int
}

func NewCSVParser() *CSVParser {
	return &CSVParser{SampleRows: 100}
}

func (p *CSVParser) Parse(content []byte) (*ir.Schema, error) {
	reader := csv.NewReader(strings.NewReader(string(content)))
	return p.parseFromReader(reader, "csv-schema")
}

func (p *CSVParser) ParseFile(filePath string) (*ir.Schema, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, &ParseError{Msg: err.Error(), File: filePath}
	}
	defer f.Close()

	reader := csv.NewReader(f)
	base := filepath.Base(filePath)
	name := strings.TrimSuffix(base, filepath.Ext(base))

	schema, err := p.parseFromReader(reader, name)
	if err != nil {
		if pe, ok := err.(*ParseError); ok {
			pe.File = filePath
		}
		return nil, err
	}
	return schema, nil
}

func (p *CSVParser) parseFromReader(reader *csv.Reader, name string) (*ir.Schema, error) {
	headers, err := reader.Read()
	if err != nil {
		return nil, &ParseError{Msg: "failed to read CSV header: " + err.Error()}
	}

	columns := make([]*columnInfo, len(headers))
	for i, h := range headers {
		columns[i] = &columnInfo{
			Name:     strings.TrimSpace(h),
			Path:     strings.TrimSpace(h),
			Nullable: false,
			Values:   make([]string, 0, p.SampleRows),
		}
	}

	rowCount := 0
	for rowCount < p.SampleRows {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		rowCount++

		for i, col := range columns {
			if i < len(record) {
				val := strings.TrimSpace(record[i])
				if val == "" || strings.EqualFold(val, "null") || strings.EqualFold(val, "na") {
					col.Nullable = true
				} else {
					col.Values = append(col.Values, val)
				}
			} else {
				col.Nullable = true
			}
		}
	}

	schema := ir.NewSchema(name, "csv")
	for _, col := range columns {
		field := col.inferField()
		schema.Fields = append(schema.Fields, field)
	}

	return schema, nil
}

type columnInfo struct {
	Name     string
	Path     string
	Nullable bool
	Values   []string
}

var (
	intPattern     = regexp.MustCompile(`^-?\d+$`)
	floatPattern   = regexp.MustCompile(`^-?\d+\.\d+$`)
	datePatterns   = []*regexp.Regexp{
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`),
		regexp.MustCompile(`^\d{4}/\d{2}/\d{2}$`),
		regexp.MustCompile(`^\d{2}-\d{2}-\d{4}$`),
	}
	datetimePatterns = []*regexp.Regexp{
		regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:?\d{2})?$`),
		regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}$`),
	}
	boolPattern = regexp.MustCompile(`^(true|false|yes|no|1|0)$`)
)

func (ci *columnInfo) inferField() *ir.Field {
	field := ir.NewField(ci.Path, ci.Name, ir.TypeString)
	field.Nullable = ci.Nullable

	if len(ci.Values) == 0 {
		return field
	}

	allMatch := func(pattern *regexp.Regexp) bool {
		for _, v := range ci.Values {
			if !pattern.MatchString(v) {
				return false
			}
		}
		return true
	}

	allMatchDates := func(patterns []*regexp.Regexp) bool {
		for _, v := range ci.Values {
			matched := false
			for _, p := range patterns {
				if p.MatchString(v) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}

	allParseable := func(parseFn func(string) bool) bool {
		for _, v := range ci.Values {
			if !parseFn(v) {
				return false
			}
		}
		return true
	}

	if allMatch(boolPattern) && allParseable(func(s string) bool {
		_, err := strconv.ParseBool(s)
		return err == nil
	}) {
		field.Type = ir.TypeBool
		return field
	}

	if allMatch(intPattern) {
		allInt32 := allParseable(func(s string) bool {
			_, err := strconv.ParseInt(s, 10, 32)
			return err == nil
		})
		if allInt32 {
			field.Type = ir.TypeInt32
		} else {
			field.Type = ir.TypeInt64
		}
		return field
	}

	if allMatch(floatPattern) {
		allFloat32 := allParseable(func(s string) bool {
			_, err := strconv.ParseFloat(s, 32)
			return err == nil
		})
		if allFloat32 {
			field.Type = ir.TypeFloat32
		} else {
			field.Type = ir.TypeFloat64
		}
		return field
	}

	if allMatchDates(datetimePatterns) {
		field.Type = ir.TypeDateTime
		return field
	}

	if allMatchDates(datePatterns) {
		field.Type = ir.TypeDate
		return field
	}

	uniqueVals := make(map[string]bool)
	for _, v := range ci.Values {
		uniqueVals[v] = true
	}
	if len(uniqueVals) <= 10 && len(uniqueVals) > 0 && float64(len(uniqueVals))/float64(len(ci.Values)) < 0.2 {
		field.Type = ir.TypeEnum
		enums := make([]string, 0, len(uniqueVals))
		for k := range uniqueVals {
			enums = append(enums, k)
		}
		field.Constraints = &ir.Constraint{Enum: enums}
		return field
	}

	maxLen := int64(0)
	for _, v := range ci.Values {
		if int64(len(v)) > maxLen {
			maxLen = int64(len(v))
		}
	}
	if maxLen > 0 {
		field.Constraints = &ir.Constraint{MaxLength: &maxLen}
	}

	return field
}

func (p *CSVParser) Supports(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".csv"
}

func tryParseDate(s string) bool {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006/01/02",
	}
	for _, f := range formats {
		if _, err := time.Parse(f, s); err == nil {
			return true
		}
	}
	return false
}
