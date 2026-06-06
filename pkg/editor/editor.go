package editor

import (
	"fmt"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
	"github.com/schema-mapper/schema-mapper/pkg/parser"
)

type FilterMode int

const (
	FilterAll FilterMode = iota
	FilterNeedsReview
	FilterUnmapped
)

type EditorState struct {
	rules         *mapper.MappingRules
	mappingFile   string
	sourceSchema  *ir.Schema
	targetSchema  *ir.Schema
	cursor        int
	scrollOffset  int
	modified      bool
	searchQuery   string
	searchMode    bool
	filterMode    FilterMode
	filteredItems []*DisplayItem
	screenHeight  int
	screenWidth   int
}

type DisplayItem struct {
	Rule     *mapper.MappingRule
	IsNew    bool
	IsUnmapped bool
	UnmappedSource string
}

var TransformTypes = []mapper.TransformType{
	mapper.TransformDirect,
	mapper.TransformCast,
	mapper.TransformFormat,
	mapper.TransformConcat,
	mapper.TransformSplit,
	mapper.TransformLookup,
	mapper.TransformExpression,
	mapper.TransformDefault,
}

func NewEditor(mappingFile, sourceSchemaPath, targetSchemaPath string) (*EditorState, error) {
	rules, err := mapper.LoadMappingRules(mappingFile)
	if err != nil {
		return nil, err
	}

	registry := parser.NewParserRegistry()

	var sourceSchema, targetSchema *ir.Schema
	if sourceSchemaPath != "" {
		sourceSchema, err = registry.ParseFile(sourceSchemaPath, detectFormat(sourceSchemaPath))
		if err != nil {
			return nil, fmt.Errorf("failed to parse source schema: %w", err)
		}
	}
	if targetSchemaPath != "" {
		targetSchema, err = registry.ParseFile(targetSchemaPath, detectFormat(targetSchemaPath))
		if err != nil {
			return nil, fmt.Errorf("failed to parse target schema: %w", err)
		}
	}

	if rules.SourceSchema != "" && sourceSchemaPath == "" {
		sourceSchema, _ = registry.ParseFile(rules.SourceSchema, detectFormat(rules.SourceSchema))
	}
	if rules.TargetSchema != "" && targetSchemaPath == "" {
		targetSchema, _ = registry.ParseFile(rules.TargetSchema, detectFormat(rules.TargetSchema))
	}

	ed := &EditorState{
		rules:        rules,
		mappingFile:  mappingFile,
		sourceSchema: sourceSchema,
		targetSchema: targetSchema,
		cursor:       0,
		scrollOffset: 0,
		modified:     false,
		searchMode:   false,
		filterMode:   FilterAll,
	}
	ed.RefreshFilteredItems()
	return ed, nil
}

func (e *EditorState) Run() error {
	if err := initTerminal(); err != nil {
		return err
	}
	defer restoreTerminal()

	e.screenHeight, e.screenWidth = getTerminalSize()
	e.RefreshFilteredItems()

	for {
		e.screenHeight, e.screenWidth = getTerminalSize()
		e.render()

		key, err := readKey()
		if err != nil {
			return err
		}

		if e.searchMode {
			if err := e.handleSearchKey(key); err != nil {
				if err == ErrExitSearch {
					e.searchMode = false
					e.searchQuery = ""
					e.RefreshFilteredItems()
				} else {
					return err
				}
			}
			continue
		}

		if err := e.handleKey(key); err != nil {
			if err == ErrExit {
				return nil
			}
			return err
		}
	}
}

func (e *EditorState) handleKey(key Key) error {
	switch key.Type {
	case KeyArrowUp:
		e.moveCursor(-1)
	case KeyArrowDown:
		e.moveCursor(1)
	case KeyRune:
		switch key.Ch {
		case 'k':
			e.moveCursor(-1)
		case 'j':
			e.moveCursor(1)
		case 'd':
			e.deleteMapping()
		case 'e':
			if err := e.editTransformType(); err != nil {
				return err
			}
		case 't':
			if err := e.changeTargetField(); err != nil {
				return err
			}
		case 'a':
			if err := e.addMapping(); err != nil {
				return err
			}
		case 'c':
			e.confirmMapping()
		case 's':
			if err := e.save(); err != nil {
				return err
			}
		case '/':
			e.searchMode = true
			e.searchQuery = ""
		case 'f':
			e.cycleFilterMode()
		case 'q':
			if e.modified {
				if err := e.confirmQuit(); err != nil {
					return err
				}
			}
			return ErrExit
		}
	case KeyCtrlC:
		if e.modified {
			if err := e.confirmQuit(); err != nil {
				return err
			}
		}
		return ErrExit
	}
	return nil
}

func (e *EditorState) handleSearchKey(key Key) error {
	switch key.Type {
	case KeyRune:
		e.searchQuery += string(key.Ch)
		e.RefreshFilteredItems()
	case KeyBackspace:
		if len(e.searchQuery) > 0 {
			e.searchQuery = e.searchQuery[:len(e.searchQuery)-1]
			e.RefreshFilteredItems()
		}
	case KeyEnter:
		e.searchMode = false
	case KeyEscape, KeyCtrlC:
		return ErrExitSearch
	}
	return nil
}

func (e *EditorState) moveCursor(delta int) {
	if len(e.filteredItems) == 0 {
		return
	}

	newCursor := e.cursor + delta
	if newCursor < 0 {
		newCursor = 0
	}
	if newCursor >= len(e.filteredItems) {
		newCursor = len(e.filteredItems) - 1
	}

	e.cursor = newCursor

	visibleHeight := e.screenHeight - 4
	if e.cursor < e.scrollOffset {
		e.scrollOffset = e.cursor
	} else if e.cursor >= e.scrollOffset+visibleHeight {
		e.scrollOffset = e.cursor - visibleHeight + 1
	}
}

