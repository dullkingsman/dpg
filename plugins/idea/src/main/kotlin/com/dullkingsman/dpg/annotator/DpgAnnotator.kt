package com.dullkingsman.dpg.annotator

import com.dullkingsman.dpg.lang.DpgElementTypes.NESTED_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.PART2_BLOCK
import com.dullkingsman.dpg.lang.DpgTokenTypes.DROP_KW
import com.dullkingsman.dpg.lang.DpgTokenTypes.FORBIDDEN_VERB
import com.dullkingsman.dpg.lang.DpgTokenTypes.MACRO_KW
import com.dullkingsman.dpg.lang.psi.DpgFile
import com.intellij.lang.annotation.AnnotationHolder
import com.intellij.lang.annotation.Annotator
import com.intellij.lang.annotation.HighlightSeverity
import com.intellij.psi.PsiElement

/**
 * Semantic annotations layered on top of lexer-based highlighting.
 *
 * DPG-E006  forbidden_verb    — CREATE, ALTER, or DROP at declaration level
 * DPG-E007  macro_inside_block — MACRO keyword inside a { } block
 */
class DpgAnnotator : Annotator {

    override fun annotate(element: PsiElement, holder: AnnotationHolder) {
        val tt = element.node?.elementType ?: return
        when (tt) {
            FORBIDDEN_VERB -> annotateForbiddenVerb(element, holder)
            // DROP is a valid block directive (DROP CASCADE) but forbidden at declaration level
            DROP_KW        -> if (!isInsideBlock(element)) annotateForbiddenVerb(element, holder)
            MACRO_KW       -> annotateMacroKeyword(element, holder)
        }
    }

    // ── DPG-E006 ─────────────────────────────────────────────────────────────

    private fun annotateForbiddenVerb(element: PsiElement, holder: AnnotationHolder) {
        val verb = element.text.uppercase()
        holder.newAnnotation(
            HighlightSeverity.ERROR,
            "DPG-E006: '$verb' is forbidden in .dpg source files at the declaration level."
        ).range(element).create()
    }

    // ── DPG-E007 ─────────────────────────────────────────────────────────────

    private fun annotateMacroKeyword(element: PsiElement, holder: AnnotationHolder) {
        if (isInsideBlock(element)) {
            holder.newAnnotation(
                HighlightSeverity.ERROR,
                "DPG-E007: MACRO declarations must appear at the top level of a .dpg file, " +
                "not inside a { } block."
            ).range(element).create()
        }
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private fun isInsideBlock(element: PsiElement): Boolean {
        var p: PsiElement? = element.parent
        while (p != null && p !is DpgFile) {
            val et = p.node?.elementType
            if (et == PART2_BLOCK || et == NESTED_BLOCK) return true
            p = p.parent
        }
        return false
    }
}
