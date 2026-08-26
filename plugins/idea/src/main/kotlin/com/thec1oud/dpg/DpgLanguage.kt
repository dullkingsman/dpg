package com.thec1oud.dpg

import com.intellij.lang.Language

object DpgLanguage : Language("DPG") {
    private fun readResolve(): Any = DpgLanguage
}
