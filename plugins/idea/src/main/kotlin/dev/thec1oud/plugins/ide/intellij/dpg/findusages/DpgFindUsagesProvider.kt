package dev.thec1oud.plugins.ide.intellij.dpg.findusages

import dev.thec1oud.plugins.ide.intellij.dpg.lang.DpgLexer
import dev.thec1oud.plugins.ide.intellij.dpg.lang.DpgTokenTypes.COMMENTS
import dev.thec1oud.plugins.ide.intellij.dpg.lang.DpgTokenTypes.IDENTIFIER
import dev.thec1oud.plugins.ide.intellij.dpg.lang.DpgTokenTypes.STRINGS
import dev.thec1oud.plugins.ide.intellij.dpg.lang.psi.DpgObjectDeclaration
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
