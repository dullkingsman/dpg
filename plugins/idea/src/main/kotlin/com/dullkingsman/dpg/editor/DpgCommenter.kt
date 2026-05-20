package com.dullkingsman.dpg.editor

import com.intellij.lang.CodeDocumentationAwareCommenter
import com.intellij.psi.PsiComment
import com.intellij.psi.tree.IElementType
import com.dullkingsman.dpg.lang.DpgTokenTypes

class DpgCommenter : CodeDocumentationAwareCommenter {
    override fun getLineCommentPrefix(): String                = "-- "
    override fun getBlockCommentPrefix(): String               = "/* "
    override fun getBlockCommentSuffix(): String               = " */"
    override fun getCommentedBlockCommentPrefix(): String?     = null
    override fun getCommentedBlockCommentSuffix(): String?     = null
    override fun getDocumentationCommentPrefix(): String?      = null
    override fun getDocumentationCommentLinePrefix(): String?  = null
    override fun getDocumentationCommentSuffix(): String?      = null
    override fun isDocumentationComment(element: PsiComment?): Boolean = false
    override fun getDocumentationCommentTokenType(): IElementType? = null
    override fun getLineCommentTokenType(): IElementType = DpgTokenTypes.LINE_COMMENT
    override fun getBlockCommentTokenType(): IElementType = DpgTokenTypes.BLOCK_COMMENT
}
