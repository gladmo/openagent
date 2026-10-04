package codingagent

// Port of modes/interactive/components/model-selector.ts: the searchable
// model selector with scoped/all toggle, default-first filtering, wrap
// navigation, and the current/default markers.

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

// ModelSelectorItem is one selectable model row.
type ModelSelectorItem struct {
	Provider string
	ID       string
	Model    *ai.Model
}

// ScopedModelRef pairs a scoped model with its thinking level.
type ScopedModelRef struct {
	Model         *ai.Model
	ThinkingLevel string
}

// DefaultModelRef names the saved default.
type DefaultModelRef struct {
	Provider string
	ID       string
}

// ModelSelectorOptions configure the selector.
type ModelSelectorOptions struct {
	CurrentModel      *ai.Model
	ScopedModels      []ScopedModelRef
	DefaultModel      *DefaultModelRef
	InitialSearch     string
	OnSelect          func(model *ai.Model)
	OnSelectAsDefault func(model *ai.Model)
	OnCancel          func()
}

// modelSelectorVisibleRows bounds the list window.
const modelSelectorVisibleRows = 10

// ModelSelectorComponent renders the model selector.
type ModelSelectorComponent struct {
	*tui.VStack

	activeModels   []ModelSelectorItem
	scopedItems    []ModelSelectorItem
	filteredModels []ModelSelectorItem
	selectedIndex  int
	currentModel   *ai.Model
	defaultModel   *DefaultModelRef
	scope          string // "all" | "scoped"
	searchValue    string
	options        ModelSelectorOptions
}

// NewModelSelectorComponent builds the selector.
func NewModelSelectorComponent(options ModelSelectorOptions) *ModelSelectorComponent {
	component := &ModelSelectorComponent{
		VStack:       tui.NewVStack(0),
		currentModel: options.CurrentModel,
		defaultModel: options.DefaultModel,
		options:      options,
	}
	if len(options.ScopedModels) > 0 {
		component.scope = "scoped"
	} else {
		component.scope = "all"
	}
	for _, scoped := range options.ScopedModels {
		if scoped.Model != nil {
			component.scopedItems = append(component.scopedItems, ModelSelectorItem{
				Provider: scoped.Model.Provider,
				ID:       scoped.Model.ID,
				Model:    scoped.Model,
			})
		}
	}
	component.setScope(component.scope)
	return component
}

// setScope swaps the active list and refilters.
func (c *ModelSelectorComponent) setScope(scope string) {
	c.scope = scope
	if scope == "scoped" && len(c.scopedItems) > 0 {
		c.activeModels = c.scopedItems
	} else {
		c.activeModels = c.allModelsForScope()
	}
	c.filterModels(c.searchValue)
}

func (c *ModelSelectorComponent) allModelsForScope() []ModelSelectorItem {
	// The full catalog rides the runtime seam; scoped-only when absent.
	return c.scopedItems
}

// Scope reports the current scope (tests).
func (c *ModelSelectorComponent) Scope() string { return c.scope }

// SearchValue reports the query (tests).
func (c *ModelSelectorComponent) SearchValue() string { return c.searchValue }

// SelectedIndex reports the cursor (tests).
func (c *ModelSelectorComponent) SelectedIndex() int { return c.selectedIndex }

// FilteredCount reports the filtered list length (tests).
func (c *ModelSelectorComponent) FilteredCount() int { return len(c.filteredModels) }

// isDefaultModel checks the saved default.
func (c *ModelSelectorComponent) isDefaultModel(model *ai.Model) bool {
	return c.defaultModel != nil && model != nil &&
		c.defaultModel.Provider == model.Provider && c.defaultModel.ID == model.ID
}

func (c *ModelSelectorComponent) isDefaultSearch(query string) bool {
	return strings.EqualFold(strings.TrimSpace(query), "default")
}

// filterModels applies the fuzzy query with default-first pinning on
// "default" searches; the cursor moves to the best match while queries
// clear back to the clamped position.
func (c *ModelSelectorComponent) filterModels(query string) {
	c.searchValue = query
	if query != "" {
		filtered := tui.FuzzyFilter(c.activeModels, query, func(item ModelSelectorItem) string {
			corpus := GetModelSelectorSearchText(ModelSearchItem{ID: item.ID, Provider: item.Provider})
			if c.isDefaultModel(item.Model) {
				corpus += " default"
			}
			return corpus
		})
		if c.isDefaultSearch(query) {
			var defaults, rest []ModelSelectorItem
			seen := map[string]bool{}
			for _, item := range c.activeModels {
				if c.isDefaultModel(item.Model) {
					defaults = append(defaults, item)
					seen[item.Provider+"\x00"+item.ID] = true
				}
			}
			for _, item := range filtered {
				if !seen[item.Provider+"\x00"+item.ID] {
					rest = append(rest, item)
				}
			}
			c.filteredModels = append(defaults, rest...)
		} else {
			c.filteredModels = filtered
		}
	} else {
		c.filteredModels = c.activeModels
	}
	if query != "" {
		c.selectedIndex = 0
	} else {
		max := len(c.filteredModels) - 1
		if max < 0 {
			max = 0
		}
		if c.selectedIndex > max {
			c.selectedIndex = max
		}
	}
	c.updateList()
}

