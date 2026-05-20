package com.dullkingsman.dpg.findusages

import com.dullkingsman.dpg.lang.DpgLexer
import com.dullkingsman.dpg.lang.DpgTokenTypes.COMMENTS
import com.dullkingsman.dpg.lang.DpgTokenTypes.IDENTIFIER
import com.dullkingsman.dpg.lang.DpgTokenTypes.STRINGS
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.intellij.lang.cacheBuilder.DefaultWordsScanner
import com.intellij.lang.cacheBuilder.WordsScanner
import com.intellij.lang.findUsages.FindUsagesProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNamedElement
import com.intellij.psi.tree.TokenSet

class DpgFindUsagesProvider : FindUsagesProvider {

    override fun getWordsScanner(): WordsScanner = DefaultWordsScanner(
        DpgLexer(),
        /* identifiers */ TokenSet.create(IDENTIFIER),
        /* comments    */ COMMENTS,
        /* literals    */ STRINGS
    )

    override fun canFindUsagesFor(element: PsiElement): Boolean =
        element is DpgObjectDeclaration && element.name != null

    override fun getHelpId(element: PsiElement): String? = null

    override fun getType(element: PsiElement): String = when (element) {
        is DpgObjectDeclaration -> element.getObjectKindText().lowercase().ifBlank { "object" }
        else -> "element"
    }

    override fun getDescriptiveName(element: PsiElement): String =
        (element as? PsiNamedElement)?.name ?: element.text ?: "<unnamed>"

    override fun getNodeText(element: PsiElement, useFullName: Boolean): String =
        getDescriptiveName(element)
}
