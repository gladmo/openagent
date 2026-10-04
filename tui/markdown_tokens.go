package tui

// The marked-compatible markdown token model: block and inline tokens the
// Markdown renderer consumes. Mirrors the marked v18 token shapes the
// reference's renderer switches on.

// MdToken is one markdown token (block or inline).
type MdToken interface{ mdTokenKind() string }

// ---- Block tokens ----

type MdHeading struct {
	Depth  int
	Tokens []MdToken
}

type MdParagraph struct {
	Tokens []MdToken
	Text   string
}

type MdCode struct {
	Lang string
	Text string
	Raw  string
}

type MdListItem struct {
	Task    bool
	Checked bool
	Tokens  []MdToken
	Raw     string
}

type MdList struct {
	Ordered bool
	Start   int
	Loose   bool
	Items   []*MdListItem
}

type MdTableCell struct {
	Tokens []MdToken
	Text   string
}

type MdTable struct {
	Header []*MdTableCell
	Align  []string // "" | "center" | "left" | "right"
	Rows   [][]*MdTableCell
	Raw    string
}

type MdBlockquote struct {
	Tokens []MdToken
}

type MdHr struct{}

type MdSpace struct{}

type MdHtml struct {
	Raw string
}

type MdTextBlock struct {
	Text   string
	Tokens []MdToken
}

type MdLatexBlock struct {
	Text    string
	Raw     string
	Pending bool
}

func (MdHeading) mdTokenKind() string    { return "heading" }
func (MdParagraph) mdTokenKind() string  { return "paragraph" }
func (MdCode) mdTokenKind() string       { return "code" }
func (*MdList) mdTokenKind() string      { return "list" }
func (MdTable) mdTokenKind() string      { return "table" }
func (MdBlockquote) mdTokenKind() string { return "blockquote" }
func (MdHr) mdTokenKind() string         { return "hr" }
func (MdSpace) mdTokenKind() string      { return "space" }
func (MdHtml) mdTokenKind() string       { return "html" }
func (MdTextBlock) mdTokenKind() string  { return "text" }
func (MdLatexBlock) mdTokenKind() string { return "latexBlock" }

// ---- Inline tokens ----

type MdText struct {
	Text   string
	Tokens []MdToken
}

type MdStrong struct{ Tokens []MdToken }
type MdEm struct{ Tokens []MdToken }
type MdCodespan struct{ Text string }
type MdLink struct {
	Href   string
	Text   string
	Tokens []MdToken
}
type MdDel struct{ Tokens []MdToken }
type MdEscape struct {
	Text string
	Raw  string
}
type MdCheckbox struct {
	Checked bool
}

func (MdCheckbox) mdTokenKind() string { return "checkbox" }

type MdBr struct{}
type MdLatex struct {
	Text    string
	Raw     string
	Pending bool
}

func (MdText) mdTokenKind() string     { return "text" }
func (MdStrong) mdTokenKind() string   { return "strong" }
func (MdEm) mdTokenKind() string       { return "em" }
func (MdCodespan) mdTokenKind() string { return "codespan" }
func (MdLink) mdTokenKind() string     { return "link" }
func (MdDel) mdTokenKind() string      { return "del" }
func (MdEscape) mdTokenKind() string   { return "escape" }
func (MdBr) mdTokenKind() string       { return "br" }
func (MdLatex) mdTokenKind() string    { return "latex" }
