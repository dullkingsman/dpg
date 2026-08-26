package dev.thec1oud.plugins.ide.intellij.dpg.lang.psi

import dev.thec1oud.plugins.ide.intellij.dpg.DpgFileType
import dev.thec1oud.plugins.ide.intellij.dpg.DpgLanguage
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.openapi.fileTypes.FileType
import com.intellij.psi.FileViewProvider

class DpgFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, DpgLanguage) {
    override fun getFileType(): FileType = DpgFileType.INSTANCE
    override fun toString(): String = "DPG File"
}
