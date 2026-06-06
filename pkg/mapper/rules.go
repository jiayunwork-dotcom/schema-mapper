package mapper

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type TransformType string

const (
	TransformDirect     TransformType = "direct"
	TransformCast       TransformType = "cast"
	TransformFormat     TransformType = "format"
	TransformConcat     TransformType = "concat"
	TransformSplit      TransformType = "split"
	TransformLookup     TransformType = "lookup"
	TransformExpression TransformType = "expression"
	TransformDefault    TransformType = "default"
	TransformNested     TransformType = "nested"
	TransformArrayMap   TransformType = "array_map"
	TransformConditional TransformType = "conditional"
)

type MappingRule struct {
	Source      string                 `yaml:"source" json:"source"`
	Target      string                 `yaml:"target" json:"target"`
	Transform   TransformType          `yaml:"transform" json:"transform"`
	Format      string                 `yaml:"format,omitempty" json:"format,omitempty"`
	Separator   string                 `yaml:"separator,omitempty" json:"separator,omitempty"`
	Sources     []string               `yaml:"sources,omitempty" json:"sources,omitempty"`
	Targets     []string               `yaml:"targets,omitempty" json:"targets,omitempty"`
	LookupTable map[string]interface{} `yaml:"lookup_table,omitempty" json:"lookupTable,omitempty"`
	Expression  string                 `yaml:"expression,omitempty" json:"expression,omitempty"`
	Default     interface{}            `yaml:"default,omitempty" json:"default,omitempty"`
	Condition   *Condition             `yaml:"condition,omitempty" json:"condition,omitempty"`
	Mappings    []*MappingRule         `yaml:"mappings,omitempty" json:"mappings,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	NeedsReview bool                   `yaml:"needs_review,omitempty" json:"needsReview,omitempty"`
	Confidence  float64                `yaml:"confidence,omitempty" json:"confidence,omitempty"`
	MatchType   string                 `yaml:"match_type,omitempty" json:"matchType,omitempty"`
	Warnings    []string               `yaml:"warnings,omitempty" json:"warnings,omitempty"`
}

type Condition struct {
	Field string      `yaml:"field" json:"field"`
	Op    string      `yaml:"op" json:"op"`
	Value interface{} `yaml:"value" json:"value"`
	Then  string      `yaml:"then" json:"then"`
	Else  string      `yaml:"else,omitempty" json:"else,omitempty"`
}

type MappingRules struct {
	SourceSchema string         `yaml:"source_schema,omitempty" json:"sourceSchema,omitempty"`
	TargetSchema string         `yaml:"target_schema,omitempty" json:"targetSchema,omitempty"`
	Mappings     []*MappingRule `yaml:"mappings" json:"mappings"`
}

type TransformContext struct {
	SourceData map[string]interface{}
	RowIndex   int
	Errors     []*TransformError
}

type TransformError struct {
	SourcePath string
	TargetPath string
	Message    string
	RowIndex   int
}

func LoadMappingRules(filePath string) (*MappingRules, error) {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read mapping file: %w", err)
	}

	var rules MappingRules
	if err := yaml.Unmarshal(content, &rules); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &rules, nil
}

func (r *MappingRules) Save(filePath string) error {
	content, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	return ioutil.WriteFile(filePath, content, 0644)
}

type RuleEngine struct {
	rules *MappingRules
}

func NewRuleEngine(rules *MappingRules) *RuleEngine {
	return &RuleEngine{rules: rules}
}

func (re *RuleEngine) Transform(sourceData map[string]interface{}) (map[string]interface{}, []*TransformError) {
	ctx := &TransformContext{
		SourceData: sourceData,
		Errors:     make([]*TransformError, 0),
	}

	result := make(map[string]interface{})
	for _, rule := range re.rules.Mappings {
		re.applyRule(rule, ctx, result)
	}

	return result, ctx.Errors
}

func (re *RuleEngine) applyRule(rule *MappingRule, ctx *TransformContext, result map[string]interface{}) {
	var value interface{}
	var err error

	switch rule.Transform {
	case TransformDirect:
		value, err = re.transformDirect(rule, ctx)
	case TransformCast:
		value, err = re.transformCast(rule, ctx)
	case TransformFormat:
		value, err = re.transformFormat(rule, ctx)
	case TransformConcat:
		value, err = re.transformConcat(rule, ctx)
	case TransformSplit:
		values, err := re.transformSplit(rule, ctx)
		if err != nil {
			ctx.Errors = append(ctx.Errors, &TransformError{
				SourcePath: rule.Source,
				TargetPath: rule.Target,
				Message:    err.Error(),
				RowIndex:   ctx.RowIndex,
			})
			return
		}
		for i, target := range rule.Targets {
			if i < len(values) {
				setNestedValue(result, target, values[i])
			}
		}
		return
	case TransformLookup:
		value, err = re.transformLookup(rule, ctx)
	case TransformExpression:
		value, err = re.transformExpression(rule, ctx)
	case TransformDefault:
		value, err = re.transformDefault(rule, ctx)
	case TransformNested:
		value, err = re.transformNested(rule, ctx)
	case TransformArrayMap:
		value, err = re.transformArrayMap(rule, ctx)
	case TransformConditional:
		value, err = re.transformConditional(rule, ctx)
	default:
		err = fmt.Errorf("unknown transform type: %s", rule.Transform)
	}

	if err != nil {
		ctx.Errors = append(ctx.Errors, &TransformError{
			SourcePath: rule.Source,
			TargetPath: rule.Target,
			Message:    err.Error(),
			RowIndex:   ctx.RowIndex,
		})
		if rule.Default != nil {
			value = rule.Default
		} else {
			value = nil
		}
	}

	setNestedValue(result, rule.Target, value)
}

func getNestedValue(data map[string]interface{}, path string) (interface{}, bool) {
	return GetNestedValue(data, path)
}

func GetNestedValue(data map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	var current interface{} = data

	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current, ok = m[part]
			if !ok {
				return nil, false
			}
		} else {
			return nil, false
		}
	}

	return current, true
}

func setNestedValue(data map[string]interface{}, path string, value interface{}) {
	SetNestedValue(data, path, value)
}

func SetNestedValue(data map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}

		if next, ok := current[part]; ok {
			if m, ok := next.(map[string]interface{}); ok {
				current = m
			} else {
				newMap := make(map[string]interface{})
				current[part] = newMap
				current = newMap
			}
		} else {
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		}
	}
}

func (re *RuleEngine) transformDirect(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok {
		if rule.Default != nil {
			return rule.Default, nil
		}
		return nil, fmt.Errorf("source field not found: %s", rule.Source)
	}
	return val, nil
}

func (re *RuleEngine) transformCast(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok {
		if rule.Default != nil {
			return rule.Default, nil
		}
		return nil, fmt.Errorf("source field not found: %s", rule.Source)
	}

	if val == nil {
		return nil, nil
	}

	targetType := rule.Format
	if targetType == "" {
		return val, nil
	}

	strVal := fmt.Sprintf("%v", val)
	switch strings.ToLower(targetType) {
	case "int", "int32", "integer":
		i, err := strconv.ParseInt(strVal, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to int32: %w", strVal, err)
		}
		return int32(i), nil
	case "int64", "long":
		i, err := strconv.ParseInt(strVal, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to int64: %w", strVal, err)
		}
		return i, nil
	case "float", "float32":
		f, err := strconv.ParseFloat(strVal, 32)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to float32: %w", strVal, err)
		}
		return float32(f), nil
	case "float64", "double":
		f, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to float64: %w", strVal, err)
		}
		return f, nil
	case "string", "str":
		return strVal, nil
	case "bool", "boolean":
		b, err := strconv.ParseBool(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to bool: %w", strVal, err)
		}
		return b, nil
	case "date":
		t, err := parseDate(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to date: %w", strVal, err)
		}
		return t.Format("2006-01-02"), nil
	case "datetime", "timestamp":
		t, err := parseDate(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to datetime: %w", strVal, err)
		}
		return t.Format(time.RFC3339), nil
	case "unix":
		t, err := parseDate(strVal)
		if err != nil {
			return nil, fmt.Errorf("cannot cast '%s' to unix timestamp: %w", strVal, err)
		}
		return t.Unix(), nil
	default:
		return val, nil
	}
}

func parseDate(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02",
		"02-01-2006",
		"01/02/2006",
	}

	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}

	if unix, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(unix, 0), nil
	}

	return time.Time{}, fmt.Errorf("unrecognized date format")
}

func (re *RuleEngine) transformFormat(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok {
		if rule.Default != nil {
			return rule.Default, nil
		}
		return nil, fmt.Errorf("source field not found: %s", rule.Source)
	}

	if val == nil {
		return nil, nil
	}

	format := rule.Format
	if format == "" {
		return fmt.Sprintf("%v", val), nil
	}

	strVal := fmt.Sprintf("%v", val)

	if strings.Contains(format, "unix") {
		t, err := parseDate(strVal)
		if err != nil {
			return nil, err
		}
		if format == "unix" {
			return t.Unix(), nil
		}
		return t.UnixMilli(), nil
	}

	t, err := parseDate(strVal)
	if err == nil {
		return t.Format(format), nil
	}

	return fmt.Sprintf(format, val), nil
}

func (re *RuleEngine) transformConcat(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	if len(rule.Sources) == 0 && rule.Source != "" {
		rule.Sources = []string{rule.Source}
	}

	sep := rule.Separator
	if sep == "" {
		sep = ""
	}

	var sb strings.Builder
	for i, src := range rule.Sources {
		val, ok := getNestedValue(ctx.SourceData, src)
		if ok && val != nil {
			if i > 0 {
				sb.WriteString(sep)
			}
			sb.WriteString(fmt.Sprintf("%v", val))
		}
	}

	result := sb.String()
	if result == "" && rule.Default != nil {
		return rule.Default, nil
	}

	return result, nil
}

func (re *RuleEngine) transformSplit(rule *MappingRule, ctx *TransformContext) ([]interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok {
		if rule.Default != nil {
			return []interface{}{rule.Default}, nil
		}
		return nil, fmt.Errorf("source field not found: %s", rule.Source)
	}

	if val == nil {
		return make([]interface{}, len(rule.Targets)), nil
	}

	strVal := fmt.Sprintf("%v", val)
	sep := rule.Separator
	if sep == "" {
		sep = " "
	}

	parts := strings.Split(strVal, sep)
	result := make([]interface{}, len(rule.Targets))
	for i := range rule.Targets {
		if i < len(parts) {
			result[i] = strings.TrimSpace(parts[i])
		}
	}

	return result, nil
}

func (re *RuleEngine) transformLookup(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok {
		if rule.Default != nil {
			return rule.Default, nil
		}
		return nil, fmt.Errorf("source field not found: %s", rule.Source)
	}

	key := fmt.Sprintf("%v", val)
	if mapped, ok := rule.LookupTable[key]; ok {
		return mapped, nil
	}

	if rule.Default != nil {
		return rule.Default, nil
	}

	return val, nil
}

func (re *RuleEngine) transformExpression(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	expr := rule.Expression
	if expr == "" {
		return nil, fmt.Errorf("no expression specified")
	}

	return evaluateExpression(expr, ctx.SourceData)
}

func evaluateExpression(expr string, data map[string]interface{}) (interface{}, error) {
	expr = strings.TrimSpace(expr)

	if strings.HasPrefix(expr, "toUpper(") {
		inner := expr[8 : len(expr)-1]
		val, _ := getNestedValue(data, inner)
		return strings.ToUpper(fmt.Sprintf("%v", val)), nil
	}
	if strings.HasPrefix(expr, "toLower(") {
		inner := expr[8 : len(expr)-1]
		val, _ := getNestedValue(data, inner)
		return strings.ToLower(fmt.Sprintf("%v", val)), nil
	}
	if strings.HasPrefix(expr, "trim(") {
		inner := expr[5 : len(expr)-1]
		val, _ := getNestedValue(data, inner)
		return strings.TrimSpace(fmt.Sprintf("%v", val)), nil
	}
	if strings.HasPrefix(expr, "substring(") {
		inner := expr[10 : len(expr)-1]
		parts := strings.SplitN(inner, ",", 3)
		if len(parts) >= 2 {
			field := strings.TrimSpace(parts[0])
			start, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			length := -1
			if len(parts) >= 3 {
				length, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
			}
			val, _ := getNestedValue(data, field)
			s := fmt.Sprintf("%v", val)
			if start < 0 {
				start = 0
			}
			if start > len(s) {
				return "", nil
			}
			end := len(s)
			if length > 0 && start+length < len(s) {
				end = start + length
			}
			return s[start:end], nil
		}
	}
	if strings.Contains(expr, "replace(") {
		inner := expr[8 : len(expr)-1]
		parts := strings.SplitN(inner, ",", 3)
		if len(parts) == 3 {
			field := strings.TrimSpace(parts[0])
			old := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
			new := strings.Trim(strings.TrimSpace(parts[2]), "'\"")
			val, _ := getNestedValue(data, field)
			s := fmt.Sprintf("%v", val)
			return strings.ReplaceAll(s, old, new), nil
		}
	}

	if strings.Contains(expr, "+") || strings.Contains(expr, "-") ||
		strings.Contains(expr, "*") || strings.Contains(expr, "/") {
		return evaluateArithmetic(expr, data)
	}

	val, ok := getNestedValue(data, expr)
	if ok {
		return val, nil
	}

	return expr, nil
}

func evaluateArithmetic(expr string, data map[string]interface{}) (float64, error) {
	expr = strings.ReplaceAll(expr, " ", "")

	tokens := tokenizeExpression(expr)
	if len(tokens) == 0 {
		return 0, fmt.Errorf("empty expression")
	}

	var values []float64
	var ops []string

	for _, token := range tokens {
		if token == "+" || token == "-" || token == "*" || token == "/" {
			ops = append(ops, token)
		} else {
			val, err := resolveToken(token, data)
			if err != nil {
				return 0, err
			}
			values = append(values, val)
		}
	}

	for len(ops) > 0 {
		priority := -1
		idx := -1
		for i, op := range ops {
			p := 0
			if op == "*" || op == "/" {
				p = 1
			}
			if p > priority {
				priority = p
				idx = i
			}
		}

		var result float64
		switch ops[idx] {
		case "+":
			result = values[idx] + values[idx+1]
		case "-":
			result = values[idx] - values[idx+1]
		case "*":
			result = values[idx] * values[idx+1]
		case "/":
			if values[idx+1] == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			result = values[idx] / values[idx+1]
		}

		values = append(append(values[:idx], result), values[idx+2:]...)
		ops = append(ops[:idx], ops[idx+1:]...)
	}

	if len(values) == 0 {
		return 0, nil
	}
	return values[0], nil
}

func tokenizeExpression(expr string) []string {
	var tokens []string
	var current strings.Builder

	for _, r := range expr {
		if r == '+' || r == '-' || r == '*' || r == '/' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(r))
		} else {
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

func resolveToken(token string, data map[string]interface{}) (float64, error) {
	if f, err := strconv.ParseFloat(token, 64); err == nil {
		return f, nil
	}

	val, ok := getNestedValue(data, token)
	if !ok {
		return 0, fmt.Errorf("field not found: %s", token)
	}

	strVal := fmt.Sprintf("%v", val)
	return strconv.ParseFloat(strVal, 64)
}

func (re *RuleEngine) transformDefault(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok || val == nil {
		return rule.Default, nil
	}
	return val, nil
}

func (re *RuleEngine) transformNested(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	nestedResult := make(map[string]interface{})
	for _, subRule := range rule.Mappings {
		re.applyRule(subRule, ctx, nestedResult)
	}
	return nestedResult, nil
}

func (re *RuleEngine) transformArrayMap(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	val, ok := getNestedValue(ctx.SourceData, rule.Source)
	if !ok {
		if rule.Default != nil {
			return rule.Default, nil
		}
		return nil, fmt.Errorf("source field not found: %s", rule.Source)
	}

	arr, ok := val.([]interface{})
	if !ok {
		return nil, fmt.Errorf("source field is not an array")
	}

	result := make([]interface{}, 0, len(arr))
	for i, item := range arr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			itemMap = map[string]interface{}{"value": item}
		}

		subCtx := &TransformContext{
			SourceData: itemMap,
			RowIndex:   i,
			Errors:     ctx.Errors,
		}

		itemResult := make(map[string]interface{})
		for _, subRule := range rule.Mappings {
			re.applyRule(subRule, subCtx, itemResult)
		}
		result = append(result, itemResult)
	}

	return result, nil
}

func (re *RuleEngine) transformConditional(rule *MappingRule, ctx *TransformContext) (interface{}, error) {
	if rule.Condition == nil {
		return nil, fmt.Errorf("no condition specified")
	}

	val, ok := getNestedValue(ctx.SourceData, rule.Condition.Field)
	if !ok {
		if rule.Condition.Else != "" {
			elseVal, _ := getNestedValue(ctx.SourceData, rule.Condition.Else)
			return elseVal, nil
		}
		if rule.Default != nil {
			return rule.Default, nil
		}
		return nil, nil
	}

	condVal := fmt.Sprintf("%v", val)
	matchVal := fmt.Sprintf("%v", rule.Condition.Value)

	matched := false
	switch strings.ToLower(rule.Condition.Op) {
	case "=", "==", "eq":
		matched = condVal == matchVal
	case "!=", "<>", "ne":
		matched = condVal != matchVal
	case ">", "gt":
		matched = compareNumbers(val, rule.Condition.Value) > 0
	case "<", "lt":
		matched = compareNumbers(val, rule.Condition.Value) < 0
	case ">=", "gte":
		matched = compareNumbers(val, rule.Condition.Value) >= 0
	case "<=", "lte":
		matched = compareNumbers(val, rule.Condition.Value) <= 0
	case "contains":
		matched = strings.Contains(condVal, matchVal)
	case "startswith":
		matched = strings.HasPrefix(condVal, matchVal)
	case "endswith":
		matched = strings.HasSuffix(condVal, matchVal)
	default:
		matched = condVal == matchVal
	}

	if matched {
		thenVal, _ := getNestedValue(ctx.SourceData, rule.Condition.Then)
		return thenVal, nil
	} else if rule.Condition.Else != "" {
		elseVal, _ := getNestedValue(ctx.SourceData, rule.Condition.Else)
		return elseVal, nil
	}

	if rule.Default != nil {
		return rule.Default, nil
	}
	return nil, nil
}

func compareNumbers(a, b interface{}) int {
	fa, erra := toFloat64(a)
	fb, errb := toFloat64(b)
	if erra != nil || errb != nil {
		return strings.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
	}
	if fa < fb {
		return -1
	}
	if fa > fb {
		return 1
	}
	return 0
}

func toFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	default:
		return strconv.ParseFloat(fmt.Sprintf("%v", val), 64)
	}
}

func (r *MappingRules) ToJSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func GenerateMappingFileName(source, target string) string {
	base := fmt.Sprintf("%s_to_%s", source, target)
	return base + ".yaml"
}

func GetMappingFilePath(dir, source, target string) string {
	return filepath.Join(dir, GenerateMappingFileName(source, target))
}
