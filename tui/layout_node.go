package tui

// Port of layout-node.ts: layout nodes describe the box tree a layout-capable
// component exposes (stacks with flex-like sizing, scroll views). The TS
// Symbol-keyed method becomes the LayoutNode() interface method.

// LayoutViewport is the layout viewport size.
type LayoutViewport struct {
	Width  int
	Height int
}

// StackBasis is a fixed basis size, or auto (nil) to measure the component.
type StackBasis struct {
	Auto  bool
	Value int
}

// StackLayoutEntry is one stack child with flex sizing options.
type StackLayoutEntry struct {
	Component Component
	Basis     *StackBasis
	Grow      *int
	Shrink    *int
	MinSize   *int
	MaxSize   *int
	Visible   func(viewport LayoutViewport) bool
}

// StackAlign controls cross-axis alignment.
type StackAlign string

const (
	StackAlignStretch StackAlign = "stretch"
	StackAlignStart   StackAlign = "start"
	StackAlignCenter  StackAlign = "center"
	StackAlignEnd     StackAlign = "end"
)

// StackLayoutNode lays children out vertically (vstack) or horizontally
// (hstack).
type StackLayoutNode struct {
	Type    string // "vstack" | "hstack"
	Entries []StackLayoutEntry
	Gap     int
	Align   StackAlign
}

// ScrollLayoutState is the scroll view's contribution to layout.
type ScrollLayoutState interface {
	ScrollTop() int
	Primary() bool
	Overscroll() string // "chain" | "contain"
	ViewportHeight() int
	GetContentWidth(width int) int
	UpdateLayout(contentHeight, viewportHeight int, requestRender func())
}

// ScrollLayoutNode wraps one component in a scroll viewport.
type ScrollLayoutNode struct {
	Component Component
	State     ScrollLayoutState
}

// LayoutNode is the sealed union of layout node types.
type LayoutNode interface{ layoutNodeKind() string }

func (StackLayoutNode) layoutNodeKind() string   { return "stack" }
func (*ScrollLayoutNode) layoutNodeKind() string { return "scroll" }

// LayoutComponent is a Component that participates in layout.
type LayoutComponent interface {
	Component
	LayoutNode() LayoutNode
}

// GetLayoutNode returns the component's layout node, or nil.
func GetLayoutNode(component Component) LayoutNode {
	if layout, ok := component.(LayoutComponent); ok {
		return layout.LayoutNode()
	}
	return nil
}
