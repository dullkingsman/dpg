package com.dullkingsman.dpg.lang.psi

import com.dullkingsman.dpg.lang.DpgElementTypes
import com.dullkingsman.dpg.lang.DpgTokenTypes
import com.intellij.lang.ASTNode
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.impl.source.tree.LeafElement
import com.intellij.util.IncorrectOperationException

/**
 * PSI node for a DPG object declaration (TABLE, VIEW, FUNCTION, SCHEMA, …).
 * The declared name is the first IDENTIFIER token inside the QUALIFIED_NAME child.
 */
class DpgObjectDeclaration(node: ASTNode) : DpgPsiElement(node), PsiNameIdentifierOwner {

    override fun getNameIdentifier(): PsiElement? {
        val qname = node.findChildByType(DpgElementTypes.QUALIFIED_NAME) ?: return null
        return qname.findChildByType(DpgTokenTypes.IDENTIFIER)?.psi
    }

    override fun getName(): String? = nameIdentifier?.text

    @Throws(IncorrectOperationException::class)
    override fun setName(name: String): PsiElement {
        val ident = nameIdentifier ?: throw IncorrectOperationException("No name identifier")
        val leaf = ident.node as? LeafElement
            ?: throw IncorrectOperationException("Name identifier is not a leaf")
        leaf.replaceWithText(name)
        return this
    }

    /** Human-readable object kind prefix, e.g. "TABLE", "SCHEMA public". */
    fun getObjectKindText(): String =
        node.findChildByType(DpgElementTypes.OBJECT_KEYWORD_SEQ)?.text?.trim() ?: ""

    override fun toString(): String = "DpgObjectDeclaration(${name ?: "<unnamed>"})"
}
