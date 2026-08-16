package diff

import (
	"strconv"
	"strings"
)

// typmodWideningSafe reports whether a same-base-type column type change
// from "from" to "to" (both fully-rendered TypeRef.String() text) is a pure
// modifier (length/precision/scale) widening PostgreSQL can apply with no
// data loss and no USING clause — RFC §7.2's own primary example for the
// "implicit cast -> CAUTION" rule, VARCHAR(10) -> VARCHAR(20). This is a
// genuinely different mechanism from hasImplicitCast/implicit_casts.go:
// pg_cast has no entry for a type to itself (a same-type "cast" is trivial
// and never stored), so this needs type-specific typmod comparison instead
// of a lookup table. Every rule below was verified live against a real
// PostgreSQL 17 container, not assumed by analogy — several turned out
// genuinely surprising (see the per-family comments).
//
// Deliberately excludes arrays: the widening rules below were only
// confirmed live for the scalar case. hasImplicitCast's array handling
// was separately verified (element-type derivation), but typmod-widening
// on an array element (e.g. varchar(10)[] -> varchar(20)[]) has not been,
// so this stays conservative (DESTRUCTIVE) for arrays rather than assume
// the same derivation applies here too.
func typmodWideningSafe(from, to string) bool {
	if strings.HasSuffix(from, "[]") || strings.HasSuffix(to, "[]") {
		return false
	}
	fromBase, fromMods := splitTypeNameAndMods(from)
	toBase, toMods := splitTypeNameAndMods(to)
	if fromBase != toBase {
		return false
	}
	switch fromBase {
	case "character varying", "bit varying":
		// Confirmed live: widening the length is always safe (no rewrite
		// risk — existing values already fit within the old, smaller
		// bound); shrinking can fail at apply time if existing data is too
		// long, so it stays DESTRUCTIVE (verified live: ALTER COLUMN a TYPE
		// varchar(3) errored "value too long for type character
		// varying(3)"). Dropping the length entirely (toMods == "") is
		// always safe — it can only ever be a widening, since PostgreSQL's
		// unbounded form accepts everything a bounded one could.
		return lengthWideningSafe(fromMods, toMods)
	case "character":
		// Same length-widening rule as character varying — confirmed live
		// that widening character(5) -> character(10) succeeds and
		// re-pads the stored value (octet_length went from 5 to 10, not
		// just a display artifact). Bare "character" with no length is
		// deliberately NOT treated as toMods=="" == unlimited the way
		// character varying is: PostgreSQL's own bare-word default for
		// CHARACTER is character(1), a fixed narrow width, not "unbounded"
		// — so an empty fromMods/toMods here just falls through
		// lengthWideningSafe's own explicit-lengths-only requirement
		// (empty-empty already means "no change" and never reaches this
		// function; empty-on-one-side-only is conservatively unsafe).
		if fromMods == "" || toMods == "" {
			return false
		}
		return lengthWideningSafe(fromMods, toMods)
	case "bit":
		// Confirmed live, and genuinely surprising by analogy with
		// character/bit varying: fixed-length bit(n) has NO auto-pad or
		// auto-truncate coercion at all — ALTER COLUMN a TYPE bit(8) on a
		// real bit(4) column errored outright ("bit string length 4 does
		// not match type bit(8)"), even though it's strictly widening.
		// Never safe, in either direction, without an explicit USING.
		return false
	case "numeric":
		// Confirmed live: widening precision alone is a no-op value-wise;
		// widening scale pads trailing zeros (123.45 -> 123.4500, the same
		// mathematical value); dropping the bound entirely (toMods=="")
		// is always safe for the same "unbounded accepts everything
		// bounded did" reason as character varying. Shrinking scale is the
		// one genuinely surprising case: PostgreSQL does NOT error, it
		// SILENTLY ROUNDS the stored value (123.4500 -> 123.5 on a
		// scale-only shrink, confirmed live) — real, irreversible data
		// loss with no error to catch it, so any precision OR scale
		// decrease stays DESTRUCTIVE. Going from unbounded to bounded
		// (fromMods=="", toMods!="") is a shrink from DPG's perspective
		// (can't know at plan time whether live data fits the new bound —
		// confirmed live that it genuinely can overflow and error) and
		// deliberately excluded by requiring both sides present below.
		return numericWideningSafe(fromMods, toMods)
	case "time without time zone", "time with time zone",
		"timestamp without time zone", "timestamp with time zone", "interval":
		// Confirmed live: widening fractional-seconds precision is a
		// no-op (already-rounded digits aren't recovered, but nothing
		// further is lost); shrinking silently truncates the existing
		// value with no error (timestamp(6) -> timestamp(0) dropped
		// ".123456" to nothing, confirmed live) — same silent-data-loss
		// shape as numeric scale-shrinking, stays DESTRUCTIVE. Bare (no
		// mods) is PostgreSQL's own maximum precision (6) — confirmed
		// live a bare timestamp column stores full microsecond precision
		// — so toMods=="" is always safe (same "target is the least
		// restrictive form" reasoning as the other families), and
		// fromMods=="" with toMods!="" is a real shrink (verified: going
		// from bare (effectively precision 6) down to any explicit
		// precision < 6 truncates existing sub-precision data).
		return precisionWideningSafe(fromMods, toMods)
	default:
		return false
	}
}

