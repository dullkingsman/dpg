package diff

import "testing"

// ── splitTypeNameAndMods ─────────────────────────────────────────────────────

func TestSplitTypeNameAndModsPlain(t *testing.T) {
	base, mods := splitTypeNameAndMods("integer")
	if base != "integer" || mods != "" {
		t.Errorf("got base=%q mods=%q", base, mods)
	}
}

func TestSplitTypeNameAndModsTrailing(t *testing.T) {
	base, mods := splitTypeNameAndMods("character varying(255)")
	if base != "character varying" || mods != "(255)" {
		t.Errorf("got base=%q mods=%q", base, mods)
	}
}

func TestSplitTypeNameAndModsMidString(t *testing.T) {
	base, mods := splitTypeNameAndMods("timestamp(3) with time zone")
	if base != "timestamp with time zone" || mods != "(3)" {
		t.Errorf("got base=%q mods=%q", base, mods)
	}
}

// ── typmodWideningSafe: character varying / bit varying (length) ──────────────

func TestTypmodWideningVarcharWiden(t *testing.T) {
	if !typmodWideningSafe("character varying(10)", "character varying(20)") {
		t.Error("expected varchar(10) -> varchar(20) to be a safe widening")
	}
}

func TestTypmodWideningVarcharShrinkUnsafe(t *testing.T) {
	if typmodWideningSafe("character varying(20)", "character varying(10)") {
		t.Error("expected varchar(20) -> varchar(10) to NOT be safe (can truncate)")
	}
}

func TestTypmodWideningVarcharToUnboundedSafe(t *testing.T) {
	if !typmodWideningSafe("character varying(10)", "character varying") {
		t.Error("expected varchar(10) -> unbounded varchar to be safe")
	}
}

func TestTypmodWideningVarcharFromUnboundedUnsafe(t *testing.T) {
	if typmodWideningSafe("character varying", "character varying(10)") {
		t.Error("expected unbounded varchar -> varchar(10) to NOT be safe (can truncate existing data)")
	}
}

func TestTypmodWideningBitVaryingWiden(t *testing.T) {
	if !typmodWideningSafe("bit varying(4)", "bit varying(8)") {
		t.Error("expected bit varying(4) -> bit varying(8) to be a safe widening")
	}
}

// ── typmodWideningSafe: character (fixed, auto-pads) ───────────────────────────

func TestTypmodWideningCharacterWiden(t *testing.T) {
	if !typmodWideningSafe("character(5)", "character(10)") {
		t.Error("expected character(5) -> character(10) to be safe (confirmed live: auto-pads with spaces)")
	}
}

func TestTypmodWideningCharacterShrinkUnsafe(t *testing.T) {
	if typmodWideningSafe("character(10)", "character(5)") {
		t.Error("expected character(10) -> character(5) to NOT be safe")
	}
}

// TestTypmodWideningCharacterBareModsUnsafe proves bare "character" (no
// length) is NOT treated as "unbounded" the way character varying is —
// PostgreSQL's own bare-word default for CHARACTER is character(1), a
// fixed narrow width, confirmed live, not an unbounded form.
func TestTypmodWideningCharacterBareModsUnsafe(t *testing.T) {
	if typmodWideningSafe("character(5)", "character") {
		t.Error("expected character(5) -> bare character to NOT be treated as safe unbounded widening")
	}
}

// ── typmodWideningSafe: bit (fixed, no auto-pad at all) ────────────────────────

// TestTypmodWideningBitNeverSafe proves fixed-length bit(n) is never a safe
// widening in either direction — confirmed live that even WIDENING errors
// outright ("bit string length 4 does not match type bit(8)"), unlike every
// other length-modifier type in this file.
func TestTypmodWideningBitNeverSafe(t *testing.T) {
	if typmodWideningSafe("bit(4)", "bit(8)") {
		t.Error("expected bit(4) -> bit(8) to NOT be safe (confirmed live: PostgreSQL errors on this, no auto-pad)")
	}
	if typmodWideningSafe("bit(8)", "bit(4)") {
		t.Error("expected bit(8) -> bit(4) to NOT be safe either")
	}
}

// ── typmodWideningSafe: numeric ────────────────────────────────────────────────

func TestTypmodWideningNumericPrecisionOnlyWiden(t *testing.T) {
	if !typmodWideningSafe("numeric(5,2)", "numeric(10,2)") {
		t.Error("expected numeric(5,2) -> numeric(10,2) to be safe (precision widened, scale unchanged)")
	}
}

func TestTypmodWideningNumericScaleWiden(t *testing.T) {
	if !typmodWideningSafe("numeric(10,2)", "numeric(10,4)") {
		t.Error("expected numeric(10,2) -> numeric(10,4) to be safe (confirmed live: pads trailing zeros, same value)")
	}
}

// TestTypmodWideningNumericScaleShrinkUnsafe proves the genuinely
// surprising case: PostgreSQL does NOT error on a scale-only shrink, it
// SILENTLY ROUNDS the stored value (123.4500 -> 123.5, confirmed live) —
// real data loss with no error to catch it, so this must stay unsafe even
// though precision alone doesn't decrease.
func TestTypmodWideningNumericScaleShrinkUnsafe(t *testing.T) {
	if typmodWideningSafe("numeric(10,4)", "numeric(10,1)") {
		t.Error("expected numeric(10,4) -> numeric(10,1) to NOT be safe (confirmed live: silently rounds, no error)")
	}
}