func (e *EditorState) RefreshFilteredItems() {
	e.filteredItems = make([]*DisplayItem, 0)

	for _, rule := range e.rules.Mappings {
		if rule.Source == "" && rule.Target == "" {
			continue
		}

		item := &DisplayItem{Rule: rule}

		if e.searchQuery != "" {
			query := strings.ToLower(e.searchQuery)
			if !strings.Contains(strings.ToLower(rule.Source), query) &&
				!strings.Contains(strings.ToLower(rule.Target), query) {
				continue
			}
		}

		switch e.filterMode {
		case FilterNeedsReview:
			if !rule.NeedsReview {
				continue
			}
		case FilterUnmapped:
			if rule.Source != "" || rule.Target != "" {
				continue
			}
		}

		e.filteredItems = append(e.filteredItems, item)
	}

	if e.cursor >= len(e.filteredItems) {
		e.cursor = max(0, len(e.filteredItems)-1)
	}
	if e.scrollOffset > e.cursor {
		e.scrollOffset = e.cursor
	}
}

func (e *EditorState) deleteMapping() {
	if len(e.filteredItems) == 0 || e.cursor >= len(e.filteredItems) {
		return
	}

	item := e.filteredItems[e.cursor]
	if item.Rule != nil {
		for i, r := range e.rules.Mappings {
			if r == item.Rule {
				e.rules.Mappings = append(e.rules.Mappings[:i], e.rules.Mappings[i+1:]...)
				break
			}
		}
		e.modified = true
		e.RefreshFilteredItems()
	}
}

func (e *EditorState) confirmMapping() {
	if len(e.filteredItems) == 0 || e.cursor >= len(e.filteredItems) {
		return
	}

	item := e.filteredItems[e.cursor]
	if item.Rule != nil && item.Rule.NeedsReview {
		item.Rule.NeedsReview = false
		e.modified = true
		e.RefreshFilteredItems()
	}
}

func (e *EditorState) cycleFilterMode() {
	e.filterMode = FilterMode((int(e.filterMode) + 1) % 3)
	e.RefreshFilteredItems()
}

func (e *EditorState) save() error {
	if err := e.rules.Save(e.mappingFile); err != nil {
		return err
	}
	e.modified = false
	return nil
}

func (e *EditorState) confirmQuit() error {
	result, err := showYesNoPrompt("有未保存的修改，确定要退出吗? (y/n)")
	if err != nil {
		return err
	}
	if result {
		return ErrExit
	}
	return nil
}

func (e *EditorState) GetFilteredItems() []*DisplayItem {
	return e.filteredItems
}

func (e *EditorState) GetRules() []*mapper.MappingRule {
	return e.rules.Mappings
}

func (e *EditorState) SetFilterMode(mode FilterMode) {
	e.filterMode = mode
}

func (e *EditorState) GetStats() (mappedCount, totalCount, needsReviewCount, unmappedSourceCount, uncoveredTargetCount int) {
	allMappings := e.rules.Mappings
	totalCount = len(allMappings)

	mappedFields := make(map[string]bool)
	for _, r := range allMappings {
		if r.Source != "" && r.Target != "" {
			mappedCount++
			mappedFields[r.Source] = true
		}
		if r.NeedsReview {
			needsReviewCount++
		}
	}

	if e.sourceSchema != nil {
		for _, f := range e.sourceSchema.AllFields() {
			if f.IsLeaf() && !mappedFields[f.Path] {
				unmappedSourceCount++
			}
		}
	}

	mappedTargets := make(map[string]bool)
	for _, r := range allMappings {
		if r.Target != "" {
			mappedTargets[r.Target] = true
		}
	}

	if e.targetSchema != nil {
		for _, f := range e.targetSchema.AllFields() {
			if f.IsLeaf() && !mappedTargets[f.Path] {
				uncoveredTargetCount++
			}
		}
	}

	return
}

func (e *EditorState) GetUnmappedSourceFields() []string {
	if e.sourceSchema == nil {
		return nil
	}

	mappedSources := make(map[string]bool)
	for _, r := range e.rules.Mappings {
		if r.Source != "" {
			mappedSources[r.Source] = true
		}
	}

	result := make([]string, 0)
	for _, f := range e.sourceSchema.AllFields() {
		if f.IsLeaf() && !mappedSources[f.Path] {
			result = append(result, f.Path)
		}
	}
	return result
}

func (e *EditorState) GetUncoveredTargetFields() []string {
	if e.targetSchema == nil {
		return nil
	}

	mappedTargets := make(map[string]bool)
	for _, r := range e.rules.Mappings {
		if r.Target != "" {
			mappedTargets[r.Target] = true
		}
	}

	result := make([]string, 0)
	for _, f := range e.targetSchema.AllFields() {
		if f.IsLeaf() && !mappedTargets[f.Path] {
			result = append(result, f.Path)
		}
	}
	return result
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func detectFormat(path string) string {
	if strings.HasSuffix(path, ".json") {
		return "json-schema"
	}
	if strings.HasSuffix(path, ".avsc") {
		return "avro"
	}
	if strings.HasSuffix(path, ".proto") {
		return "protobuf"
	}
	if strings.HasSuffix(path, ".sql") {
		return "sql"
	}
	if strings.HasSuffix(path, ".csv") {
		return "csv"
	}
	if strings.HasSuffix(path, ".xsd") {
		return "xsd"
	}
	return ""
}

var (
	ErrExit       = fmt.Errorf("exit")
	ErrExitSearch = fmt.Errorf("exit search")
)
