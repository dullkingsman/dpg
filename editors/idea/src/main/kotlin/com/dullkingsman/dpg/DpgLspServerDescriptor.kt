package com.dullkingsman.dpg

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerDescriptor
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.LspServerSupportProvider.LspServerStarter
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor

class DpgLspServerSupportProvider : LspServerSupportProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        serverStarter: LspServerStarter,
    ) {
        if (file.extension == "dpg") {
            serverStarter.ensureServerStarted(DpgLspServerDescriptor(project))
        }
    }
}

class DpgLspServerDescriptor(project: Project) :
    ProjectWideLspServerDescriptor(project, "DPG Language Server") {

    override fun isSupportedFile(file: VirtualFile): Boolean =
        file.extension == "dpg"

    override fun createCommandLine(): GeneralCommandLine =
        GeneralCommandLine("dpg-lsp", "--stdio")
}
