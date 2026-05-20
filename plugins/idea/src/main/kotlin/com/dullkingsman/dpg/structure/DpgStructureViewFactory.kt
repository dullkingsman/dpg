package com.dullkingsman.dpg.structure

import com.dullkingsman.dpg.lang.psi.DpgFile
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.intellij.ide.structureView.StructureViewBuilder
import com.intellij.ide.structureView.StructureViewModel
import com.intellij.ide.structureView.StructureViewModelBase
import com.intellij.ide.structureView.TreeBasedStructureViewBuilder
import com.intellij.lang.PsiStructureViewFactory
import com.intellij.openapi.editor.Editor
import com.intellij.psi.PsiFile

class DpgStructureViewFactory : PsiStructureViewFactory {

    override fun getStructureViewBuilder(psiFile: PsiFile): StructureViewBuilder? {
        val dpgFile = psiFile as? DpgFile ?: return null
        return object : TreeBasedStructureViewBuilder() {
            override fun createStructureViewModel(editor: Editor?): StructureViewModel =
                DpgStructureViewModel(dpgFile, editor)

            override fun isRootNodeShown(): Boolean = false
        }
    }
}

private class DpgStructureViewModel(file: DpgFile, editor: Editor?) :
    StructureViewModelBase(file, editor, DpgStructureViewElement(file)) {

    override fun getSuitableClasses(): Array<Class<*>> =
        arrayOf(DpgObjectDeclaration::class.java)
}
