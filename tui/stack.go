package tui

// Port of components/stack.ts: VStack/HStack flex-like sizing with the
// grow/shrink distribution algorithm.

const maxSafeInteger = 9007199254740991

// StackEntryOptions configure one stack child.
type StackEntryOptions struct {
	Basis   *StackBasis
	Grow    *int
	Shrink  *int
	MinSize *int
	MaxSize *int
	Visible func(viewport LayoutViewport) bool
}

// Stack is the shared VStack/HStack implementation.
type Stack struct {
	Container
	Entries    []StackLayoutEntry
	GapValue   int
	Align      StackAlign
	LayoutType string // "vstack" | "hstack"
}

func normalizeStackSize(value int, fallback int) int {
	return maxInt(0, value)
}

// NewStack builds a stack; children are Components or (Component, options)
// pairs added with AddChild.
func NewStack(layoutType string, align StackAlign, gap int) *Stack {
	if align == "" {
		align = StackAlignStretch
	}
	return &Stack{LayoutType: layoutType, Align: align, GapValue: normalizeStackSize(gap, 0)}
}

// AddChild appends a child with optional layout options.
func (s *Stack) AddChild(component Component, options *StackEntryOptions) {
	s.Container.AddChild(component)
	entry := StackLayoutEntry{Component: component}
	if options != nil {
		if options.Basis != nil {
			entry.Basis = options.Basis
		}
		if options.Grow != nil {
			value := normalizeStackSize(*options.Grow, 0)
			entry.Grow = &value
		}
		if options.Shrink != nil {
			value := normalizeStackSize(*options.Shrink, 1)
			entry.Shrink = &value
		}
		if options.MinSize != nil {
			value := normalizeStackSize(*options.MinSize, 0)
			entry.MinSize = &value
		}
		if options.MaxSize != nil {
			value := normalizeStackSize(*options.MaxSize, maxSafeInteger)
			entry.MaxSize = &value
		}
		entry.Visible = options.Visible
	}
	s.Entries = append(s.Entries, entry)
}

// RemoveChild removes a child and its layout entry.
func (s *Stack) RemoveChild(component Component) {
	s.Container.RemoveChild(component)
	for i, entry := range s.Entries {
		if entry.Component == component {
			s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
			return
		}
	}
}

// Clear removes all children and entries.
func (s *Stack) Clear() {
	s.Container.Clear()
	s.Entries = nil
}

// LayoutNode exposes the stack layout node.
func (s *Stack) LayoutNode() LayoutNode {
	return StackLayoutNode{Type: s.LayoutType, Entries: s.Entries, Gap: s.GapValue, Align: s.Align}
}

// VStack stacks children vertically: render concatenates visible entries with
// gap rows and min-size padding.
type VStack struct{ Stack }

// NewVStack builds a vertical stack.
func NewVStack(gap int) *VStack {
	return &VStack{Stack{LayoutType: "vstack", Align: StackAlignStretch, GapValue: gap}}
}

// Render concatenates visible children, slicing/padding each to its
// allocated size.
func (s *VStack) Render(width int) []string {
	viewport := LayoutViewport{Width: maxInt(1, width), Height: maxSafeInteger}
	entries := VisibleStackEntries(s.Entries, viewport)
	rendered := make([][]string, len(entries))
	intrinsics := make([]int, len(entries))
	for i, entry := range entries {
		rendered[i] = entry.Component.Render(viewport.Width)
		intrinsics[i] = len(rendered[i])
	}
	sizes := AllocateStackSizes(entries, intrinsics, nil, s.GapValue)
	var lines []string
	for index := 0; index < len(entries); index++ {
		if index > 0 {
			for gap := 0; gap < s.GapValue; gap++ {
				lines = append(lines, "")
			}
		}
		childLines := rendered[index]
		if len(childLines) > sizes[index] {
			childLines = childLines[:sizes[index]]
		}
		lines = append(lines, childLines...)
		for padding := len(childLines); padding < sizes[index]; padding++ {
			lines = append(lines, "")
		}
	}
	return lines
}

// HStack stacks children horizontally: render composites children side by
// side at allocated widths.
type HStack struct{ Stack }

// NewHStack builds a horizontal stack.
func NewHStack(gap int) *HStack {
	return &HStack{Stack{LayoutType: "hstack", Align: StackAlignStretch, GapValue: gap}}
}

// Render composites visible children horizontally with cross-axis alignment.
func (s *HStack) Render(width int) []string {
	safeWidth := maxInt(1, width)
	viewport := LayoutViewport{Width: safeWidth, Height: maxSafeInteger}
	entries := VisibleStackEntries(s.Entries, viewport)
	if len(entries) == 0 {
		return nil
	}

	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		lines := entry.Component.Render(safeWidth)
		max := 0
		for _, line := range lines {
			if w := VisibleWidth(line); w > max {
				max = w
			}
		}
		intrinsicWidths[i] = max
	}
	widths := AllocateStackSizes(entries, intrinsicWidths, &safeWidth, s.GapValue)
	rendered := make([][]string, len(entries))
	for i, entry := range entries {
		if widths[i] == 0 {
			rendered[i] = nil
		} else {
			rendered[i] = entry.Component.Render(widths[i])
		}
	}
	height := 0
	for _, lines := range rendered {
		if len(lines) > height {
			height = len(lines)
		}
	}
	result := make([]string, height)
	x := 0
	for index, lines := range rendered {
		childWidth := widths[index]
		offset := 0
		if s.Align == StackAlignCenter {
			offset = (height - len(lines)) / 2
		} else if s.Align == StackAlignEnd {
			offset = height - len(lines)
		}
		for row := 0; row < len(lines); row++ {
			target := row + offset
			if target < 0 || target >= len(result) {
				continue
			}
			result[target] = CompositeTuiLine(result[target], lines[row], x, childWidth, safeWidth)
		}
		x += childWidth + s.GapValue
	}
	return result
}

