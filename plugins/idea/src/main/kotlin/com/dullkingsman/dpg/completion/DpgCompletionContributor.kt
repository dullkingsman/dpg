package com.dullkingsman.dpg.completion

import com.dullkingsman.dpg.DpgLanguage
import com.dullkingsman.dpg.lang.DpgElementTypes.MACRO_DECLARATION
import com.dullkingsman.dpg.lang.DpgElementTypes.NESTED_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.OBJECT_KEYWORD_SEQ
import com.dullkingsman.dpg.lang.DpgElementTypes.PART1_BODY
import com.dullkingsman.dpg.lang.DpgElementTypes.PART2_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.QUALIFIED_NAME
import com.dullkingsman.dpg.lang.DpgElementTypes.SCHEMA_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.SPREAD_EXPRESSION
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.intellij.codeInsight.completion.CompletionContributor
import com.intellij.codeInsight.completion.CompletionParameters
import com.intellij.codeInsight.completion.CompletionProvider
import com.intellij.codeInsight.completion.CompletionResultSet
import com.intellij.codeInsight.completion.CompletionType
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.patterns.PsiElementPattern
import com.intellij.psi.PsiElement
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.util.ProcessingContext

class DpgCompletionContributor : CompletionContributor() {

    init {
        // Macro spread: only macro names after ...
        extend(CompletionType.BASIC, insideSpreadExpression(), MacroSpreadProvider)

        // Object type keywords at file top-level and inside schema bodies,
        // but not inside a spread expression
        extend(CompletionType.BASIC,
            inDpgFile().andNot(insideObjectBlock()).andNot(insideSpreadExpression()),
            TopLevelKeywordsProvider)

        // Block directive keywords inside object/role/function { } blocks,
        // but not inside a spread expression
        extend(CompletionType.BASIC,
            insideObjectBlock().andNot(insideSpreadExpression()),
            BlockDirectiveProvider)

        // Block directive keywords directly inside a schema { } body
        // (OWNER, COMMENT, DEPRECATED, GRANTS, etc.) — the schema's PART2_BLOCK is
        // intentionally excluded from insideObjectBlock() so this needs its own registration.
        // Exclude insideObjectContent() so directives don't bleed into column definitions,
        // object names, or keyword sequences of objects nested within the schema.
        extend(CompletionType.BASIC,
            insideSchemaBody().andNot(insideObjectContent()).andNot(insideSpreadExpression()),
            BlockDirectiveProvider)
    }

    private fun inDpgFile(): PsiElementPattern.Capture<PsiElement> =
        PlatformPatterns.psiElement().withLanguage(DpgLanguage)

    /**
     * Matches positions inside a PART2_BLOCK or NESTED_BLOCK that belongs to an
     * object declaration (table, role, function, …) — NOT the PART2_BLOCK whose
     * direct parent is a SCHEMA_BLOCK (i.e. a schema body).
     *
     * We check the direct parent rather than using a transitive `.inside()` chain
     * so that TABLE/FUNCTION blocks nested inside a SCHEMA still offer block
     * directive completions (their PART2_BLOCK's direct parent is OBJECT_DECLARATION,
     * not SCHEMA_BLOCK).
     */
    private fun insideObjectBlock(): PsiElementPattern.Capture<PsiElement> =
        PlatformPatterns.psiElement().withLanguage(DpgLanguage)
            .inside(
                PlatformPatterns.psiElement()
                    .withElementType(PlatformPatterns.elementType().oneOf(PART2_BLOCK, NESTED_BLOCK))
                    .withParent(
                        PlatformPatterns.psiElement()
                            .andNot(PlatformPatterns.psiElement().withElementType(SCHEMA_BLOCK))
                    )
            )

    /** Matches positions anywhere inside the schema's own PART2_BLOCK. */
    private fun insideSchemaBody(): PsiElementPattern.Capture<PsiElement> =
        PlatformPatterns.psiElement().withLanguage(DpgLanguage)
            .inside(
                PlatformPatterns.psiElement()
                    .withElementType(PART2_BLOCK)
                    .withParent(
                        PlatformPatterns.psiElement().withElementType(SCHEMA_BLOCK)
                    )
            )

    /** Matches any position inside a SPREAD_EXPRESSION node (after `...`). */
    private fun insideSpreadExpression(): PsiElementPattern.Capture<PsiElement> =
        PlatformPatterns.psiElement().withLanguage(DpgLanguage)
            .inside(PlatformPatterns.psiElement().withElementType(SPREAD_EXPRESSION))

