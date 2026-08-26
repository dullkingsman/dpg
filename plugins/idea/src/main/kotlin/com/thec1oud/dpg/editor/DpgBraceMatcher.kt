package com.thec1oud.dpg.editor

import com.thec1oud.dpg.lang.DpgTokenTypes.LBRACE
import com.thec1oud.dpg.lang.DpgTokenTypes.LPAREN
import com.thec1oud.dpg.lang.DpgTokenTypes.RBRACE
import com.thec1oud.dpg.lang.DpgTokenTypes.RPAREN
import com.intellij.lang.BracePair
import com.intellij.lang.PairedBraceMatcher
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType

class DpgBraceMatcher : PairedBraceMatcher {

    private val PAIRS = arrayOf(
        BracePair(LBRACE, RBRACE, /* structural = */ true),
        BracePair(LPAREN, RPAREN, /* structural = */ false),
    )

    override fun getPairs(): Array<BracePair> = PAIRS

    override fun isPairedBracesAllowedBeforeType(
        lbraceType: IElementType,
        contextType: IElementType?
    ): Boolean = true

    override fun getCodeConstructStart(file: PsiFile, openingBraceOffset: Int): Int =
        openingBraceOffset
}
