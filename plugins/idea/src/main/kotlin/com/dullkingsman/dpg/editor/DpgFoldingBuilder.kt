package com.dullkingsman.dpg.editor

import com.dullkingsman.dpg.lang.DpgElementTypes.DOLLAR_QUOTE_BODY
import com.dullkingsman.dpg.lang.DpgElementTypes.PART2_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.SCHEMA_BLOCK
import com.dullkingsman.dpg.lang.DpgTokenTypes.BLOCK_COMMENT
import com.dullkingsman.dpg.lang.DpgTokenTypes.LBRACE
import com.dullkingsman.dpg.lang.DpgTokenTypes.RBRACE
import com.intellij.lang.ASTNode
import com.intellij.lang.folding.FoldingBuilderEx
import com.intellij.lang.folding.FoldingDescriptor
import com.intellij.openapi.editor.Document
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement

class DpgFoldingBuilder : FoldingBuilderEx(), DumbAware {

    override fun buildFoldRegions(
        root: PsiElement,
        document: Document,
        quick: Boolean
    ): Array<FoldingDescriptor> {
        val descriptors = mutableListOf<FoldingDescriptor>()
        collectRegions(root.node, descriptors)
        return descriptors.toTypedArray()
    }

    private fun collectRegions(node: ASTNode, result: MutableList<FoldingDescriptor>) {
        when (node.elementType) {

            // Part-2 { … } blocks (covers both object blocks and schema bodies)
            PART2_BLOCK -> foldBraces(node, result)

            // Dollar-quoted function bodies: fold the whole body node
            DOLLAR_QUOTE_BODY -> {
                val range = node.textRange
                if (range.length > 4) result += FoldingDescriptor(node, range)
            }

            // Block comments
            BLOCK_COMMENT -> {
                val range = node.textRange
                if (range.length > 6) result += FoldingDescriptor(node, range)
            }

            else -> {}
        }

        var child: ASTNode? = node.firstChildNode
        while (child != null) {
            collectRegions(child, result)
            child = child.treeNext
        }
    }

    private fun foldBraces(node: ASTNode, result: MutableList<FoldingDescriptor>) {
        val lbrace = node.findChildByType(LBRACE) ?: return
        val rbrace = node.findChildByType(RBRACE) ?: return
        val range = TextRange(lbrace.startOffset, rbrace.startOffset + 1)
        if (range.length > 3) result += FoldingDescriptor(node, range)
    }

    override fun getPlaceholderText(node: ASTNode): String? = when (node.elementType) {
        DOLLAR_QUOTE_BODY -> "$$...$$"
        BLOCK_COMMENT     -> "/*...*/"
        else              -> "{...}"
    }

    override fun isCollapsedByDefault(node: ASTNode): Boolean = false
}
