package dev.thec1oud.plugins.ide.dpg

import com.intellij.lang.Language

object DpgLanguage : Language("DPG") {
    private fun readResolve(): Any = DpgLanguage
}
