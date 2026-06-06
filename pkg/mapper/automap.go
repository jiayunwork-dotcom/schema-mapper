package mapper

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
)

var AbbreviationMap = map[string]string{
	"id":    "identifier",
	"ids":   "identifiers",
	"ts":    "timestamp",
	"addr":  "address",
	"dept":  "department",
	"qty":   "quantity",
	"num":   "number",
	"desc":  "description",
	"name":  "name",
	"fn":    "firstname",
	"ln":    "lastname",
	"dob":   "dateofbirth",
	"dod":   "dateofdeath",
	"created": "createdat",
	"updated": "updatedat",
	"deleted": "deletedat",
	"usr":   "user",
	"pwd":   "password",
	"pwdhash": "passwordhash",
	"email": "emailaddress",
	"phone": "phonenumber",
	"zip":   "zipcode",
	"city":  "city",
	"st":    "state",
	"street": "streetaddress",
	"lat":   "latitude",
	"lon":   "longitude",
	"lng":   "longitude",
	"temp":  "temperature",
	"hum":   "humidity",
	"pres":  "pressure",
	"vel":   "velocity",
	"acc":   "acceleration",
	"ref":   "reference",
	"seq":   "sequence",
	"cfg":   "configuration",
	"config": "configuration",
	"stat":  "status",
	"cnt":   "count",
	"tot":   "total",
	"avg":   "average",
	"min":   "minimum",
	"max":   "maximum",
	"sum":   "summary",
	"idx":   "index",
	"pos":   "position",
	"loc":   "location",
}

type MappingSuggestion struct {
	SourcePath   string   `json:"sourcePath"`
	TargetPath   string   `json:"targetPath"`
	Confidence   float64  `json:"confidence"`
	NeedsReview  bool     `json:"needsReview"`
	MatchType    string   `json:"matchType"`
	Transform    string   `json:"transform"`
	Warnings     []string `json:"warnings,omitempty"`
}

type AutoMapperResult struct {
	SourceSchema *ir.Schema          `json:"sourceSchema"`
	TargetSchema *ir.Schema          `json:"targetSchema"`
	Mappings     []*MappingSuggestion `json:"mappings"`
	Unmapped     []string            `json:"unmapped"`
	Uncovered    []string            `json:"uncovered"`
}

type AutoMapper struct {
	SimilarityThreshold float64
}

func NewAutoMapper() *AutoMapper {
	return &AutoMapper{
		SimilarityThreshold: 0.7,
	}
}

func (am *AutoMapper) GenerateMapping(source, target *ir.Schema) *AutoMapperResult {
	result := &AutoMapperResult{
		SourceSchema: source,
		TargetSchema: target,
		Mappings:     make([]*MappingSuggestion, 0),
		Unmapped:     make([]string, 0),
		Uncovered:    make([]string, 0),
	}

	sourceFields := source.AllFields()
	targetFields := target.AllFields()

	sourceLeafFields := filterLeafFields(sourceFields)
	targetLeafFields := filterLeafFields(targetFields)

	matchedTargets := make(map[string]bool)
	matchedSources := make(map[string]bool)

	for _, srcField := range sourceLeafFields {
		bestMatch := am.findBestMatch(srcField, targetLeafFields)
		if bestMatch != nil && !matchedTargets[bestMatch.TargetPath] {
			result.Mappings = append(result.Mappings, bestMatch)
			matchedSources[srcField.Path] = true
			matchedTargets[bestMatch.TargetPath] = true
		} else {
			nestedMatch := am.tryNestedMatch(srcField, target, targetLeafFields)
			if nestedMatch != nil && !matchedTargets[nestedMatch.TargetPath] {
				result.Mappings = append(result.Mappings, nestedMatch)
				matchedSources[srcField.Path] = true
				matchedTargets[nestedMatch.TargetPath] = true
			}
		}
	}

	for _, src := range sourceLeafFields {
		if !matchedSources[src.Path] {
			result.Unmapped = append(result.Unmapped, src.Path)
		}
	}

	for _, tgt := range targetLeafFields {
		if !matchedTargets[tgt.Path] {
			result.Uncovered = append(result.Uncovered, tgt.Path)
		}
	}

	return result
}

func filterLeafFields(fields []*ir.Field) []*ir.Field {
	result := make([]*ir.Field, 0)
	for _, f := range fields {
		if f.IsLeaf() {
			result = append(result, f)
		}
	}
	return result
}

func (am *AutoMapper) findBestMatch(src *ir.Field, targets []*ir.Field) *MappingSuggestion {
	var best *MappingSuggestion

	for _, tgt := range targets {
		suggestion := am.calculateMatch(src, tgt)
		if suggestion != nil {
			if best == nil || suggestion.Confidence > best.Confidence {
				best = suggestion
			}
		}
	}

	return best
}

