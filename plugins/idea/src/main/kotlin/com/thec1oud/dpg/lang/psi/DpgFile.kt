package com.thec1oud.dpg.lang.psi

import com.thec1oud.dpg.DpgFileType
import com.thec1oud.dpg.DpgLanguage
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.openapi.fileTypes.FileType
import com.intellij.psi.FileViewProvider

class DpgFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, DpgLanguage) {
    override fun getFileType(): FileType = DpgFileType.INSTANCE
    override fun toString(): String = "DPG File"
}
