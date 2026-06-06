package editor

import (
	"fmt"
	"strings"

	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	bgBlue      = "\033[44m"
	bgGray      = "\033[100m"
)

func (e *EditorState) render() {
	fmt.Print("\033[2J\033[H")

	e.renderHeader()
	e.renderTable()
	e.renderStatusBar()
	e.renderHelpBar()
}

func (e *EditorState) renderHeader() {
	title := fmt.Sprintf(" Schema Mapper Interactive Editor - %s ", e.mappingFile)
	if e.modified {
		title += colorYellow + "[未保存]" + colorReset
	}
	fmt.Println(colorBold + colorBlue + title + colorReset)
	fmt.Println(strings.Repeat("─", e.screenWidth))
}

func (e *EditorState) renderTable() {
	visibleHeight := e.screenHeight - 4

	headerFmt := fmt.Sprintf(" %%-%ds │ %%-%ds │ %%-%ds │ %%-%ds │ %%s ",
		(e.screenWidth-30)/3, (e.screenWidth-30)/3, 10, 10)

	header := fmt.Sprintf(headerFmt, "源字段", "目标字段", "转换类型", "置信度", "需确认")
	fmt.Println(colorBold + header + colorReset)
	fmt.Println(strings.Repeat("─", e.screenWidth))

	start := e.scrollOffset
	end := min(start+visibleHeight, len(e.filteredItems))

	for i := start; i < end; i++ {
		item := e.filteredItems[i]
		if item == nil || item.Rule == nil {
			continue
		}

		rule := item.Rule
		isSelected := i == e.cursor

		source := truncate(rule.Source, (e.screenWidth-30)/3)
		target := truncate(rule.Target, (e.screenWidth-30)/3)
		transform := truncate(string(rule.Transform), 10)
		confidence := fmt.Sprintf("%.2f", rule.Confidence)
		needsReview := " "
		if rule.NeedsReview {
			needsReview = colorRed + "✓" + colorReset
		}

		line := fmt.Sprintf(headerFmt, source, target, transform, confidence, needsReview)

		if isSelected {
			line = bgBlue + colorBold + line + colorReset
		}

		if rule.NeedsReview && !isSelected {
			line = colorYellow + line + colorReset
		}

		fmt.Println(line)
	}

	emptyLines := visibleHeight - (end - start)
	for i := 0; i < emptyLines; i++ {
		fmt.Println()
	}
}

func (e *EditorState) renderStatusBar() {
	mappedCount, totalCount, needsReviewCount, unmappedSourceCount, uncoveredTargetCount := e.GetStats()

	filterName := "全部"
	switch e.filterMode {
	case FilterNeedsReview:
		filterName = "需确认"
	case FilterUnmapped:
		filterName = "未映射"
	}

	status := fmt.Sprintf(
		" 已映射: %d/%d | 需确认: %d | 未映射源: %d | 未覆盖目标: %d | 过滤: %s ",
		mappedCount, totalCount, needsReviewCount, unmappedSourceCount, uncoveredTargetCount, filterName,
	)

	if e.searchMode {
		status += fmt.Sprintf(" | 搜索: /%s", e.searchQuery)
	}

	fmt.Println(strings.Repeat("─", e.screenWidth))
	fmt.Println(colorBold + colorCyan + status + colorReset)
}