// VisibleStackEntries filters entries by their visibility predicates.
func VisibleStackEntries(entries []StackLayoutEntry, viewport LayoutViewport) []StackLayoutEntry {
	var visible []StackLayoutEntry
	for _, entry := range entries {
		if entry.Visible == nil || entry.Visible(viewport) {
			visible = append(visible, entry)
		}
	}
	return visible
}

func clampStackSize(size int, entry StackLayoutEntry) int {
	min := 0
	if entry.MinSize != nil {
		min = maxInt(0, *entry.MinSize)
	}
	max := maxSafeInteger
	if entry.MaxSize != nil {
		max = *entry.MaxSize
	}
	if max < min {
		max = min
	}
	return maxInt(min, minInt(max, maxInt(0, size)))
}

func entryGrow(entry StackLayoutEntry) int {
	if entry.Grow != nil {
		return *entry.Grow
	}
	return 0
}

func entryShrink(entry StackLayoutEntry) int {
	if entry.Shrink != nil {
		return *entry.Shrink
	}
	return 1
}

func entryMinSize(entry StackLayoutEntry) int {
	if entry.MinSize != nil {
		return *entry.MinSize
	}
	return 0
}

func entryMaxSize(entry StackLayoutEntry) int {
	if entry.MaxSize != nil {
		return *entry.MaxSize
	}
	return maxSafeInteger
}

func distributeStackSizes(sizes []int, entries []StackLayoutEntry, amount int, mode string) {
	remaining := amount
	for remaining > 0 {
		type candidate struct {
			entry StackLayoutEntry
			index int
		}
		var candidates []candidate
		for index, entry := range entries {
			if mode == "grow" {
				if entryGrow(entry) > 0 && sizes[index] < entryMaxSize(entry) {
					candidates = append(candidates, candidate{entry, index})
				}
			} else {
				if entryShrink(entry) > 0 && sizes[index] > entryMinSize(entry) {
					candidates = append(candidates, candidate{entry, index})
				}
			}
		}
		if len(candidates) == 0 {
			return
		}

		totalWeight := 0.0
		for _, c := range candidates {
			if mode == "grow" {
				totalWeight += float64(entryGrow(c.entry))
			} else {
				totalWeight += float64(entryShrink(c.entry)) * float64(maxInt(1, sizes[c.index]))
			}
		}
		distributed := 0
		for _, c := range candidates {
			if remaining <= 0 {
				break
			}
			var weight float64
			if mode == "grow" {
				weight = float64(entryGrow(c.entry))
			} else {
				weight = float64(entryShrink(c.entry)) * float64(maxInt(1, sizes[c.index]))
			}
			proposed := maxInt(1, int(jsFloor((float64(remaining)*weight)/totalWeight)))
			var capacity int
			if mode == "grow" {
				capacity = entryMaxSize(c.entry) - sizes[c.index]
			} else {
				capacity = sizes[c.index] - entryMinSize(c.entry)
			}
			delta := minInt(remaining, minInt(proposed, capacity))
			if delta <= 0 {
				continue
			}
			if mode == "grow" {
				sizes[c.index] += delta
			} else {
				sizes[c.index] -= delta
			}
			remaining -= delta
			distributed += delta
		}
		if distributed == 0 {
			return
		}
	}
}

// AllocateStackSizes resolves stack child sizes against the available space:
// basis (or measured intrinsic size) first, then grow/shrink distribution.
func AllocateStackSizes(entries []StackLayoutEntry, intrinsicSizes []int, availableSize *int, gap int) []int {
	sizes := make([]int, len(entries))
	for index, entry := range entries {
		size := 0
		switch {
		case entry.Basis == nil:
			if index < len(intrinsicSizes) {
				size = intrinsicSizes[index]
			}
		case entry.Basis.Auto:
			if index < len(intrinsicSizes) {
				size = intrinsicSizes[index]
			}
		default:
			size = entry.Basis.Value
		}
		sizes[index] = clampStackSize(size, entry)
	}
	if availableSize == nil {
		return sizes
	}

	contentSize := maxInt(0, *availableSize-maxInt(0, len(entries)-1)*gap)
	total := 0
	for _, size := range sizes {
		total += size
	}
	if total < contentSize {
		distributeStackSizes(sizes, entries, contentSize-total, "grow")
	} else if total > contentSize {
		distributeStackSizes(sizes, entries, total-contentSize, "shrink")
	}
	return sizes
}
