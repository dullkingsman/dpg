package com.dullkingsman.dpg.formatter

import com.dullkingsman.dpg.DpgFileType
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiDocumentManager
import com.intellij.psi.PsiFile
import com.intellij.psi.codeStyle.ExternalFormatProcessor
import java.io.File

/**
 * Delegates Reformat Code (Ctrl+Alt+L) to `dpg fmt --stdin` when the `dpg`
 * binary is on PATH.  Falls through to the built-in [DpgFormattingModelBuilder]
 * when dpg is not installed.
 */
class DpgExternalFormatter : ExternalFormatProcessor {

    override fun getId(): String = "dpg"

    override fun activeForFile(source: PsiFile): Boolean =
        source.fileType == DpgFileType.INSTANCE && dpgOnPath()

    override fun format(
        source: PsiFile,
        range: TextRange,
        canChangeWhiteSpacesOnly: Boolean,
        keepLineBreaks: Boolean,
        @Suppress("UNUSED_PARAMETER") b3: Boolean,
        @Suppress("UNUSED_PARAMETER") cursorOffset: Int,
    ): TextRange? {
        val document = PsiDocumentManager.getInstance(source.project).getDocument(source)
            ?: return null
        val original = document.text
        val formatted = runDpgFmtStdin(original) ?: return null
        if (formatted == original) return range
        document.replaceString(0, document.textLength, formatted)
        return TextRange(0, formatted.length)
    }

    override fun indent(source: PsiFile, lineStartOffset: Int): String? = null

    // ── Helpers ───────────────────────────────────────────────────────────────

    private fun runDpgFmtStdin(input: String): String? = try {
        val process = ProcessBuilder(dpgExecutable(), "fmt", "--stdin").start()
        val inputBytes = input.toByteArray(Charsets.UTF_8)
        // Write stdin in a background thread so stdout never blocks.
        val writer = Thread { process.outputStream.use { it.write(inputBytes) } }
        writer.isDaemon = true
        writer.start()
        val output = process.inputStream.readBytes().toString(Charsets.UTF_8)
        writer.join(5_000)
        val exited = process.waitFor(10, java.util.concurrent.TimeUnit.SECONDS)
        if (!exited) process.destroyForcibly()
        if (exited && process.exitValue() == 0) output else null
    } catch (_: Exception) {
        null
    }

    companion object {
        private val cachedExe: String? by lazy {
            val exe = if (System.getProperty("os.name", "").startsWith("Win")) "dpg.exe" else "dpg"
            (System.getenv("PATH") ?: "")
                .split(File.pathSeparatorChar)
                .map { File(it, exe) }
                .firstOrNull { it.isFile && it.canExecute() }
                ?.absolutePath
        }

        fun dpgOnPath(): Boolean = dpgExecutable() != null
        fun dpgExecutable(): String? = cachedExe
    }
}
