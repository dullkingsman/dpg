package com.dullkingsman.dpg.highlight

import com.dullkingsman.dpg.lang.DpgLexer
import com.dullkingsman.dpg.lang.DpgTokenTypes as T
import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors as Default
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.colors.TextAttributesKey.createTextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.psi.tree.IElementType

object DpgHighlightingColors {
    @JvmField val OBJECT_KEYWORD   = createTextAttributesKey("DPG_OBJECT_KEYWORD",   Default.KEYWORD)
    @JvmField val BLOCK_KEYWORD    = createTextAttributesKey("DPG_BLOCK_KEYWORD",    Default.INSTANCE_METHOD)
    @JvmField val SQL_KEYWORD      = createTextAttributesKey("DPG_SQL_KEYWORD",      Default.KEYWORD)
    @JvmField val MACRO_KEYWORD    = createTextAttributesKey("DPG_MACRO_KEYWORD",    Default.METADATA)
    @JvmField val FORBIDDEN_VERB   = createTextAttributesKey("DPG_FORBIDDEN_VERB",   Default.INVALID_STRING_ESCAPE)
    @JvmField val SPREAD_OPERATOR  = createTextAttributesKey("DPG_SPREAD_OPERATOR",  Default.OPERATION_SIGN)
    @JvmField val STRING           = createTextAttributesKey("DPG_STRING",           Default.STRING)
    @JvmField val DOLLAR_QUOTE_KY  = createTextAttributesKey("DPG_DOLLAR_QUOTE",     Default.STRING)
    @JvmField val DOLLAR_BODY      = createTextAttributesKey("DPG_DOLLAR_BODY",      Default.STRING)
    @JvmField val NUMBER           = createTextAttributesKey("DPG_NUMBER",           Default.NUMBER)
    @JvmField val LINE_COMMENT_KY  = createTextAttributesKey("DPG_LINE_COMMENT",     Default.LINE_COMMENT)
    @JvmField val BLOCK_COMMENT_KY = createTextAttributesKey("DPG_BLOCK_COMMENT",    Default.BLOCK_COMMENT)
    @JvmField val IDENTIFIER_KY    = createTextAttributesKey("DPG_IDENTIFIER",       Default.IDENTIFIER)
    @JvmField val BRACES           = createTextAttributesKey("DPG_BRACES",           Default.BRACES)
    @JvmField val PARENS           = createTextAttributesKey("DPG_PARENS",           Default.PARENTHESES)
    @JvmField val COMMA_KY         = createTextAttributesKey("DPG_COMMA",            Default.COMMA)
    @JvmField val DOT_KY           = createTextAttributesKey("DPG_DOT",              Default.DOT)
    @JvmField val SEMICOLON_KY     = createTextAttributesKey("DPG_SEMICOLON",        Default.SEMICOLON)
    @JvmField val OPERATOR_KY      = createTextAttributesKey("DPG_OPERATOR",         Default.OPERATION_SIGN)
}

class DpgSyntaxHighlighter : SyntaxHighlighterBase() {

    override fun getHighlightingLexer(): Lexer = DpgLexer()

    override fun getTokenHighlights(token: IElementType): Array<TextAttributesKey> =
        pack(colorOf(token))

    private fun colorOf(tt: IElementType): TextAttributesKey? = when {
        // Comments
        tt == T.LINE_COMMENT   -> DpgHighlightingColors.LINE_COMMENT_KY
        tt == T.BLOCK_COMMENT  -> DpgHighlightingColors.BLOCK_COMMENT_KY

        // Literals
        tt == T.STRING_LITERAL       -> DpgHighlightingColors.STRING
        tt == T.DOLLAR_QUOTE         -> DpgHighlightingColors.DOLLAR_QUOTE_KY
        tt == T.DOLLAR_QUOTE_CONTENT -> DpgHighlightingColors.DOLLAR_BODY
        tt == T.INTEGER              -> DpgHighlightingColors.NUMBER

        // Identifier
        tt == T.IDENTIFIER -> DpgHighlightingColors.IDENTIFIER_KY

        // Punctuation
        tt == T.LBRACE || tt == T.RBRACE   -> DpgHighlightingColors.BRACES
        tt == T.LPAREN || tt == T.RPAREN   -> DpgHighlightingColors.PARENS
        tt == T.COMMA                      -> DpgHighlightingColors.COMMA_KY
        tt == T.DOT                        -> DpgHighlightingColors.DOT_KY
        tt == T.SEMICOLON                  -> DpgHighlightingColors.SEMICOLON_KY
        tt == T.OPERATOR || tt == T.COLON_COLON ||
            tt == T.COLON || tt == T.EQ || tt == T.STAR -> DpgHighlightingColors.OPERATOR_KY

        // Spread
        tt == T.SPREAD -> DpgHighlightingColors.SPREAD_OPERATOR

        // Forbidden verbs
        tt == T.FORBIDDEN_VERB -> DpgHighlightingColors.FORBIDDEN_VERB

        // Macro keyword
        tt == T.MACRO_KW -> DpgHighlightingColors.MACRO_KEYWORD

        // DPG object-type keywords
        T.DPG_OBJECT_KEYWORDS.contains(tt) -> DpgHighlightingColors.OBJECT_KEYWORD

        // Block directive keywords
        T.BLOCK_DIRECTIVE_KEYWORDS.contains(tt) -> DpgHighlightingColors.BLOCK_KEYWORD

        // SQL keywords
        tt == T.SQL_KW -> DpgHighlightingColors.SQL_KEYWORD

        else -> null
    }
}
