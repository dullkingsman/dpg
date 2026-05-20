package com.dullkingsman.dpg.reference

import com.dullkingsman.dpg.lang.DpgElementTypes
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiReferenceBase
import com.intellij.psi.util.PsiTreeUtil

class DpgReference(element: PsiElement, range: TextRange) : PsiReferenceBase<PsiElement>(element, range) {

    override fun resolve(): PsiElement? {
        val name = value
        return macrosInFile().firstOrNull { it.name == name }
    }

    override fun getVariants(): Array<Any> =
        macrosInFile().mapNotNull { it.name }.toTypedArray()

    private fun macrosInFile() =
        PsiTreeUtil.findChildrenOfType(element.containingFile, DpgObjectDeclaration::class.java)
            .filter { it.node.elementType == DpgElementTypes.MACRO_DECLARATION }
}
