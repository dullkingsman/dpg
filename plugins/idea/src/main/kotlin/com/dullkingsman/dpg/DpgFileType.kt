package com.dullkingsman.dpg

import com.intellij.openapi.fileTypes.LanguageFileType
import javax.swing.Icon

class DpgFileType private constructor() : LanguageFileType(DpgLanguage) {

    override fun getName(): String        = "DPG"
    override fun getDescription(): String = "DPG (Declarative PG) source file"
    override fun getDefaultExtension(): String = "dpg"
    override fun getIcon(): Icon          = DpgIcons.FILE

    companion object {
        @JvmField
        val INSTANCE = DpgFileType()
    }
}