func TestTypmodWideningNumericToUnboundedSafe(t *testing.T) {
	if !typmodWideningSafe("numeric(10,2)", "numeric") {
		t.Error("expected numeric(10,2) -> unbounded numeric to be safe")
	}
}

func TestTypmodWideningNumericFromUnboundedUnsafe(t *testing.T) {
	if typmodWideningSafe("numeric", "numeric(5,2)") {
		t.Error("expected unbounded numeric -> numeric(5,2) to NOT be safe (confirmed live: can overflow and error)")
	}
}

// TestTypmodWideningNumericPrecisionOnlyModTreatedAsScaleZero proves a
// single-int numeric mod ("numeric(5)") is normalized to scale 0 before
// comparing, matching PostgreSQL's own "numeric(p)" == "numeric(p,0)"
// convention.
func TestTypmodWideningNumericPrecisionOnlyModTreatedAsScaleZero(t *testing.T) {
	if !typmodWideningSafe("numeric(5)", "numeric(10,0)") {
		t.Error("expected numeric(5) [== numeric(5,0)] -> numeric(10,0) to be safe")
	}
	if !typmodWideningSafe("numeric(5)", "numeric(10,2)") {
		t.Error("expected numeric(5) [== numeric(5,0)] -> numeric(10,2) to be safe (scale 0 -> 2 widens too)")
	}
}

// ── typmodWideningSafe: time/timestamp/interval precision ─────────────────────

func TestTypmodWideningTimestampPrecisionWiden(t *testing.T) {
	if !typmodWideningSafe("timestamp without time zone(2)", "timestamp without time zone(6)") {
		t.Error("expected timestamp(2) -> timestamp(6) to be safe")
	}
}

func TestTypmodWideningTimestampPrecisionShrinkUnsafe(t *testing.T) {
	if typmodWideningSafe("timestamp without time zone(6)", "timestamp without time zone(0)") {
		t.Error("expected timestamp(6) -> timestamp(0) to NOT be safe (confirmed live: silently truncates fractional seconds)")
	}
}

// TestTypmodWideningTimestampBareIsMaxPrecisionSafe proves bare (no mods)
// timestamp is PostgreSQL's own maximum precision (6) — confirmed live a
// bare timestamp column stores full microsecond precision — so widening TO
// bare is always safe, and FROM bare (effectively 6) to any lower explicit
// precision is a real shrink.
func TestTypmodWideningTimestampBareIsMaxPrecisionSafe(t *testing.T) {
	if !typmodWideningSafe("timestamp without time zone(2)", "timestamp without time zone") {
		t.Error("expected timestamp(2) -> bare timestamp to be safe (bare == max precision)")
	}
	if typmodWideningSafe("timestamp without time zone", "timestamp without time zone(2)") {
		t.Error("expected bare timestamp -> timestamp(2) to NOT be safe (confirmed live: truncates existing sub-precision data)")
	}
}

func TestTypmodWideningTimeWithTimeZonePrecisionWiden(t *testing.T) {
	if !typmodWideningSafe("time with time zone(1)", "time with time zone(4)") {
		t.Error("expected time with time zone(1) -> time with time zone(4) to be safe")
	}
}

func TestTypmodWideningIntervalPrecisionWiden(t *testing.T) {
	if !typmodWideningSafe("interval(2)", "interval(6)") {
		t.Error("expected interval(2) -> interval(6) to be safe")
	}
}

// ── typmodWideningSafe: cross-cutting guards ───────────────────────────────────

func TestTypmodWideningDifferentBaseTypesFalse(t *testing.T) {
	if typmodWideningSafe("character varying(10)", "text") {
		t.Error("different base types must never be handled here — that's hasImplicitCast's job")
	}
}

// TestTypmodWideningArraysExcluded proves arrays are deliberately excluded
// from this mechanism (unverified live for the typmod case, unlike
// hasImplicitCast's separately-confirmed array/element-type handling).
func TestTypmodWideningArraysExcluded(t *testing.T) {
	if typmodWideningSafe("character varying(10)[]", "character varying(20)[]") {
		t.Error("expected array typmod widening to stay conservative (unverified live), even though the scalar case is safe")
	}
}

func TestTypmodWideningUnrelatedBaseTypeFalse(t *testing.T) {
	if typmodWideningSafe("boolean", "boolean") {
		t.Error("a base type with no typmod-widening rule at all must return false, not panic or false-positive")
	}
}

// ── parseModInts ────────────────────────────────────────────────────────────

func TestParseModIntsSingle(t *testing.T) {
	got, ok := parseModInts("(10)")
	if !ok || len(got) != 1 || got[0] != 10 {
		t.Errorf("got %v, ok=%v", got, ok)
	}
}

func TestParseModIntsPair(t *testing.T) {
	got, ok := parseModInts("(10,2)")
	if !ok || len(got) != 2 || got[0] != 10 || got[1] != 2 {
		t.Errorf("got %v, ok=%v", got, ok)
	}
}

func TestParseModIntsEmptyNotOK(t *testing.T) {
	if _, ok := parseModInts(""); ok {
		t.Error("expected ok=false for an empty mods string")
	}
}

func TestParseModIntsMalformedNotOK(t *testing.T) {
	if _, ok := parseModInts("(abc)"); ok {
		t.Error("expected ok=false for a non-numeric mods string")
	}
}
