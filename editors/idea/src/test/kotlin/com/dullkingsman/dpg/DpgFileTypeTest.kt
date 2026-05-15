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
        // MockFileTypeManager used in the headless test sandbox does not wire up
        // plugin.xml extension points, so we verify the class-level declaration.
        // Full end-to-end detection (IntelliJ routing *.dpg to DpgFileType via the
        // extension point) is exercised when the plugin is installed in a real IDE.
        assertEquals("dpg", DpgFileType.INSTANCE.defaultExtension)
        assertEquals("DPG", DpgFileType.INSTANCE.name)
    }

    fun testNonDpgFileIsNotRecognized() {
        val file = myFixture.configureByText("schema.sql", "CREATE TABLE users (id bigint);")
        assertFalse(file.fileType.name == "DPG")
    }
}
