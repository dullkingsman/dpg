package com.dullkingsman.dpg.lang.psi

import com.dullkingsman.dpg.DpgFileType
import com.dullkingsman.dpg.DpgLanguage
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.openapi.fileTypes.FileType
import com.intellij.psi.FileViewProvider

class DpgFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, DpgLanguage) {
    override fun getFileType(): FileType = DpgFileType.INSTANCE
    override fun toString(): String = "DPG File"
}