func (am *AutoMapper) calculateMatch(src, tgt *ir.Field) *MappingSuggestion {
	srcPath := normalizeForMatch(src.Path)
	tgtPath := normalizeForMatch(tgt.Path)

	srcName := normalizeForMatch(src.Name)
	tgtName := normalizeForMatch(tgt.Name)

	confidence := 0.0
	matchType := ""

	if strings.EqualFold(srcName, tgtName) || strings.EqualFold(srcPath, tgtPath) {
		confidence = 1.0
		matchType = "exact"
	} else {
		sim := max(
			normalizedLevenshtein(srcName, tgtName),
			normalizedLevenshtein(srcPath, tgtPath),
			semanticMatch(srcName, tgtName),
			semanticMatch(srcPath, tgtPath),
		)

		if sim >= am.SimilarityThreshold {
			confidence = sim
			matchType = "similarity"
		} else {
			return nil
		}
	}

	suggestion := &MappingSuggestion{
		SourcePath:  src.Path,
		TargetPath:  tgt.Path,
		Confidence:  confidence,
		NeedsReview: confidence < 0.7,
		MatchType:   matchType,
		Transform:   "direct",
		Warnings:    make([]string, 0),
	}

	typeCompat := CheckTypeCompatibility(src.Type, tgt.Type)
	if typeCompat == "warning" {
		suggestion.Warnings = append(suggestion.Warnings,
			fmt.Sprintf("Type conversion may lose precision: %s -> %s", src.Type, tgt.Type))
		suggestion.Transform = "cast"
	} else if typeCompat == "unsafe" {
		suggestion.Warnings = append(suggestion.Warnings,
			fmt.Sprintf("Potentially unsafe type conversion: %s -> %s", src.Type, tgt.Type))
		suggestion.Transform = "cast"
	}

	if !tgt.Nullable && src.Nullable {
		suggestion.Warnings = append(suggestion.Warnings,
			"Source is nullable but target requires non-null value")
	}

	return suggestion
}

func (am *AutoMapper) tryNestedMatch(src *ir.Field, targetSchema *ir.Schema, targetFields []*ir.Field) *MappingSuggestion {
	srcPathParts := strings.Split(src.Path, ".")
	lastPart := srcPathParts[len(srcPathParts)-1]

	for _, tgt := range targetFields {
		tgtPathParts := strings.Split(tgt.Path, ".")
		for _, tgtPart := range tgtPathParts {
			normalizedSrc := normalizeForMatch(lastPart)
			normalizedTgt := normalizeForMatch(tgtPart)
			if normalizedSrc == normalizedTgt ||
				normalizedLevenshtein(normalizedSrc, normalizedTgt) >= 0.8 {

				suggestion := &MappingSuggestion{
					SourcePath:  src.Path,
					TargetPath:  tgt.Path,
					Confidence:  0.75,
					NeedsReview: true,
					MatchType:   "nested-flatten",
					Transform:   "direct",
					Warnings:    []string{"Nested structure alignment detected - may require manual review"},
				}
				return suggestion
			}
		}
	}

	for _, tgt := range targetFields {
		tgtLastPart := tgt.Path
		if idx := strings.LastIndex(tgt.Path, "."); idx >= 0 {
			tgtLastPart = tgt.Path[idx+1:]
		}

		normalizedSrc := normalizeForMatch(src.Path)
		normalizedTgt := normalizeForMatch(tgtLastPart)

		if normalizedSrc == normalizedTgt ||
			normalizedLevenshtein(normalizedSrc, normalizedTgt) >= 0.8 {
			suggestion := &MappingSuggestion{
				SourcePath:  src.Path,
				TargetPath:  tgt.Path,
				Confidence:  0.75,
				NeedsReview: true,
				MatchType:   "flatten-nested",
				Transform:   "nested",
				Warnings:    []string{"Flat to nested structure alignment detected - may require manual review"},
			}
			return suggestion
		}
	}

	return nil
}

