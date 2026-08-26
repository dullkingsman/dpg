package dev.thec1oud.plugins.ide.dpg.reference

import dev.thec1oud.plugins.ide.dpg.lang.DpgElementTypes
import dev.thec1oud.plugins.ide.dpg.lang.DpgTokenTypes
import com.intellij.openapi.util.TextRange
import com.intellij.patterns.PlatformPatterns
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiReference
import com.intellij.psi.PsiReferenceContributor
import com.intellij.psi.PsiReferenceProvider
import com.intellij.psi.PsiReferenceRegistrar
import com.intellij.util.ProcessingContext

class DpgReferenceContributor : PsiReferenceContributor() {

    override fun registerReferenceProviders(registrar: PsiReferenceRegistrar) {
        // Macro spread: ...macro_name → MACRO macro_name declaration
        registrar.registerReferenceProvider(
            PlatformPatterns.psiElement(DpgTokenTypes.IDENTIFIER)
                .withParent(
                    PlatformPatterns.psiElement().withElementType(DpgElementTypes.SPREAD_EXPRESSION)
                ),
            object : PsiReferenceProvider() {
                override fun getReferencesByElement(
                    element: PsiElement,
                    context: ProcessingContext
                ): Array<PsiReference> = arrayOf(DpgReference(element, TextRange(0, element.textLength)))
            }
        )
    }
}
