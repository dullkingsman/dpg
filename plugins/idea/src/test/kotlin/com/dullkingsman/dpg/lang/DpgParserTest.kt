package com.dullkingsman.dpg.lang

import com.dullkingsman.dpg.lang.DpgElementTypes.MACRO_DECLARATION
import com.dullkingsman.dpg.lang.DpgElementTypes.PART2_BLOCK
import com.dullkingsman.dpg.lang.DpgElementTypes.SCHEMA_BLOCK
import com.dullkingsman.dpg.lang.psi.DpgFile
import com.dullkingsman.dpg.lang.psi.DpgObjectDeclaration
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.testFramework.ParsingTestCase

class DpgParserTest : ParsingTestCase("", "dpg", DpgParserDefinition()) {

    override fun getTestDataPath(): String = "src/test/testData"

    // ── Root node type ────────────────────────────────────────────────────────

    fun testEmptyFile() {
        assertInstanceOf(parseFile("empty", ""), DpgFile::class.java)
    }

    fun testCommentsOnly() {
        assertNotNull(parseFile("comments", "-- line comment\n/* block comment */"))
    }

    // ── Table ─────────────────────────────────────────────────────────────────

    fun testSimpleTable() {
        val file = parseFile("table", "TABLE users (id BIGINT NOT NULL PRIMARY KEY);")
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals(1, decls!!.size)
        assertEquals("users", decls[0].name)
    }

    fun testTableWithPart2Block() {
        val file = parseFile("table_block", """
            TABLE accounts (
                id UUID NOT NULL PRIMARY KEY
            )
            {
                COMMENT 'Tenant store';
                ENABLE ROW LEVEL SECURITY;
            }
        """.trimIndent())
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals(1, decls!!.size)
        assertEquals("accounts", decls[0].name)
        assertNotNull(decls[0].node.findChildByType(PART2_BLOCK))
    }

    // ── Schema block ──────────────────────────────────────────────────────────

    fun testSchemaBlock() {
        val file = parseFile("schema", """
            SCHEMA public {
                TABLE users (id BIGINT);
            }
        """.trimIndent())
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        val schema = decls!!.firstOrNull { it.node.elementType == SCHEMA_BLOCK }
        assertNotNull(schema)
        assertEquals("public", schema!!.name)
    }

    // ── Macro declaration ─────────────────────────────────────────────────────

    fun testMacroDeclaration() {
        val file = parseFile("macro", """
            MACRO timestamps (
                created_at TIMESTAMPTZ NOT NULL DEFAULT now()
            )
        """.trimIndent())
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals(1, decls!!.size)
        assertEquals(MACRO_DECLARATION, decls[0].node.elementType)
    }

    // ── View ──────────────────────────────────────────────────────────────────

    fun testView() {
        val file = parseFile("view", "VIEW active_users AS SELECT id FROM users WHERE active;")
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals("active_users", decls!![0].name)
    }

    // ── Function ──────────────────────────────────────────────────────────────

    fun testFunction() {
        val src = """
            FUNCTION greet(name TEXT) RETURNS TEXT LANGUAGE sql STABLE
            AS ${'$'}${'$'}
                SELECT 'Hello, ' || name;
            ${'$'}${'$'};
            {
                COMMENT 'Greet a user';
            }
        """.trimIndent()
        val file = parseFile("function", src)
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals(1, decls!!.size)
        assertEquals("greet", decls[0].name)
        assertNotNull(decls[0].node.findChildByType(PART2_BLOCK))
    }

    // ── Enum ──────────────────────────────────────────────────────────────────

    fun testEnum() {
        val file = parseFile("enum", "ENUM status ('active', 'inactive', 'deleted');")
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals("status", decls!![0].name)
    }

    // ── Role ──────────────────────────────────────────────────────────────────

    fun testRole() {
        val file = parseFile("role", """
            ROLE app_service {
                LOGIN;
                PASSWORD 'env:APP_PW';
                CONNECTION LIMIT 20;
            }
        """.trimIndent())
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals("app_service", decls!![0].name)
    }

    // ── Multiple declarations ─────────────────────────────────────────────────

    fun testMultipleDeclarations() {
        val file = parseFile("multiple", """
            TABLE a (id BIGINT);
            TABLE b (id BIGINT);
            VIEW v AS SELECT 1;
        """.trimIndent())
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals(3, decls!!.size)
    }

    // ── Spread expression ─────────────────────────────────────────────────────

    fun testSpreadExpression() {
        val src = """
            MACRO timestamps (created_at TIMESTAMPTZ)
            TABLE t (id BIGINT, ...timestamps);
        """.trimIndent()
        assertNotNull(parseFile("spread", src))
    }

    // ── Extension ─────────────────────────────────────────────────────────────

    fun testExtension() {
        val file = parseFile("extension", "EXTENSION pgcrypto;")
        val decls = PsiTreeUtil.getChildrenOfType(file, DpgObjectDeclaration::class.java)
        assertNotNull(decls)
        assertEquals("pgcrypto", decls!![0].name)
    }
}