func (e *EditorState) renderHelpBar() {
	help := "  ↑/k:上移 ↓/j:下移 d:删除 e:编辑转换 t:改目标 a:新增 c:确认 s:保存 /:搜索 f:过滤 q:退出 "
	if len(help) > e.screenWidth {
		help = help[:e.screenWidth]
	}
	fmt.Println(colorDim + help + colorReset)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s + strings.Repeat(" ", maxLen-len(s))
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func showSelectionList(title string, items []string) (int, error) {
	cursor := 0

	for {
		fmt.Print("\033[2J\033[H")
		fmt.Println(colorBold + colorBlue + " " + title + " " + colorReset)
		fmt.Println(strings.Repeat("─", 80))
		fmt.Println()

		for i, item := range items {
			marker := "  "
			if i == cursor {
				marker = colorBold + bgBlue + "▶ " + colorReset
			}
			fmt.Printf("%s%s\n", marker, item)
		}

		fmt.Println()
		fmt.Println(colorDim + " ↑/k:上移 ↓/j:下移 Enter:选择 Esc:取消 " + colorReset)

		key, err := readKey()
		if err != nil {
			return -1, err
		}

		switch key.Type {
		case KeyArrowUp:
			if cursor > 0 {
				cursor--
			}
		case KeyArrowDown:
			if cursor < len(items)-1 {
				cursor++
			}
		case KeyRune:
			switch key.Ch {
			case 'k':
				if cursor > 0 {
					cursor--
				}
			case 'j':
				if cursor < len(items)-1 {
					cursor++
				}
			}
		case KeyEnter:
			return cursor, nil
		case KeyEscape, KeyCtrlC:
			return -1, nil
		}
	}
}

func showYesNoPrompt(prompt string) (bool, error) {
	for {
		fmt.Print("\033[s")
		fmt.Print("\033[K")
		fmt.Print(colorYellow + prompt + colorReset)

		key, err := readKey()
		if err != nil {
			return false, err
		}

		if key.Type == KeyRune {
			switch key.Ch {
			case 'y', 'Y':
				fmt.Println()
				return true, nil
			case 'n', 'N':
				fmt.Println()
				return false, nil
			}
		}
		if key.Type == KeyEscape || key.Type == KeyCtrlC {
			fmt.Println()
			return false, nil
		}
	}
}

func (e *EditorState) editTransformType() error {
	if len(e.filteredItems) == 0 || e.cursor >= len(e.filteredItems) {
		return nil
	}

	item := e.filteredItems[e.cursor]
	if item.Rule == nil {
		return nil
	}

	items := make([]string, len(TransformTypes))
	for i, t := range TransformTypes {
		items[i] = string(t)
	}

	selected, err := showSelectionList("选择转换类型", items)
	if err != nil {
		return err
	}

	if selected >= 0 {
		item.Rule.Transform = TransformTypes[selected]
		e.modified = true
		e.RefreshFilteredItems()
	}

	return nil
}

func (e *EditorState) changeTargetField() error {
	if len(e.filteredItems) == 0 || e.cursor >= len(e.filteredItems) {
		return nil
	}

	item := e.filteredItems[e.cursor]
	if item.Rule == nil {
		return nil
	}

	uncovered := e.GetUncoveredTargetFields()
	if len(uncovered) == 0 {
		return nil
	}

	selected, err := showSelectionList("选择目标字段", uncovered)
	if err != nil {
		return err
	}

	if selected >= 0 {
		oldTarget := item.Rule.Target
		item.Rule.Target = uncovered[selected]
		if oldTarget != item.Rule.Target {
			e.modified = true
			e.RefreshFilteredItems()
		}
	}

	return nil
}

func (e *EditorState) addMapping() error {
	unmappedSources := e.GetUnmappedSourceFields()
	if len(unmappedSources) == 0 {
		return nil
	}

	selSource, err := showSelectionList("选择源字段", unmappedSources)
	if err != nil {
		return err
	}
	if selSource < 0 {
		return nil
	}

	uncoveredTargets := e.GetUncoveredTargetFields()
	if len(uncoveredTargets) == 0 {
		return nil
	}

	selTarget, err := showSelectionList("选择目标字段", uncoveredTargets)
	if err != nil {
		return err
	}
	if selTarget < 0 {
		return nil
	}

	transformItems := make([]string, len(TransformTypes))
	for i, t := range TransformTypes {
		transformItems[i] = string(t)
	}

	selTransform, err := showSelectionList("选择转换类型", transformItems)
	if err != nil {
		return err
	}
	if selTransform < 0 {
		return nil
	}

	newRule := &mapper.MappingRule{
		Source:      unmappedSources[selSource],
		Target:      uncoveredTargets[selTarget],
		Transform:   TransformTypes[selTransform],
		Confidence:  1.0,
		NeedsReview: false,
		MatchType:   "manual",
	}

	e.rules.Mappings = append(e.rules.Mappings, newRule)
	e.modified = true
	e.RefreshFilteredItems()
	e.cursor = len(e.filteredItems) - 1

	return nil
}