    /**
     * Matches positions inside the non-block parts of an object declaration:
     * PART1_BODY (column/param/body text — also covers any PAREN_BODY children inside it),
     * OBJECT_KEYWORD_SEQ (the leading keyword tokens), or QUALIFIED_NAME (the
     * declared name). Used to suppress schema-body directive completions from
     * bleeding into these positions when an object is nested inside a schema block.
     */
    private fun insideObjectContent(): PsiElementPattern.Capture<PsiElement> =
        PlatformPatterns.psiElement().withLanguage(DpgLanguage)
            .inside(
                PlatformPatterns.psiElement().withElementType(
                    PlatformPatterns.elementType()
                        .oneOf(PART1_BODY, OBJECT_KEYWORD_SEQ, QUALIFIED_NAME)
                )
            )
}

private object TopLevelKeywordsProvider : CompletionProvider<CompletionParameters>() {
    private val KEYWORDS = listOf(
        "SCHEMA", "TABLE", "UNLOGGED TABLE", "FOREIGN TABLE",
        "VIEW", "MATERIALIZED VIEW", "RECURSIVE VIEW",
        "FUNCTION", "PROCEDURE", "AGGREGATE",
        "ENUM", "TYPE", "DOMAIN",
        "SEQUENCE", "ROLE", "TABLESPACE", "EXTENSION",
        "PUBLICATION", "SUBSCRIPTION",
        "FOREIGN DATA WRAPPER", "SERVER", "USER MAPPING",
        "EVENT TRIGGER", "COLLATION", "OPERATOR",
        "OPERATOR CLASS", "OPERATOR FAMILY", "CAST", "STATISTICS",
        "TEXT SEARCH CONFIGURATION", "TEXT SEARCH DICTIONARY",
        "TEXT SEARCH PARSER", "TEXT SEARCH TEMPLATE",
        "DEFAULT PRIVILEGES", "VIRTUAL TYPE", "MACRO"
    )

    override fun addCompletions(
        parameters: CompletionParameters,
        context: ProcessingContext,
        result: CompletionResultSet
    ) {
        KEYWORDS.forEach { kw ->
            result.addElement(
                LookupElementBuilder.create(kw).withBoldness(true).withTypeText("DPG object")
            )
        }
    }
}

private object BlockDirectiveProvider : CompletionProvider<CompletionParameters>() {
    private val DIRECTIVES = listOf(
        // Collection blocks
        "INDICES", "POLICIES", "TRIGGERS", "GRANTS", "REVOCATIONS",
        "PARTITIONS", "COLUMNS", "CONSTRAINTS",
        // Singular directives
        "INDEX", "POLICY", "TRIGGER", "GRANT", "REVOCATION",
        "PARTITION", "COLUMN", "CONSTRAINT",
        // Scalar directives
        "COMMENT", "OWNER", "PROTECTED", "DEPRECATED", "RENAMED FROM",
        "ENABLE ROW LEVEL SECURITY", "DISABLE ROW LEVEL SECURITY",
        "FORCE ROW LEVEL SECURITY", "NOFORCE ROW LEVEL SECURITY",
        "DROP CASCADE", "STATISTICS",
        // Role attributes
        "LOGIN", "NOLOGIN", "SUPERUSER", "NOSUPERUSER",
        "CREATEDB", "NOCREATEDB", "CREATEROLE", "NOCREATEROLE",
        "INHERIT", "NOINHERIT", "REPLICATION", "NOREPLICATION",
        "BYPASSRLS", "NOBYPASSRLS",
        "PASSWORD", "CONNECTION LIMIT", "VALID UNTIL", "IN ROLE",
        // Privilege keywords for GRANTS / REVOCATIONS
        "SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE",
        "REFERENCES", "USAGE", "EXECUTE", "CONNECT", "TEMPORARY",
        "ALL PRIVILEGES",
    )

    override fun addCompletions(
        parameters: CompletionParameters,
        context: ProcessingContext,
        result: CompletionResultSet
    ) {
        DIRECTIVES.forEach { kw ->
            result.addElement(
                LookupElementBuilder.create(kw).withTypeText("DPG directive")
            )
        }
    }
}

private object MacroSpreadProvider : CompletionProvider<CompletionParameters>() {
    override fun addCompletions(
        parameters: CompletionParameters,
        context: ProcessingContext,
        result: CompletionResultSet
    ) {
        PsiTreeUtil.findChildrenOfType(parameters.originalFile, DpgObjectDeclaration::class.java)
            .filter { it.node.elementType == MACRO_DECLARATION }
            .forEach { macro ->
                val name = macro.name ?: return@forEach
                result.addElement(
                    LookupElementBuilder.create(name)
                        .withBoldness(true)
                        .withTypeText("macro")
                )
            }
    }
}
