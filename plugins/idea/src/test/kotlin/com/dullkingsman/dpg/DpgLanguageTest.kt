package com.dullkingsman.dpg

import com.intellij.testFramework.fixtures.BasePlatformTestCase

class DpgLanguageTest : BasePlatformTestCase() {

    fun testLanguageId() {
        assertEquals("DPG", DpgLanguage.id)
    }

    fun testSingleton() {
        assertSame(DpgLanguage, DpgLanguage)
    }

    fun testDisplayName() {
        assertNotNull(DpgLanguage.displayName)
    }
}
