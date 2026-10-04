package tui

// Port of components/settings-list.ts: settings list with optional search,
// value cycling, and submenu navigation.

// SettingSubmenuDone closes a submenu, optionally selecting a value and
// navigating to another item after close.
type SettingSubmenuDone func(selectedValue string, navigateTo string)

// SettingItem is one settings row.
type SettingItem struct {
	ID           string
	Label        string
	Description  string
	CurrentValue string
	// Values cycles on Enter/Space when non-empty.
	Values []string
	// Submenu opens on Enter; receives the current value and a done callback.
	Submenu func(currentValue string, done SettingSubmenuDone) Component
}

// SettingsListTheme styles the list.
type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

// SettingsListOptions toggles search.
type SettingsListOptions struct {
	EnableSearch bool
}

// SettingsList renders a searchable, cyclable settings list with submenus.
type SettingsList struct {
	items             []SettingItem
	filteredItems     []SettingItem
	theme             SettingsListTheme
	selectedIndex     int
	mousePressedIndex *int
	maxVisible        int
	onChange          func(id, newValue string)
	onCancel          func()
	searchInput       *Input
	searchEnabled     bool

	submenuComponent   Component
	submenuItemIndex   *int
	navigateAfterClose string
}

// NewSettingsList builds the list.
func NewSettingsList(items []SettingItem, maxVisible int, theme SettingsListTheme, onChange func(id, newValue string), onCancel func(), options SettingsListOptions) *SettingsList {
	list := &SettingsList{
		items:         items,
		filteredItems: items,
		maxVisible:    maxVisible,
		theme:         theme,
		onChange:      onChange,
		onCancel:      onCancel,
		searchEnabled: options.EnableSearch,
	}
	if list.searchEnabled {
		list.searchInput = NewInput(InputOptions{})
	}
	return list
}

// UpdateValue replaces an item's current value.
func (s *SettingsList) UpdateValue(id, newValue string) {
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].CurrentValue = newValue
			return
		}
	}
}

// SelectItem moves selection to the item with the given id (no-op when
// missing).
func (s *SettingsList) SelectItem(id string) {
	items := s.items
	if s.searchEnabled {
		items = s.filteredItems
	}
	for index, item := range items {
		if item.ID == id {
			s.selectedIndex = index
			return
		}
	}
}

// Invalidate forwards to the active submenu.
func (s *SettingsList) Invalidate() {
	if s.submenuComponent != nil {
		s.submenuComponent.Invalidate()
	}
}

// Render draws the submenu when active, else the main list.
func (s *SettingsList) Render(width int) []string {
	if s.submenuComponent != nil {
		return s.submenuComponent.Render(width)
	}
	return s.renderMainList(width)
}

func (s *SettingsList) renderMainList(width int) []string {
	var lines []string

	if s.searchEnabled && s.searchInput != nil {
		lines = append(lines, s.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	if len(s.items) == 0 {
		lines = append(lines, s.theme.Hint("  No settings available"))
		if s.searchEnabled {
			s.addHintLine(&lines, width)
		}
		return lines
	}

	displayItems := s.getDisplayItems()
	if len(displayItems) == 0 {
		lines = append(lines, TruncateToWidth(s.theme.Hint("  No matching settings"), width, "...", false))
		s.addHintLine(&lines, width)
		return lines
	}

	startIndex, endIndex := s.getVisibleRange(displayItems)

	// Max label width for alignment.
	maxLabelWidth := 0
	for _, item := range s.items {
		if w := VisibleWidth(item.Label); w > maxLabelWidth {
			maxLabelWidth = w
		}
	}
	maxLabelWidth = minInt(36, maxLabelWidth)

	for i := startIndex; i < endIndex; i++ {
		item := displayItems[i]
		isSelected := i == s.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = s.theme.Cursor
		}
		prefixWidth := VisibleWidth(prefix)

		labelPadded := item.Label + stringRepeat(" ", maxInt(0, maxLabelWidth-VisibleWidth(item.Label)))
		labelText := s.theme.Label(labelPadded, isSelected)

		separator := "  "
		usedWidth := prefixWidth + maxLabelWidth + VisibleWidth(separator)
		valueMaxWidth := width - usedWidth - 2

		valueText := s.theme.Value(TruncateToWidth(item.CurrentValue, valueMaxWidth, "", false), isSelected)

		lines = append(lines, TruncateToWidth(prefix+labelText+separator+valueText, width, "...", false))
	}

	if startIndex > 0 || endIndex < len(displayItems) {
		scrollText := "  (" + itoa(s.selectedIndex+1) + "/" + itoa(len(displayItems)) + ")"
		lines = append(lines, s.theme.Hint(TruncateToWidth(scrollText, width-2, "", false)))
	}

	if s.selectedIndex >= 0 && s.selectedIndex < len(displayItems) {
		if selectedItem := displayItems[s.selectedIndex]; selectedItem.Description != "" {
			lines = append(lines, "")
			for _, line := range WrapTextWithAnsi(selectedItem.Description, width-4) {
				lines = append(lines, s.theme.Description("  "+line))
			}
		}
	}

	s.addHintLine(&lines, width)
	return lines
}

// HandleMouse routes into the submenu, search input, or list rows.
func (s *SettingsList) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	if s.submenuComponent != nil {
		if result := DispatchMouseEvent(s.submenuComponent, event); result != nil {
			result.Focus = true
			return &result.TuiMouseEventResult
		}
		return nil
	}

	if s.searchEnabled && s.searchInput != nil {
		if event.Y == 0 {
			if result := DispatchMouseEvent(s.searchInput, event); result != nil {
				result.Focus = true
				return &result.TuiMouseEventResult
			}
			return nil
		}
		if event.Y == 1 {
			return nil
		}
	}

	displayItems := s.getDisplayItems()
	if len(displayItems) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != nil {
		delta := 1
		if *event.WheelDelta < 0 {
			delta = -1
		}
		previousIndex := s.selectedIndex
		s.selectedIndex = maxInt(0, minInt(len(displayItems)-1, s.selectedIndex+delta))
		render := s.selectedIndex != previousIndex
		return &TuiMouseEventResult{Handled: true, Render: &render}
	}
	if event.Button != MouseLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}

	rowOffset := 0
	if s.searchEnabled {
		rowOffset = 2
	}
	startIndex, endIndex := s.getVisibleRange(displayItems)
	itemIndex := startIndex + event.Y - rowOffset
	if itemIndex < startIndex || itemIndex >= endIndex {
		return nil
	}
	if event.Type == MousePress {
		s.mousePressedIndex = &itemIndex
		s.selectedIndex = itemIndex
		return &TuiMouseEventResult{Handled: true, Focus: true}
	}
	clickedIndex := itemIndex
	if s.mousePressedIndex != nil {
		clickedIndex = *s.mousePressedIndex
		s.mousePressedIndex = nil
	}
	s.selectedIndex = clickedIndex
	s.activateItem()
	return &TuiMouseEventResult{Handled: true}
}

