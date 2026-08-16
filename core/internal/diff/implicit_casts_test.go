package diff

import "testing"

// ── bareTypeName ─────────────────────────────────────────────────────────────

func TestBareTypeNamePlain(t *testing.T) {
	if got := bareTypeName("integer"); got != "integer" {
		t.Errorf("got %q", got)
	}
}

func TestBareTypeNameTrailingMod(t *testing.T) {
	if got := bareTypeName("character varying(255)"); got != "character varying" {
		t.Errorf("got %q", got)
	}
}

func TestBareTypeNameNumericPrecisionScale(t *testing.T) {
	if got := bareTypeName("numeric(10,2)"); got != "numeric" {
		t.Errorf("got %q", got)
	}
}

// TestBareTypeNameMidStringModifier proves the "with time zone" special case
// (TypeRef.String() inserts Mods BEFORE " with time zone", not at the end —
// "timestamp(3) with time zone", never "timestamp with time zone(3)") is
// correctly inverted, not just the common trailing-mod case.
func TestBareTypeNameMidStringModifier(t *testing.T) {
	if got := bareTypeName("timestamp(3) with time zone"); got != "timestamp with time zone" {
		t.Errorf("got %q", got)
	}
	if got := bareTypeName("time(2) with time zone"); got != "time with time zone" {
		t.Errorf("got %q", got)
	}
}

func TestBareTypeNameNoModWithTimeZone(t *testing.T) {
	if got := bareTypeName("timestamp with time zone"); got != "timestamp with time zone" {
		t.Errorf("got %q", got)
	}
}

func TestBareTypeNameArraySuffix(t *testing.T) {
	if got := bareTypeName("integer[]"); got != "integer" {
		t.Errorf("got %q", got)
	}
}

func TestBareTypeNameArrayWithMod(t *testing.T) {
	if got := bareTypeName("character varying(50)[]"); got != "character varying" {
		t.Errorf("got %q", got)
	}
}

func TestBareTypeNameMultiDimArray(t *testing.T) {
	if got := bareTypeName("integer[][]"); got != "integer" {
		t.Errorf("got %q", got)
	}
}

// ── hasImplicitCast ──────────────────────────────────────────────────────────

func TestHasImplicitCastKnownWidening(t *testing.T) {
	cases := []struct{ from, to string }{
		{"smallint", "integer"},
		{"integer", "bigint"},
		{"smallint", "bigint"},
		{"real", "double precision"},
		{"integer", "numeric"},
		{"character varying", "text"},
		{"character", "text"},
		{"date", "timestamp without time zone"}, // PGCatalogName now maps "timestamp" -> "timestamp without time zone" (fixed alongside this workstream — see ir/typeutil.go), matching format_type()'s own convention.
	}
	for _, c := range cases {
		if !hasImplicitCast(c.from, c.to) {
			t.Errorf("expected an implicit cast %s -> %s", c.from, c.to)
		}
	}
}

// TestHasImplicitCastDirectional proves the table is directional — a
// narrowing cast in the reverse direction of a known widening pair must NOT
// be treated as implicit, matching real PostgreSQL (bigint -> integer has no
// pg_cast entry at all, only integer -> bigint does).
func TestHasImplicitCastDirectional(t *testing.T) {
	if hasImplicitCast("bigint", "integer") {
		t.Error("bigint -> integer must not be implicit (narrowing, no pg_cast entry)")
	}
	if hasImplicitCast("double precision", "real") {
		t.Error("double precision -> real must not be implicit (narrowing, no pg_cast entry)")
	}
}

func TestHasImplicitCastUnrelatedTypesFalse(t *testing.T) {
	if hasImplicitCast("text", "integer") {
		t.Error("text -> integer must not be implicit (needs an explicit cast, can fail at runtime)")
	}
	if hasImplicitCast("boolean", "integer") {
		t.Error("boolean -> integer must not be implicit")
	}
}

// TestHasImplicitCastRealTypeStrings proves the table's keys match the exact
// strings resolveColType/TypeRef.String() themselves produce — using
// PGCatalogName-aliased names (e.g. "integer" not "int4"), the same as a
// real desired/snapshot column type comparison in differ.go would see, not
// the raw pg_catalog internal names the table was extracted with.
func TestHasImplicitCastRealTypeStrings(t *testing.T) {
	if !hasImplicitCast("integer", "bigint") {
		t.Error("expected the PGCatalogName-aliased form \"integer\"/\"bigint\" to be found, not just raw \"int4\"/\"int8\"")
	}
	if hasImplicitCast("int4", "int8") {
		t.Error("raw internal names (\"int4\"/\"int8\") should NOT be found — the table is keyed on the aliased form only, matching what the differ actually compares")
	}
}

func TestHasImplicitCastWithModifiersStripped(t *testing.T) {
	if !hasImplicitCast("character varying(50)", "text") {
		t.Error("expected typmod to be stripped before the lookup")
	}
	if !hasImplicitCast("smallint", "numeric(10,2)") {
		t.Error("expected the target's typmod to be stripped before the lookup")
	}
}

// TestHasImplicitCastArrayDerivesFromElementType proves array-to-array
// widening is correctly treated as implicit when the ELEMENT types have an
// implicit cast between them — confirmed live against a real PostgreSQL 17
// container (ALTER COLUMN a TYPE bigint[] with no USING succeeded on a real
// integer[] column), matching PostgreSQL's own array-coercion behavior
// (parse_coerce.c derives an array-to-array cast's applicable context
// directly from the corresponding element cast's context, not from a
// separate per-array-type pg_cast entry — pg_cast has no array OIDs in it
// at all). bareTypeName strips array brackets before the lookup specifically
// to get this right, not to sidestep arrays entirely.
func TestHasImplicitCastArrayDerivesFromElementType(t *testing.T) {
	if !hasImplicitCast("integer[]", "bigint[]") {
		t.Error("integer[] -> bigint[] must be implicit (element types integer -> bigint are implicit) — confirmed live")
	}
}

// TestHasImplicitCastArrayNonImplicitElementStaysDestructive is the array
// negative case, also confirmed live (text[] -> integer[] fails without an
// explicit USING against a real PostgreSQL container, matching the scalar
// text -> integer case having no implicit cast either).
func TestHasImplicitCastArrayNonImplicitElementStaysDestructive(t *testing.T) {
	if hasImplicitCast("text[]", "integer[]") {
		t.Error("text[] -> integer[] must not be implicit (element types text -> integer are not implicit) — confirmed live")
	}
}
