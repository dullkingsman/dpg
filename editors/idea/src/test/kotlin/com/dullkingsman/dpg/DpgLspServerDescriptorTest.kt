package com.dullkingsman.dpg

import com.intellij.testFramework.fixtures.BasePlatformTestCase
import com.intellij.testFramework.fixtures.IdeaTestFixtureFactory

class DpgLspServerDescriptorTest : BasePlatformTestCase() {

    private lateinit var descriptor: DpgLspServerDescriptor

    override fun setUp() {
        super.setUp()
        descriptor = DpgLspServerDescriptor(project)
    }

    fun testIsSupportedFileForDpg() {
        val file = myFixture.configureByText("schema.dpg", "TABLE t (id bigint);").virtualFile
        assertTrue(descriptor.isSupportedFile(file))
    }

    fun testIsSupportedFileRejectsSql() {
        val file = myFixture.configureByText("schema.sql", "CREATE TABLE t (id bigint);").virtualFile
        assertFalse(descriptor.isSupportedFile(file))
    }

    fun testIsSupportedFileRejectsTxt() {
        val file = myFixture.configureByText("notes.txt", "some text").virtualFile
        assertFalse(descriptor.isSupportedFile(file))
    }

    fun testCommandLineUsesDpgLsp() {
        val cmd = descriptor.createCommandLine()
        assertEquals("dpg-lsp", cmd.exePath)
    }

    fun testCommandLineIncludesStdioFlag() {
        val cmd = descriptor.createCommandLine()
        assertTrue("--stdio" in cmd.parametersList.list)
    }

    fun testPresentationTextIsNotBlank() {
        assertTrue(descriptor.presentationText.isNotBlank())
    }
}
