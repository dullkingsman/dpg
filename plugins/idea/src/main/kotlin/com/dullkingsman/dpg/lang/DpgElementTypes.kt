package com.dullkingsman.dpg.lang

import com.dullkingsman.dpg.DpgLanguage
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.IFileElementType

class DpgElementType(debugName: String) : IElementType(debugName, DpgLanguage)

object DpgElementTypes {
    @JvmField val FILE = IFileElementType(DpgLanguage)

    // ── Top-level declarations ─────────────────────────────────────────────
    @JvmField val OBJECT_DECLARATION = DpgElementType("OBJECT_DECLARATION")
    @JvmField val MACRO_DECLARATION  = DpgElementType("MACRO_DECLARATION")

    // ── Schema block (SCHEMA name { ... }) ────────────────────────────────
    @JvmField val SCHEMA_BLOCK       = DpgElementType("SCHEMA_BLOCK")

    // ── Declaration parts ─────────────────────────────────────────────────
    @JvmField val OBJECT_KEYWORD_SEQ = DpgElementType("OBJECT_KEYWORD_SEQ")
    @JvmField val QUALIFIED_NAME     = DpgElementType("QUALIFIED_NAME")
    @JvmField val PART1_BODY         = DpgElementType("PART1_BODY")
    @JvmField val PAREN_BODY         = DpgElementType("PAREN_BODY")
    @JvmField val DOLLAR_QUOTE_BODY  = DpgElementType("DOLLAR_QUOTE_BODY")
    @JvmField val PART2_BLOCK        = DpgElementType("PART2_BLOCK")

    // ── Block internals ───────────────────────────────────────────────────
    @JvmField val BLOCK_DIRECTIVE    = DpgElementType("BLOCK_DIRECTIVE")
    @JvmField val NESTED_BLOCK       = DpgElementType("NESTED_BLOCK")

    // ── Macro internals ───────────────────────────────────────────────────
    @JvmField val MACRO_PAREN_BODY   = DpgElementType("MACRO_PAREN_BODY")
    @JvmField val MACRO_BRACE_BODY   = DpgElementType("MACRO_BRACE_BODY")
    @JvmField val SPREAD_EXPRESSION  = DpgElementType("SPREAD_EXPRESSION")
}