// HandleInput navigates, activates, or edits the search query.
func (s *SettingsList) HandleInput(data string) {
	// Submenu active: delegate all input; its cancel closes via done().
	if s.submenuComponent != nil {
		if handler, ok := s.submenuComponent.(InputHandler); ok {
			handler.HandleInput(data)
		}
		return
	}

	kb := GetKeybindings()
	displayItems := s.getDisplayItems()
	searchEmpty := !s.searchEnabled || s.searchInput == nil || s.searchInput.GetValue() == ""
	switch {
	case kb.Matches(data, "tui.select.up"):
		if len(displayItems) == 0 {
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(displayItems) - 1
		} else {
			s.selectedIndex--
		}
	case kb.Matches(data, "tui.select.down"):
		if len(displayItems) == 0 {
			return
		}
		if s.selectedIndex == len(displayItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
	case kb.Matches(data, "tui.select.confirm") || (data == " " && searchEmpty):
		s.activateItem()
	case kb.Matches(data, "tui.select.cancel"):
		s.onCancel()
	case s.searchEnabled && s.searchInput != nil:
		s.searchInput.HandleInput(data)
		s.applyFilter(s.searchInput.GetValue())
	}
}

func (s *SettingsList) getDisplayItems() []SettingItem {
	if s.searchEnabled {
		return s.filteredItems
	}
	return s.items
}

func (s *SettingsList) getVisibleRange(displayItems []SettingItem) (int, int) {
	startIndex := maxInt(0, minInt(s.selectedIndex-s.maxVisible/2, len(displayItems)-s.maxVisible))
	endIndex := minInt(startIndex+s.maxVisible, len(displayItems))
	return startIndex, endIndex
}

func (s *SettingsList) activateItem() {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.getDisplayItems()) {
		return
	}
	item := &s.getDisplayItems()[s.selectedIndex]

	if item.Submenu != nil {
		index := s.selectedIndex
		s.submenuItemIndex = &index
		s.submenuComponent = item.Submenu(item.CurrentValue, func(selectedValue, navigateTo string) {
			if selectedValue != "" {
				item.CurrentValue = selectedValue
				s.onChange(item.ID, selectedValue)
			}
			if navigateTo != "" {
				s.navigateAfterClose = navigateTo
			}
			s.closeSubmenu()
		})
	} else if len(item.Values) > 0 {
		currentIndex := -1
		for i, value := range item.Values {
			if value == item.CurrentValue {
				currentIndex = i
				break
			}
		}
		nextIndex := (currentIndex + 1) % len(item.Values)
		newValue := item.Values[nextIndex]
		item.CurrentValue = newValue
		s.onChange(item.ID, newValue)
	}
}

func (s *SettingsList) closeSubmenu() {
	s.submenuComponent = nil
	if s.navigateAfterClose != "" {
		id := s.navigateAfterClose
		s.navigateAfterClose = ""
		s.submenuItemIndex = nil
		s.SelectItem(id)
		// Open the target item's submenu automatically.
		s.activateItem()
	} else if s.submenuItemIndex != nil {
		// Restore selection to the item that opened the submenu.
		s.selectedIndex = *s.submenuItemIndex
		s.submenuItemIndex = nil
	}
}

func (s *SettingsList) applyFilter(query string) {
	s.filteredItems = FuzzyFilter(s.items, query, func(item SettingItem) string {
		return item.Label
	})
	s.selectedIndex = 0
}

func (s *SettingsList) addHintLine(lines *[]string, width int) {
	*lines = append(*lines, "")
	hint := "  Enter/Space to change · Esc to cancel"
	if s.searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	*lines = append(*lines, TruncateToWidth(s.theme.Hint(hint), width, "...", false))
}
