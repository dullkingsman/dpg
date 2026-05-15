package com.dullkingsman.dpg

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile

// ---------------------------------------------------------------------------
// LSP server support — only activated when running in IntelliJ IDEA Ultimate
// 2023.2+ which ships the com.intellij.platform.lsp bundled plugin.
//
// plugin.xml registers DpgLspServerSupportProvider under an optional <depends>
// on com.intellij.platform.lsp, so IntelliJ silently skips the extension in
// Community Edition where that plugin is absent.
//
// We compile against Community Edition only, so the LSP framework interfaces
// (LspServerSupportProvider, LspServerStarter, ProjectWideLspServerDescriptor)
// are NOT on the compile-time classpath. DpgLspServerSupportProvider
// implements the contract via reflection; DpgLspServerDescriptor is a plain
// class whose instances are passed to the framework at runtime.
// ---------------------------------------------------------------------------

/**
 * Registered as <platform.lsp.serverSupportProvider> in dpg-lsp-optional.xml.
 * IntelliJ instantiates this via the extension point only when the lsp plugin
 * is present (Ultimate 2023.2+).
 *
 * The method signature matches LspServerSupportProvider.fileOpened exactly so
 * IntelliJ can invoke it reflectively through the extension-point mechanism.
 */
@Suppress("unused")
class DpgLspServerSupportProvider {
    @Suppress("UNUSED_PARAMETER")
    fun fileOpened(project: Project, file: VirtualFile, serverStarter: Any) {
        if (file.extension != "dpg") return
        // serverStarter is LspServerSupportProvider.LspServerStarter at runtime.
        val descriptorClass = Class.forName("com.intellij.platform.lsp.api.LspServerDescriptor")
        val method = serverStarter.javaClass.getMethod("ensureServerStarted", descriptorClass)
        method.invoke(serverStarter, DpgLspServerDescriptor(project))
    }
}

/**
 * Describes the dpg-lsp subprocess to IntelliJ's LSP client.
 *
 * At runtime in Ultimate the LSP framework calls isSupportedFile and
 * createCommandLine via reflection. The class does NOT extend
 * ProjectWideLspServerDescriptor (Ultimate-only) so it compiles against
 * Community Edition, but it satisfies the expected contract at runtime.
 */
@Suppress("unused")
class DpgLspServerDescriptor(private val project: Project) {

    val presentableName: String = "DPG Language Server"

    fun isSupportedFile(file: VirtualFile): Boolean = file.extension == "dpg"

    fun createCommandLine(): GeneralCommandLine =
        GeneralCommandLine("dpg-lsp", "--stdio")

    fun getProject(): Project = project
}
