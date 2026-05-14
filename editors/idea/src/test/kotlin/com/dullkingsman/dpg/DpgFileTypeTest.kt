package com.dullkingsman.dpg

import com.intellij.testFramework.fixtures.BasePlatformTestCase

class DpgFileTypeTest : BasePlatformTestCase() {

    fun testName() {
        assertEquals("DPG", DpgFileType.INSTANCE.name)
    }

    fun testDefaultExtension() {
        assertEquals("dpg", DpgFileType.INSTANCE.defaultExtension)
    }

    fun testDescription() {
        assertNotNull(DpgFileType.INSTANCE.description)
        assertTrue(DpgFileType.INSTANCE.description.isNotBlank())
    }

    fun testLanguage() {
        assertSame(DpgLanguage, DpgFileType.INSTANCE.language)
    }

    fun testIcon() {
        assertNotNull(DpgFileType.INSTANCE.icon)
    }

    fun testSingleton() {
        assertSame(DpgFileType.INSTANCE, DpgFileType.INSTANCE)
    }

    fun testDpgFileIsRecognized() {
        val file = myFixture.configureByText("schema.dpg", "TABLE users (id bigint);")
        assertEquals("DPG", file.fileType.name)
    }

    fun testNonDpgFileIsNotRecognized() {
        val file = myFixture.configureByText("schema.sql", "CREATE TABLE users (id bigint);")
        assertFalse(file.fileType.name == "DPG")
    }
}
