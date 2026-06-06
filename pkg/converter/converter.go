package converter

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

type DataFormat string

const (
	FormatJSON    DataFormat = "json"
	FormatCSV     DataFormat = "csv"
	FormatXML     DataFormat = "xml"
	FormatNDJSON  DataFormat = "ndjson"
	FormatParquet DataFormat = "parquet"
)

const (
	StreamThreshold = 100 * 1024 * 1024
	MemoryLimit   = 256 * 1024 * 1024
)

type ConversionOptions struct {
	InputFormat  DataFormat
	OutputFormat DataFormat
	ShowProgress bool
}

type ConversionResult struct {
	TotalRecords   int64
	SuccessRecords   int64
	FailedRecords    int64
	SkippedRecords  int64
	Errors       []*mapper.TransformError
	ErrorSummary map[string]int
	Duration     time.Duration
	OutputPath   string
}

type DataConverter struct {
	engine *mapper.RuleEngine
	opts   ConversionOptions
}

func NewDataConverter(engine *mapper.RuleEngine, opts ConversionOptions) *DataConverter {
	return &DataConverter{
		engine: engine,
		opts:   opts,
	}
}

func (dc *DataConverter) ConvertFile(inputPath, mappingPath, outputPath string) (*ConversionResult, error) {
	rules, err := mapper.LoadMappingRules(mappingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load mapping rules: %w", err)
	}

	engine := mapper.NewRuleEngine(rules)
	dc.engine = engine

	fileInfo, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}

	inputFormat := dc.opts.InputFormat
	if inputFormat == "" {
		inputFormat = detectFormat(inputPath)
	}

	outputFormat := dc.opts.OutputFormat
	if outputFormat == "" {
		outputFormat = detectFormat(outputPath)
	}

	useStreaming := fileInfo.Size() > StreamThreshold

	if useStreaming {
		return dc.convertStreaming(inputPath, outputPath, inputFormat, outputFormat)
	}

	return dc.convertInMemory(inputPath, outputPath, inputFormat, outputFormat)
}

func detectFormat(path string) DataFormat {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return FormatJSON
	case ".csv":
		return FormatCSV
	case ".xml":
		return FormatXML
	case ".ndjson", ".jsonl":
		return FormatNDJSON
	case ".parquet":
		return FormatParquet
	default:
		return FormatJSON
	}
}

func (dc *DataConverter) convertInMemory(inputPath, outputPath string, inputFormat, outputFormat DataFormat) (*ConversionResult, error) {
	start := time.Now()
	result := &ConversionResult{
		ErrorSummary: make(map[string]int),
		Errors:       make([]*mapper.TransformError, 0),
	}

	data, err := readAll(inputPath, inputFormat)
	if err != nil {
		return nil, err
	}

	records, ok := data.([]interface{})
	if !ok {
		records = []interface{}{data}
	}

	total := int64(len(records))
	result.TotalRecords = total

	var bar *progressbar.ProgressBar
	if dc.opts.ShowProgress && total > 0 {
		bar = progressbar.Default(total, "Converting")
	}

	outputData := make([]interface{}, 0, total)

	for i, record := range records {
		recordMap, ok := record.(map[string]interface{})
		if !ok {
			result.FailedRecords++
			continue
		}

		transformed, errs := dc.engine.Transform(recordMap)
		if len(errs) > 0 {
			for _, e := range errs {
				result.Errors = append(result.Errors, &mapper.TransformError{
					SourcePath: e.SourcePath,
					TargetPath: e.TargetPath,
					Message:    e.Message,
					RowIndex:   i,
				})
				result.ErrorSummary[e.Message]++
			}
		}

		outputData = append(outputData, transformed)
		result.SuccessRecords++

		if bar != nil {
			bar.Add(1)
		}
	}

	if bar != nil {
		bar.Finish()
	}

	if err := writeAll(outputData, outputPath, outputFormat); err != nil {
		return nil, err
	}

	result.Duration = time.Since(start)
	result.OutputPath = outputPath

	return result, nil
}

func (dc *DataConverter) convertStreaming(inputPath, outputPath string, inputFormat, outputFormat DataFormat) (*ConversionResult, error) {
	start := time.Now()
	result := &ConversionResult{
		ErrorSummary: make(map[string]int),
		Errors:       make([]*mapper.TransformError, 0),
	}

	reader, err := newStreamReader(inputPath, inputFormat)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	writer, err := newStreamWriter(outputPath, outputFormat)
	if err != nil {
		return nil, err
	}
	defer writer.Close()

	var bar *progressbar.ProgressBar
	if dc.opts.ShowProgress {
		bar = progressbar.Default(-1, "Converting (streaming)")
	}

	rowIndex := int64(0)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.FailedRecords++
			continue
		}

		recordMap, ok := record.(map[string]interface{})
		if !ok {
			result.FailedRecords++
			continue
		}

		transformed, errs := dc.engine.Transform(recordMap)
		if len(errs) > 0 {
			for _, e := range errs {
				te := &mapper.TransformError{
					SourcePath: e.SourcePath,
					TargetPath: e.TargetPath,
					Message:    e.Message,
					RowIndex:   int(rowIndex),
				}
				if len(result.Errors) < 100 {
					result.Errors = append(result.Errors, te)
				}
				result.ErrorSummary[e.Message]++
			}
		}

		if err := writer.Write(transformed); err != nil {
			result.FailedRecords++
			continue
		}

		rowIndex++
		result.TotalRecords++
		result.SuccessRecords++

		if bar != nil {
			bar.Add(1)
		}
	}

	if bar != nil {
		bar.Finish()
	}

	result.Duration = time.Since(start)
	result.OutputPath = outputPath

	return result, nil
}

