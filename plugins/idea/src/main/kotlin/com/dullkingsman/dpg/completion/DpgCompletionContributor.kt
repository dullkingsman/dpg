package com.dullkingsman.dpg.completion

import com.dullkingsman.dpg.DpgLanguage
import com.dullkingsman.dpg.lang.DpgElementTypes.NESTED_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.PART2_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.SCHEMA_BLOCK
import com.intellij.codeInsight.completion.CompletionContributor
import com.intellij.codeInsight.completion.CompletionParameters
import com.intellij.codeInsight.completion.CompletionProvider
import com.intellij.codeInsight.completion.CompletionResultSet
import com.intellij.codeInsight.completion.CompletionType
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.patterns.PsiElementPattern
import com.intellij.psi.PsiElement
import com.intellij.util.ProcessingContext

class DpgCompletionContributor : CompletionContributor() {

    init {
        // Object type keywords at file top-level and inside schema bodies
        extend(CompletionType.BASIC,
            inDpgFile().andNot(insideObjectBlock()),
            TopLevelKeywordsProvider)

        // Block directive keywords inside object/role/function { } blocks
        extend(CompletionType.BASIC, insideObjectBlock(), BlockDirectiveProvider)
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
