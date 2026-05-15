package com.dullkingsman.dpg

import com.intellij.lang.Language

object DpgLanguage : Language("DPG") {
    private fun readResolve(): Any = DpgLanguage
}