type StreamReader interface {
	Read() (interface{}, error)
	Close() error
}

type StreamWriter interface {
	Write(interface{}) error
	Close() error
}

func readAll(path string, format DataFormat) (interface{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	switch format {
	case FormatJSON:
		var data interface{}
		decoder := json.NewDecoder(f)
		if err := decoder.Decode(&data); err != nil {
			return nil, fmt.Errorf("failed to decode JSON: %w", err)
		}
		return data, nil

	case FormatNDJSON:
		records := make([]interface{}, 0)
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var record interface{}
			if err := json.Unmarshal(line, &record); err != nil {
				return nil, fmt.Errorf("failed to decode NDJSON: %w", err)
			}
			records = append(records, record)
		}
		return records, scanner.Err()

	case FormatCSV:
		reader := csv.NewReader(f)
		headers, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV header: %w", err)
		}
		records := make([]interface{}, 0)
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read CSV row: %w", err)
			}
			record := make(map[string]interface{})
			for i, h := range headers {
				if i < len(row) {
					record[h] = row[i]
				}
			}
			records = append(records, record)
		}
		return records, nil

	case FormatXML:
		return readXML(f)

	case FormatParquet:
		return nil, fmt.Errorf("parquet format requires streaming mode only")

	default:
		return nil, fmt.Errorf("unsupported input format: %s", format)
	}
}

func writeAll(data interface{}, path string, format DataFormat) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	records, ok := data.([]interface{})
	if !ok {
		records = []interface{}{data}
	}

	switch format {
	case FormatJSON:
		encoder := json.NewEncoder(f)
		encoder.SetIndent("", "  ")
		if len(records) == 1 {
			return encoder.Encode(records[0])
		}
		return encoder.Encode(records)

	case FormatNDJSON:
		encoder := json.NewEncoder(f)
		for _, r := range records {
			if err := encoder.Encode(r); err != nil {
				return err
			}
		}
		return nil

	case FormatCSV:
		return writeCSV(f, records)

	case FormatXML:
		return writeXML(f, records)

	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

func newStreamReader(path string, format DataFormat) (StreamReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	switch format {
	case FormatNDJSON:
		return &ndjsonReader{f: f, scanner: bufio.NewScanner(f)}, nil
	case FormatJSON:
		return &jsonArrayReader{f: f, decoder: json.NewDecoder(f)}, nil
	case FormatCSV:
		reader := csv.NewReader(f)
		headers, err := reader.Read()
		if err != nil {
			f.Close()
			return nil, err
		}
		return &csvStreamReader{f: f, reader: reader, headers: headers}, nil
	default:
		f.Close()
		return nil, fmt.Errorf("streaming not supported for format: %s", format)
	}
}

func newStreamWriter(path string, format DataFormat) (StreamWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	switch format {
	case FormatNDJSON:
		return &ndjsonWriter{f: f, encoder: json.NewEncoder(f)}, nil
	case FormatJSON:
		return &jsonArrayWriter{f: f, encoder: json.NewEncoder(f), first: true}, nil
	case FormatCSV:
		return nil, fmt.Errorf("CSV streaming writer not implemented for streaming")
	default:
		f.Close()
		return nil, fmt.Errorf("streaming not supported for format: %s", format)
	}
}

type ndjsonReader struct {
	f       *os.File
	scanner *bufio.Scanner
}

func (r *ndjsonReader) Read() (interface{}, error) {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	line := r.scanner.Bytes()
	if len(line) == 0 {
		return r.Read()
	}
	var record interface{}
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, err
	}
	return record, nil
}

func (r *ndjsonReader) Close() error {
	return r.f.Close()
}

type jsonArrayReader struct {
	f       *os.File
	decoder *json.Decoder
	inArray bool
	first   bool
}

func (r *jsonArrayReader) Read() (interface{}, error) {
	if !r.inArray {
		t, err := r.decoder.Token()
		if err != nil {
			return nil, err
		}
		if delim, ok := t.(json.Delim); ok && delim == '[' {
			r.inArray = true
		} else {
			return t, io.EOF
		}
	}

	if !r.decoder.More() {
		var record interface{}
		if err := r.decoder.Decode(&record); err != nil {
			return nil, err
		}
		return record, nil
	}

	return nil, io.EOF
}

func (r *jsonArrayReader) Close() error {
	return r.f.Close()
}

type csvStreamReader struct {
	f       *os.File
	reader  *csv.Reader
	headers []string
}

