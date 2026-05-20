package com.dullkingsman.dpg.formatter

import com.intellij.formatting.FormattingContext
import com.intellij.formatting.FormattingModel
import com.intellij.formatting.FormattingModelBuilder
import com.intellij.formatting.FormattingModelProvider
import com.intellij.formatting.Indent

class DpgFormattingModelBuilder : FormattingModelBuilder {
    override fun createModel(formattingContext: FormattingContext): FormattingModel {
        val psiFile = formattingContext.psiElement.containingFile
        val settings = formattingContext.codeStyleSettings
        val rootBlock = DpgBlock(psiFile.node, null, null, Indent.getNoneIndent(), settings)
        return FormattingModelProvider.createFormattingModelForPsiFile(psiFile, rootBlock, settings)
    }
}