// updateList renders the visible window with markers and scroll info.
func (c *ModelSelectorComponent) updateList() {
	c.VStack = tui.NewVStack(0)

	hint := "Only showing models from configured providers. Use /login to add providers."
	if len(c.options.ScopedModels) > 0 {
		hint = "Tab to toggle scoped/all models"
	}
	c.VStack.AddChild(tui.NewText(hint, 0, 0, nil), nil)
	c.VStack.AddChild(tui.NewSpacer(1), nil)
	if c.searchValue != "" {
		c.VStack.AddChild(tui.NewText("Search: "+c.searchValue, 0, 0, nil), nil)
	}

	startIndex := c.selectedIndex - modelSelectorVisibleRows/2
	if startIndex > len(c.filteredModels)-modelSelectorVisibleRows {
		startIndex = len(c.filteredModels) - modelSelectorVisibleRows
	}
	if startIndex < 0 {
		startIndex = 0
	}
	endIndex := startIndex + modelSelectorVisibleRows
	if endIndex > len(c.filteredModels) {
		endIndex = len(c.filteredModels)
	}

	for i := startIndex; i < endIndex; i++ {
		item := c.filteredModels[i]
		cursor := "  "
		if i == c.selectedIndex {
			cursor = "→ "
		}
		currentMarker := "  "
		if c.currentModel != nil && item.Model != nil &&
			c.currentModel.Provider == item.Model.Provider && c.currentModel.ID == item.Model.ID {
			currentMarker = "✓ "
		}
		defaultBadge := ""
		if c.isDefaultModel(item.Model) {
			defaultBadge = " · default"
		}
		line := fmt.Sprintf("%s%s%s [%s]%s", cursor, currentMarker, item.ID, item.Provider, defaultBadge)
		c.VStack.AddChild(tui.NewText(line, 0, 0, nil), nil)
	}

	if startIndex > 0 || endIndex < len(c.filteredModels) {
		scroll := fmt.Sprintf("  (%d/%d)", c.selectedIndex+1, len(c.filteredModels))
		c.VStack.AddChild(tui.NewText(scroll, 0, 0, nil), nil)
	}

	if len(c.filteredModels) == 0 {
		c.VStack.AddChild(tui.NewText("  No matching models", 0, 0, nil), nil)
	}
}

// HandleInput drives navigation and selection.
func (c *ModelSelectorComponent) HandleInput(key string) {
	switch key {
	case "up":
		if len(c.filteredModels) == 0 {
			return
		}
		if c.selectedIndex == 0 {
			c.selectedIndex = len(c.filteredModels) - 1
		} else {
			c.selectedIndex--
		}
		c.updateList()
	case "down":
		if len(c.filteredModels) == 0 {
			return
		}
		if c.selectedIndex == len(c.filteredModels)-1 {
			c.selectedIndex = 0
		} else {
			c.selectedIndex++
		}
		c.updateList()
	case "enter":
		if c.selectedIndex < len(c.filteredModels) {
			if c.options.OnSelect != nil {
				c.options.OnSelect(c.filteredModels[c.selectedIndex].Model)
			}
		}
	case "escape", "ctrl+c":
		if c.options.OnCancel != nil {
			c.options.OnCancel()
		}
	case "ctrl+s":
		if c.options.OnSelectAsDefault != nil && c.selectedIndex < len(c.filteredModels) {
			c.options.OnSelectAsDefault(c.filteredModels[c.selectedIndex].Model)
		}
	case "tab":
		if len(c.scopedItems) > 0 {
			if c.scope == "all" {
				c.setScope("scoped")
			} else {
				c.setScope("all")
			}
		}
	default:
		// Typed characters extend the query; backspace shrinks it.
		if key == "backspace" {
			if len(c.searchValue) > 0 {
				c.filterModels(c.searchValue[:len(c.searchValue)-1])
			}
			return
		}
		if len(key) == 1 {
			c.filterModels(c.searchValue + key)
		}
	}
}
