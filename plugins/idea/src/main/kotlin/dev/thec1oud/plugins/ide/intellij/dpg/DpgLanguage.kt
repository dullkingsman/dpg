package dev.thec1oud.plugins.ide.intellij.dpg

import com.intellij.lang.Language

object DpgLanguage : Language("DPG") {
    private fun readResolve(): Any = DpgLanguage
}
