package editor

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/schema-mapper/schema-mapper/pkg/mapper"
)

func TestNewEditor(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_mapping_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	rules := &mapper.MappingRules{
		Mappings: []*mapper.MappingRule{
			{
				Source:      "id",
				Target:      "userId",
				Transform:   mapper.TransformDirect,
				Confidence:  1.0,
				NeedsReview: false,
			},
			{
				Source:      "name",
				Target:      "userName",
				Transform:   mapper.TransformCast,
				Confidence:  0.6,
				NeedsReview: true,
			},
		},
	}

	content, _ := yaml.Marshal(rules)
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	editor, err := NewEditor(tmpFile.Name(), "", "")
	if err != nil {
		t.Fatalf("Failed to create editor: %v", err)
	}

	if editor == nil {
		t.Fatal("Editor is nil")
	}

	if len(editor.rules.Mappings) != 2 {
		t.Errorf("Expected 2 mappings, got %d", len(editor.rules.Mappings))
	}

	editor.RefreshFilteredItems()
	if len(editor.GetFilteredItems()) != 2 {
		t.Errorf("Expected 2 filtered items, got %d", len(editor.GetFilteredItems()))
	}
}

func TestFilterMode(t *testing.T) {
	editor := &EditorState{
		rules: &mapper.MappingRules{
			Mappings: []*mapper.MappingRule{
				{Source: "a", Target: "x", NeedsReview: false},
				{Source: "b", Target: "y", NeedsReview: true},
				{Source: "c", Target: "z", NeedsReview: true},
			},
		},
		filterMode: FilterAll,
	}

	editor.RefreshFilteredItems()
	if len(editor.GetFilteredItems()) != 3 {
		t.Errorf("FilterAll: expected 3 items, got %d", len(editor.GetFilteredItems()))
	}

	editor.SetFilterMode(FilterNeedsReview)
	editor.RefreshFilteredItems()
	if len(editor.GetFilteredItems()) != 2 {
		t.Errorf("FilterNeedsReview: expected 2 items, got %d", len(editor.GetFilteredItems()))
	}

	editor.cycleFilterMode()
	if editor.filterMode != FilterUnmapped {
		t.Errorf("Expected FilterUnmapped, got %d", editor.filterMode)
	}
}

func TestSearch(t *testing.T) {
	editor := &EditorState{
		rules: &mapper.MappingRules{
			Mappings: []*mapper.MappingRule{
				{Source: "user_id", Target: "userId"},
				{Source: "user_name", Target: "userName"},
				{Source: "email", Target: "emailAddress"},
			},
		},
		filterMode: FilterAll,
	}

	editor.searchQuery = "user"
	editor.RefreshFilteredItems()
	if len(editor.GetFilteredItems()) != 2 {
		t.Errorf("Search 'user': expected 2 items, got %d", len(editor.GetFilteredItems()))
	}

	editor.searchQuery = "email"
	editor.RefreshFilteredItems()
	if len(editor.GetFilteredItems()) != 1 {
		t.Errorf("Search 'email': expected 1 item, got %d", len(editor.GetFilteredItems()))
	}
}

func TestStats(t *testing.T) {
	editor := &EditorState{
		rules: &mapper.MappingRules{
			Mappings: []*mapper.MappingRule{
				{Source: "a", Target: "x", NeedsReview: false},
				{Source: "b", Target: "y", NeedsReview: true},
				{Source: "", Target: "", NeedsReview: false},
			},
		},
	}

	mapped, total, needsReview, _, _ := editor.GetStats()
	if mapped != 2 {
		t.Errorf("Expected 2 mapped, got %d", mapped)
	}
	if total != 3 {
		t.Errorf("Expected 3 total, got %d", total)
	}
	if needsReview != 1 {
		t.Errorf("Expected 1 needsReview, got %d", needsReview)
	}
}

func TestDeleteMapping(t *testing.T) {
	editor := &EditorState{
		rules: &mapper.MappingRules{
			Mappings: []*mapper.MappingRule{
				{Source: "a", Target: "x"},
				{Source: "b", Target: "y"},
				{Source: "c", Target: "z"},
			},
		},
	}
	editor.RefreshFilteredItems()
	editor.cursor = 1

	editor.deleteMapping()

	if len(editor.rules.Mappings) != 2 {
		t.Errorf("Expected 2 mappings after delete, got %d", len(editor.rules.Mappings))
	}
	if editor.rules.Mappings[0].Source != "a" || editor.rules.Mappings[1].Source != "c" {
		t.Error("Wrong mapping deleted")
	}
	if !editor.modified {
		t.Error("Modified flag should be set")
	}
}

func TestConfirmMapping(t *testing.T) {
	editor := &EditorState{
		rules: &mapper.MappingRules{
			Mappings: []*mapper.MappingRule{
				{Source: "a", Target: "x", NeedsReview: true},
			},
		},
	}
	editor.RefreshFilteredItems()
	editor.cursor = 0

	editor.confirmMapping()

	if editor.rules.Mappings[0].NeedsReview {
		t.Error("NeedsReview should be false after confirm")
	}
	if !editor.modified {
		t.Error("Modified flag should be set")
	}
}

func TestMoveCursor(t *testing.T) {
	editor := &EditorState{
		rules: &mapper.MappingRules{
			Mappings: []*mapper.MappingRule{
				{Source: "a", Target: "x"},
				{Source: "b", Target: "y"},
				{Source: "c", Target: "z"},
			},
		},
		screenHeight: 20,
	}
	editor.RefreshFilteredItems()

	editor.cursor = 1
	editor.moveCursor(1)
	if editor.cursor != 2 {
		t.Errorf("Expected cursor 2, got %d", editor.cursor)
	}

	editor.moveCursor(1)
	if editor.cursor != 2 {
		t.Errorf("Cursor should not go beyond last item, got %d", editor.cursor)
	}

	editor.moveCursor(-1)
	if editor.cursor != 1 {
		t.Errorf("Expected cursor 1, got %d", editor.cursor)
	}

	editor.moveCursor(-10)
	if editor.cursor != 0 {
		t.Errorf("Cursor should not go below 0, got %d", editor.cursor)
	}
}
