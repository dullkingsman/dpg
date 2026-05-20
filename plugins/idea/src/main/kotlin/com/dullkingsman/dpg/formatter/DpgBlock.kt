package com.dullkingsman.dpg.formatter

import com.dullkingsman.dpg.lang.DpgElementTypes as E
import com.dullkingsman.dpg.lang.DpgTokenTypes as T
import com.intellij.formatting.*
import com.intellij.lang.ASTNode
import com.intellij.psi.formatter.common.AbstractBlock
import com.intellij.psi.codeStyle.CodeStyleSettings
import com.intellij.psi.tree.IElementType

class DpgBlock(
    private val node: ASTNode,
    wrap: Wrap?,
    alignment: Alignment?,
    private val myIndent: Indent,
    private val settings: CodeStyleSettings,
) : AbstractBlock(node, wrap, alignment) {

    override fun getIndent(): Indent = myIndent

    override fun isLeaf(): Boolean = node.firstChildNode == null

    override fun buildChildren(): MutableList<Block> {
        if (isLeaf) return mutableListOf()
        val result = mutableListOf<Block>()
        var child: ASTNode? = node.firstChildNode
        while (child != null) {
            if (child.elementType != T.WHITE_SPACE) {
                result += DpgBlock(child, null, null, childIndent(child), settings)
            }
            child = child.treeNext
        }
        return result
    }

    // ── Indentation ───────────────────────────────────────────────────────────

    private fun childIndent(child: ASTNode): Indent {
        val childType = child.elementType
        return when {
            // Non-brace content inside any { } block is indented 4 spaces
            node.elementType == E.PART2_BLOCK
                && childType != T.LBRACE
                && childType != T.RBRACE -> Indent.getNormalIndent()

            else -> Indent.getNoneIndent()
        }
    }

    override fun getChildAttributes(newChildIndex: Int): ChildAttributes =
        when (node.elementType) {
            E.PART2_BLOCK -> ChildAttributes(Indent.getNormalIndent(), null)
            else          -> ChildAttributes(Indent.getNoneIndent(), null)
        }

    // ── Spacing ───────────────────────────────────────────────────────────────

    override fun getSpacing(child1: Block?, child2: Block): Spacing? {
        if (child1 == null) return null
        val c1 = (child1 as? DpgBlock)?.node ?: return null
        val c2 = (child2 as? DpgBlock)?.node ?: return null
        val parent = node.elementType

        return when {

            // ── FILE: blank line between top-level declarations ───────────────
            parent == E.FILE && isTopLevelDecl(c1.elementType) && isTopLevelDecl(c2.elementType) ->
                blankLine()

            // ── OBJECT_DECLARATION internal spacing ──────────────────────────
            parent == E.OBJECT_DECLARATION
                && c1.elementType == E.OBJECT_KEYWORD_SEQ
                && c2.elementType == E.QUALIFIED_NAME ->
                singleSpace()

            parent == E.OBJECT_DECLARATION
                && c1.elementType == E.QUALIFIED_NAME
                && c2.elementType == E.PART1_BODY ->
                singleSpace()

            parent == E.OBJECT_DECLARATION
                && c1.elementType == E.PART1_BODY
                && c2.elementType == E.PART2_BLOCK ->
                singleSpace()

            // ── SCHEMA_BLOCK internal spacing ────────────────────────────────
            parent == E.SCHEMA_BLOCK
                && c1.elementType == E.OBJECT_KEYWORD_SEQ
                && c2.elementType == E.QUALIFIED_NAME ->
                singleSpace()

            parent == E.SCHEMA_BLOCK
                && c1.elementType == E.QUALIFIED_NAME
                && c2.elementType == E.PART2_BLOCK ->
                singleSpace()

            // ── MACRO_DECLARATION internal spacing ───────────────────────────
            parent == E.MACRO_DECLARATION && c1.elementType == T.MACRO_KW ->
                singleSpace()

            // ── PART2_BLOCK interior ─────────────────────────────────────────
            parent == E.PART2_BLOCK && c1.elementType == T.LBRACE ->
                newline()

            parent == E.PART2_BLOCK && c2.elementType == T.RBRACE ->
                newline()

            // Blank line between nested declarations (schema body)
            parent == E.PART2_BLOCK
                && isTopLevelDecl(c1.elementType)
                && isTopLevelDecl(c2.elementType) ->
                blankLine()

            parent == E.PART2_BLOCK ->
                newline()

            else -> null
        }
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private fun isTopLevelDecl(type: IElementType) =
        type == E.OBJECT_DECLARATION || type == E.SCHEMA_BLOCK || type == E.MACRO_DECLARATION

    /** Single space, no line break. */
    private fun singleSpace() = Spacing.createSpacing(1, 1, 0, false, 0)

    /** Exactly one newline. */
    private fun newline() = Spacing.createSpacing(0, 0, 1, false, 0)

    /** One blank line (two newlines), preserving existing blank lines up to one extra. */
    private fun blankLine() = Spacing.createSpacing(0, 0, 2, true, 1)
}