// splitTypeNameAndMods is bareTypeName's fuller sibling: instead of just
// discarding the parenthesized modifier, it returns it too. Shares exactly
// the same position-finding logic (see bareTypeName's doc comment for why
// a modifier isn't always at the end — "with/without time zone" types
// insert theirs mid-string).
func splitTypeNameAndMods(rendered string) (base, mods string) {
	s := rendered
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return s, ""
	}
	closeRel := strings.IndexByte(s[open:], ')')
	if closeRel < 0 {
		return s, ""
	}
	closeAbs := open + closeRel
	return s[:open] + s[closeAbs+1:], s[open : closeAbs+1]
}

// parseModInts parses a "(a)" or "(a,b)" modifier string into its integer
// components, stripping the surrounding parens. Returns ok=false for an
// empty string or anything that doesn't parse cleanly — callers treat that
// as "can't prove this is safe," never as "assume safe."
func parseModInts(mods string) ([]int, bool) {
	if len(mods) < 2 || mods[0] != '(' || mods[len(mods)-1] != ')' {
		return nil, false
	}
	parts := strings.Split(mods[1:len(mods)-1], ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// lengthWideningSafe handles the single-integer-length family (character
// varying, bit varying): safe when both sides parse and to >= from, or when
// to is dropped entirely (unbounded is always at least as permissive as any
// bounded form). from=="" with to!="" (shrinking from unbounded) is
// deliberately NOT treated as safe here — callers reaching this function
// already know both are non-empty except via the toMods=="" branch, which
// is checked first.
func lengthWideningSafe(fromMods, toMods string) bool {
	if toMods == "" {
		return true
	}
	if fromMods == "" {
		return false
	}
	fromN, ok := parseModInts(fromMods)
	if !ok || len(fromN) != 1 {
		return false
	}
	toN, ok := parseModInts(toMods)
	if !ok || len(toN) != 1 {
		return false
	}
	return toN[0] >= fromN[0]
}

// numericWideningSafe: safe when to is dropped entirely (unbounded), or
// when both sides parse as (precision,scale) pairs with toPrecision >=
// fromPrecision AND toScale >= fromScale. A precision-only mod (single
// int, no scale — PostgreSQL treats "numeric(5)" as "numeric(5,0)") is
// normalized to an explicit scale of 0 before comparing.
func numericWideningSafe(fromMods, toMods string) bool {
	if toMods == "" {
		return true
	}
	if fromMods == "" {
		return false
	}
	fromN, ok := parseModInts(fromMods)
	if !ok || len(fromN) == 0 || len(fromN) > 2 {
		return false
	}
	toN, ok := parseModInts(toMods)
	if !ok || len(toN) == 0 || len(toN) > 2 {
		return false
	}
	fromPrecision, fromScale := fromN[0], 0
	if len(fromN) == 2 {
		fromScale = fromN[1]
	}
	toPrecision, toScale := toN[0], 0
	if len(toN) == 2 {
		toScale = toN[1]
	}
	return toPrecision >= fromPrecision && toScale >= fromScale
}

// precisionWideningSafe handles the single-integer-precision family (time/
// timestamp with or without time zone, interval): safe when to is dropped
// entirely (PostgreSQL's own bare-word maximum, precision 6), or when both
// sides parse and to >= from.
func precisionWideningSafe(fromMods, toMods string) bool {
	if toMods == "" {
		return true
	}
	if fromMods == "" {
		return false
	}
	fromN, ok := parseModInts(fromMods)
	if !ok || len(fromN) != 1 {
		return false
	}
	toN, ok := parseModInts(toMods)
	if !ok || len(toN) != 1 {
		return false
	}
	return toN[0] >= fromN[0]
}
