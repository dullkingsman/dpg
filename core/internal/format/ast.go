package format

// File is the top-level FormatAST node representing one .dpg source file.
type File struct {
	Path            string
	LeadingComments []string
	Objects         []ObjectNode
}

// ObjectNode is the common interface for all top-level and schema-nested nodes.
type ObjectNode interface {
	objectNode()
	// GetLeadingComments returns block/line comments immediately before this node.
	GetLeadingComments() []string
}

// baseNode holds the comment fields shared by every node type.
type baseNode struct {
	LeadingComments []string // block/line comments immediately before this node
	TrailingComment string   // inline comment on the same line (after , or ;)
}

func (b *baseNode) GetLeadingComments() []string { return b.LeadingComments }

// OpaqueNode holds a declaration that the format parser does not decompose
// further (e.g. FUNCTIONs whose bodies are opaque, or any object type not
// yet handled by the detail parser). The raw text is preserved verbatim;
// only comments are re-attached.
type OpaqueNode struct {
	baseNode
	// KindKeyword is the leading keyword(s) for this object kind (e.g. "ROLE",
	// "TABLE", "MATERIALIZED VIEW"). The scanner strips these from Part1, so
	// the renderer must re-prefix them.
	KindKeyword string
	// RawPart1 is the declaration text up to (but not including) the { block,
	// with the kind keyword already stripped.
	RawPart1 string
	// RawPart2 is the { } block text including braces, or "" when absent.
	RawPart2 string
}

func (n *OpaqueNode) objectNode() {}

// SchemaBlockNode is a SCHEMA name { ... } declaration.
type SchemaBlockNode struct {
	baseNode
	Name    string
	Objects []ObjectNode
	// RawAttrs is the raw schema attribute text collected from inside the block
	// (OWNER, COMMENT, GRANTS, etc.) that belongs to the schema itself.
	RawAttrs string
}

func (n *SchemaBlockNode) objectNode() {}

// TableNode is a TABLE / UNLOGGED TABLE declaration.
type TableNode struct {
	baseNode
	Unlogged bool
	Name     string
	Columns  []*ColumnNode
	// RawPart2 is the unparsed { } block; preserved verbatim pending a full
	// directive parser (planned for a future pass).
	RawPart2 string
}

func (n *TableNode) objectNode() {}

// MacroNode preserves a MACRO declaration verbatim. The formatter keeps macro
// bodies exactly as written since they are templates, not independent objects.
type MacroNode struct {
	baseNode
	// RawAfterKeyword is the text following the MACRO keyword — the name and
	// body (including the opening/closing delimiter), trimmed of leading space.
	RawAfterKeyword string
}

func (n *MacroNode) objectNode() {}

// ColumnNode is one column in a TABLE Part 1. It is not a top-level ObjectNode.
type ColumnNode struct {
	// BlankLineBefore is true when a blank line (empty line) precedes this
	// column's leading comments in the original source. The renderer preserves it.
	BlankLineBefore bool
	LeadingComments []string
	TrailingComment string
	// RawText is the column definition text as it appeared in the source,
	// trimmed of leading/trailing whitespace but otherwise verbatim.
	RawText string
}