func normalizeForMatch(s string) string {
	s = strings.ToLower(s)
	var result strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func LevenshteinDistance(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)
	len1, len2 := len(r1), len(r2)

	if len1 == 0 {
		return len2
	}
	if len2 == 0 {
		return len1
	}

	prev := make([]int, len2+1)
	curr := make([]int, len2+1)

	for j := 0; j <= len2; j++ {
		prev[j] = j
	}

	for i := 1; i <= len1; i++ {
		curr[0] = i
		for j := 1; j <= len2; j++ {
			cost := 1
			if r1[i-1] == r2[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return prev[len2]
}

func normalizedLevenshtein(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	dist := LevenshteinDistance(s1, s2)
	maxLen := max(float64(len(s1)), float64(len(s2)))
	if maxLen == 0 {
		return 1.0
	}
	return 1.0 - float64(dist)/maxLen
}

func semanticMatch(s1, s2 string) float64 {
	tokens1 := splitIntoTokens(s1)
	tokens2 := splitIntoTokens(s2)

	expanded1 := expandAbbreviations(tokens1)
	expanded2 := expandAbbreviations(tokens2)

	set1 := make(map[string]bool)
	for _, t := range expanded1 {
		set1[t] = true
	}
	set2 := make(map[string]bool)
	for _, t := range expanded2 {
		set2[t] = true
	}

	intersection := 0
	for t := range set1 {
		if set2[t] {
			intersection++
		}
	}

	union := len(set1) + len(set2) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func splitIntoTokens(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	var current strings.Builder

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	result := make([]string, 0)
	for _, t := range tokens {
		result = append(result, splitCamelCase(t)...)
	}
	return result
}

func splitCamelCase(s string) []string {
	var result []string
	var current strings.Builder

	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(rune(s[i-1])) {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

func expandAbbreviations(tokens []string) []string {
	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		result = append(result, t)
		if expanded, ok := AbbreviationMap[strings.ToLower(t)]; ok {
			result = append(result, expanded)
		}
	}
	return result
}

func CheckTypeCompatibility(src, tgt ir.BaseType) string {
	if src == tgt {
		return "safe"
	}

	safeWidening := map[ir.BaseType][]ir.BaseType{
		ir.TypeInt32:   {ir.TypeInt64, ir.TypeFloat64, ir.TypeFloat32},
		ir.TypeInt64:   {ir.TypeFloat64},
		ir.TypeFloat32: {ir.TypeFloat64},
		ir.TypeDate:    {ir.TypeDateTime, ir.TypeString},
		ir.TypeBool:    {ir.TypeInt32, ir.TypeInt64, ir.TypeString},
	}

	if targets, ok := safeWidening[src]; ok {
		for _, t := range targets {
			if t == tgt {
				return "safe"
			}
		}
	}

	warningConversions := map[ir.BaseType][]ir.BaseType{
		ir.TypeFloat64: {ir.TypeFloat32},
		ir.TypeInt64:   {ir.TypeInt32},
		ir.TypeString:  {ir.TypeDate, ir.TypeDateTime, ir.TypeInt32, ir.TypeInt64},
	}

	if targets, ok := warningConversions[src]; ok {
		for _, t := range targets {
			if t == tgt {
				return "warning"
			}
		}
	}

	if src.IsStringLike() && tgt.IsStringLike() {
		return "safe"
	}

	return "unsafe"
}

func max(a, b float64, rest ...float64) float64 {
	m := math.Max(a, b)
	for _, r := range rest {
		m = math.Max(m, r)
	}
	return m
}

func min(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func (r *AutoMapperResult) ToYAML() string {
	var sb strings.Builder
	sb.WriteString("# Auto-generated mapping rules\n")
	sb.WriteString("# Confidence threshold: 0.7 (marked as needs_review below this)\n\n")
	sb.WriteString("mappings:\n")

	for _, m := range r.Mappings {
		sb.WriteString(fmt.Sprintf("  - source: %s\n", m.SourcePath))
		sb.WriteString(fmt.Sprintf("    target: %s\n", m.TargetPath))
		sb.WriteString(fmt.Sprintf("    transform: %s\n", m.Transform))
		sb.WriteString(fmt.Sprintf("    confidence: %.2f\n", m.Confidence))
		sb.WriteString(fmt.Sprintf("    match_type: %s\n", m.MatchType))
		if m.NeedsReview {
			sb.WriteString("    needs_review: true\n")
		}
		if len(m.Warnings) > 0 {
			sb.WriteString("    warnings:\n")
			for _, w := range m.Warnings {
				sb.WriteString(fmt.Sprintf("      - %s\n", w))
			}
		}
		sb.WriteString("\n")
	}

	if len(r.Unmapped) > 0 {
		sb.WriteString("# Unmapped source fields:\n")
		for _, u := range r.Unmapped {
			sb.WriteString("#   - ")
			sb.WriteString(u)
			sb.WriteString("\n")
		}
	}

	if len(r.Uncovered) > 0 {
		sb.WriteString("# Uncovered target fields:\n")
		for _, u := range r.Uncovered {
			sb.WriteString("#   - ")
			sb.WriteString(u)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
