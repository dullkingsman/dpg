package com.thec1oud.dpg

import com.thec1oud.dpg.lang.DpgParserDefinition
import com.intellij.testFramework.fixtures.BasePlatformTestCase

class DpgLanguageTest : BasePlatformTestCase() {

    fun testLanguageId() = assertEquals("DPG", DpgLanguage.id)

    fun testSingleton() = assertSame(DpgLanguage, DpgLanguage)

    fun testDisplayName() = assertNotNull(DpgLanguage.displayName)

    fun testParserDefinitionCreatesLexer() {
        val def = DpgParserDefinition()
        assertNotNull(def.createLexer(project))
    }

    fun testParserDefinitionCreatesParser() {
        val def = DpgParserDefinition()
        assertNotNull(def.createParser(project))
    }

    fun testCommentTokens() {
        val def = DpgParserDefinition()
        val comments = def.commentTokens
        assertTrue(comments.contains(com.thec1oud.dpg.lang.DpgTokenTypes.LINE_COMMENT))
        assertTrue(comments.contains(com.thec1oud.dpg.lang.DpgTokenTypes.BLOCK_COMMENT))
    }
}