func (r *csvStreamReader) Read() (interface{}, error) {
	row, err := r.reader.Read()
	if err != nil {
		return nil, err
	}
	record := make(map[string]interface{})
	for i, h := range r.headers {
		if i < len(row) {
			record[h] = row[i]
		}
	}
	return record, nil
}

func (r *csvStreamReader) Close() error {
	return r.f.Close()
}

type ndjsonWriter struct {
	f       *os.File
	encoder *json.Encoder
}

func (w *ndjsonWriter) Write(data interface{}) error {
	return w.encoder.Encode(data)
}

func (w *ndjsonWriter) Close() error {
	return w.f.Close()
}

type jsonArrayWriter struct {
	f       *os.File
	encoder *json.Encoder
	first   bool
}

func (w *jsonArrayWriter) Write(data interface{}) error {
	if w.first {
		w.f.WriteString("[\n")
		w.first = false
	} else {
		w.f.WriteString(",\n")
	}
	return w.encoder.Encode(data)
}

func (w *jsonArrayWriter) Close() error {
	w.f.WriteString("\n]")
	return w.f.Close()
}

type xmlRecord struct {
	XMLName xml.Name
	Content string `xml:",chardata"`
	Attrs   []xml.Attr `xml:",any,attr"`
	Children []xmlRecord `xml:",any"`
}

func readXML(f *os.File) (interface{}, error) {
	var records []interface{}
	decoder := xml.NewDecoder(f)
	for {
		var rec xmlRecord
		err := decoder.Decode(&rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		records = append(records, xmlToMap(rec))
	}
	return records, nil
}

func xmlToMap(r xmlRecord) map[string]interface{} {
	m := make(map[string]interface{})
	for _, attr := range r.Attrs {
		m[attr.Name.Local] = attr.Value
	}
	for _, child := range r.Children {
		if len(child.Children) == 0 && child.Content != "" {
			m[child.XMLName.Local] = child.Content
		} else {
			m[child.XMLName.Local] = xmlToMap(child)
		}
	}
	if r.Content != "" && strings.TrimSpace(r.Content) != "" {
		m["_content"] = strings.TrimSpace(r.Content)
	}
	return m
}

func writeXML(f *os.File, records []interface{}) error {
	f.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	f.WriteString("<records>\n")
	encoder := xml.NewEncoder(f)
	encoder.Indent("", "  ")
	for _, r := range records {
		f.WriteString("  <record>\n")
		writeXMLRecord(f, r, "    ")
		f.WriteString("  </record>\n")
	}
	f.WriteString("</records>\n")
	return nil
}

func writeXMLRecord(f *os.File, data interface{}, indent string) {
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			switch val := val.(type) {
			case map[string]interface{}:
				fmt.Fprintf(f, "%s<%s>\n", indent, key)
				writeXMLRecord(f, val, indent+"  ")
				fmt.Fprintf(f, "%s</%s>\n", indent, key)
			case []interface{}:
				for _, item := range val {
					fmt.Fprintf(f, "%s<%s>\n", indent, key)
					writeXMLRecord(f, item, indent+"  ")
					fmt.Fprintf(f, "%s</%s>\n", indent, key)
				}
			default:
				fmt.Fprintf(f, "%s<%s>%v</%s>\n", indent, key, xmlEscape(fmt.Sprintf("%v", val)), key)
			}
		}
	default:
		fmt.Fprintf(f, "%s%v\n", indent, xmlEscape(fmt.Sprintf("%v", v)))
	}
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func writeCSV(f *os.File, records []interface{}) error {
	writer := csv.NewWriter(f)
	defer writer.Flush()

	if len(records) == 0 {
		return nil
	}

	first, ok := records[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid record format")
	}

	headers := make([]string, 0, len(first))
	for k := range first {
		headers = append(headers, k)
	}

	if err := writer.Write(headers); err != nil {
		return err
	}

	for _, r := range records {
		record, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		row := make([]string, len(headers))
		for i, h := range headers {
			if val, ok := record[h]; ok {
				row[i] = fmt.Sprintf("%v", val)
			}
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

func (r *ConversionResult) PrintSummary() {
	fmt.Printf("\n=== Conversion Summary ===\n")
	fmt.Printf("Total records:   %d\n", r.TotalRecords)
	fmt.Printf("Success:         %d\n", r.SuccessRecords)
	fmt.Printf("Failed:          %d\n", r.FailedRecords)
	fmt.Printf("Skipped:       %d\n", r.SkippedRecords)
	fmt.Printf("Duration:      %v\n", r.Duration)
	fmt.Printf("Output file:   %s\n", r.OutputPath)

	if len(r.ErrorSummary) > 0 {
		fmt.Printf("\n=== Error Summary ===\n")
		for msg, count := range r.ErrorSummary {
			fmt.Printf("  %dx: %s\n", count, msg)
		}

		if len(r.Errors) > 0 {
			fmt.Printf("\nSample errors (first %d):\n", min(len(r.Errors), len(r.Errors)))
			for _, e := range r.Errors {
				fmt.Printf("  Row %d: %s -> %s: %s\n", e.RowIndex, e.SourcePath, e.TargetPath, e.Message)
			}
		}
	}
}

func (r *ConversionResult) ToJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
