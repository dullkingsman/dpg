package com.dullkingsman.dpg.structure

import com.dullkingsman.dpg.DpgIcons
import com.dullkingsman.dpg.lang.DpgElementTypes.MACRO_DECLARATION
import com.dullkingsman.dpg.lang.DpgElementTypes.PART2_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.SCHEMA_BLOCK
import com.dullkingsman.dpg.lang.psi.DpgFile
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.intellij.icons.AllIcons
import com.intellij.ide.structureView.StructureViewTreeElement
import com.intellij.ide.util.treeView.smartTree.SortableTreeElement
import com.intellij.ide.util.treeView.smartTree.TreeElement
import com.intellij.navigation.ItemPresentation
import com.intellij.psi.NavigatablePsiElement
import com.intellij.psi.PsiElement
import javax.swing.Icon

class DpgStructureViewElement(private val element: PsiElement) :
    StructureViewTreeElement, SortableTreeElement {

    override fun getValue(): Any = element

    override fun navigate(requestFocus: Boolean) {
        (element as? NavigatablePsiElement)?.navigate(requestFocus)
    }

    override fun canNavigate(): Boolean =
        (element as? NavigatablePsiElement)?.canNavigate() == true

    override fun canNavigateToSource(): Boolean =
        (element as? NavigatablePsiElement)?.canNavigateToSource() == true

    override fun getAlphaSortKey(): String = presentation.presentableText ?: ""

    override fun getPresentation(): ItemPresentation = object : ItemPresentation {
        override fun getPresentableText(): String? = when (element) {
            is DpgFile              -> element.name
            is DpgObjectDeclaration -> buildLabel(element)
            else                    -> element.text?.take(40)
        }

        override fun getLocationString(): String? = null

        override fun getIcon(unused: Boolean): Icon = objectIcon(element)
    }

    private fun objectIcon(el: PsiElement): Icon = when {
        el is DpgFile -> DpgIcons.FILE
        el is DpgObjectDeclaration -> when (el.node.elementType) {
            SCHEMA_BLOCK      -> AllIcons.Nodes.Module
            MACRO_DECLARATION -> AllIcons.Nodes.Template
            else -> when (el.getObjectKindText().trim().uppercase().split(" ").first()) {
                "FUNCTION", "PROCEDURE", "AGGREGATE" -> AllIcons.Nodes.Function
                "VIEW", "MATERIALIZED", "RECURSIVE"  -> AllIcons.Nodes.Interface
                "TABLE", "UNLOGGED", "FOREIGN"       -> AllIcons.Nodes.Class
                "MACRO"                              -> AllIcons.Nodes.Template
                else                                 -> DpgIcons.FILE
            }
        }
        else -> DpgIcons.FILE
    }

    private fun buildLabel(decl: DpgObjectDeclaration): String {
        val kind = decl.getObjectKindText()
        val name = decl.name ?: return kind
        return if (kind.isNotBlank()) "$kind $name" else name
    }

    override fun getChildren(): Array<TreeElement> = when {
        element is DpgFile -> collectTopLevel(element)
        element is DpgObjectDeclaration &&
            element.node.elementType == SCHEMA_BLOCK -> collectDeclarationsIn(element)
        else -> emptyArray()
    }

    private fun collectTopLevel(file: DpgFile): Array<TreeElement> {
        val result = mutableListOf<TreeElement>()
        var child = file.firstChild
        while (child != null) {
            if (child is DpgObjectDeclaration) result += DpgStructureViewElement(child)
            child = child.nextSibling
        }
        return result.toTypedArray()
    }

    private fun collectDeclarationsIn(schema: DpgObjectDeclaration): Array<TreeElement> {
        val part2 = schema.node.findChildByType(PART2_BLOCK) ?: return emptyArray()
        val result = mutableListOf<TreeElement>()
        var child = part2.firstChildNode
        while (child != null) {
            val psi = child.psi
            if (psi is DpgObjectDeclaration) result += DpgStructureViewElement(psi)
            child = child.treeNext
        }
        return result.toTypedArray()
    }
}
