package com.dullkingsman.dpg.lang

import com.dullkingsman.dpg.lang.DpgElementTypes.FILE
import com.dullkingsman.dpg.lang.DpgElementTypes.MACRO_DECLARATION
import com.dullkingsman.dpg.lang.DpgElementTypes.OBJECT_DECLARATION
import com.dullkingsman.dpg.lang.DpgElementTypes.SCHEMA_BLOCK
import com.dullkingsman.dpg.lang.DpgTokenTypes.COMMENTS
import com.dullkingsman.dpg.lang.DpgTokenTypes.STRINGS
import com.dullkingsman.dpg.lang.psi.DpgFile
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.dullkingsman.dpg.lang.psi.DpgPsiElement
import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiParser
import com.intellij.lexer.Lexer
import com.intellij.openapi.project.Project
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IFileElementType
import com.intellij.psi.tree.TokenSet

class DpgParserDefinition : ParserDefinition {

    override fun createLexer(project: Project): Lexer = DpgLexer()

    override fun createParser(project: Project): PsiParser = DpgParser()

    override fun getFileNodeType(): IFileElementType = FILE

    override fun getCommentTokens(): TokenSet = COMMENTS

    override fun getStringLiteralElements(): TokenSet = STRINGS

    override fun createElement(node: ASTNode): PsiElement = when (node.elementType) {
        OBJECT_DECLARATION, SCHEMA_BLOCK, MACRO_DECLARATION -> DpgObjectDeclaration(node)
        else -> DpgPsiElement(node)
    }

    override fun createFile(viewProvider: FileViewProvider): PsiFile = DpgFile(viewProvider)
}
