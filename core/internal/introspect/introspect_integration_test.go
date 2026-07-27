//go:build integration

package introspect_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/testpg"
)

func TestIntrospectTable(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx,
		`CREATE TABLE public.items (
			id    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			label text NOT NULL,
			qty   integer NOT NULL DEFAULT 0
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var found *ir.Table
	for _, obj := range objects {
		if tbl, ok := obj.(*ir.Table); ok && tbl.Name == "items" && tbl.Schema == "public" {
			found = tbl
			break
		}
	}
	if found == nil {
		t.Fatal("introspect: table public.items not found in results")
	}

	wantCols := map[string]string{"id": "bigint", "label": "text", "qty": "integer"}
	for _, col := range found.Columns {
		want, ok := wantCols[col.Name]
		if !ok {
			continue
		}
		if col.Type.String() != want {
			t.Errorf("column %s: type = %q, want %q", col.Name, col.Type.String(), want)
		}
		delete(wantCols, col.Name)
	}
	for name := range wantCols {
		t.Errorf("column %q not found in introspected table", name)
	}
}

// TestIntrospectColumnStorageIsTypeDefault is the live-catalog guard for a
// new field added during a dump false-negative fix (Owner/Storage were
// genuinely diffed but never rendered by dump): every real column has a
// concrete pg_attribute.attstorage value, so Storage itself can't tell
// "matches the type's own default" apart from "explicitly overridden" the
// way a nil pointer normally would — StorageIsTypeDefault (computed via a
// live join against pg_type.typstorage) is what dump uses to avoid adding a
// STORAGE line to every ordinary variable-length column.
func TestIntrospectColumnStorageIsTypeDefault(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// body: text, left at its type default (EXTENDED). id: integer, left at
	// its type default (PLAIN). overridden: text, explicitly set to EXTERNAL
	// (text's default is EXTENDED, so this is a genuine override).
	_, err = conn.Exec(ctx,
		`CREATE TABLE public.storage_t (
			id         integer,
			body       text,
			overridden text
		);
		ALTER TABLE public.storage_t ALTER COLUMN overridden SET STORAGE EXTERNAL;`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var found *ir.Table
	for _, obj := range objects {
		if tbl, ok := obj.(*ir.Table); ok && tbl.Name == "storage_t" && tbl.Schema == "public" {
			found = tbl
			break
		}
	}
	if found == nil {
		t.Fatal("introspect: table public.storage_t not found in results")
	}

	byName := map[string]*ir.Column{}
	for _, col := range found.Columns {
		byName[col.Name] = col
	}

	if col := byName["id"]; col == nil || col.Storage == nil || *col.Storage != "PLAIN" || !col.StorageIsTypeDefault {
		t.Errorf("id: got Storage=%v StorageIsTypeDefault=%v, want PLAIN/true", col.Storage, col != nil && col.StorageIsTypeDefault)
	}
	if col := byName["body"]; col == nil || col.Storage == nil || *col.Storage != "EXTENDED" || !col.StorageIsTypeDefault {
		t.Errorf("body: got Storage=%v StorageIsTypeDefault=%v, want EXTENDED/true", col.Storage, col != nil && col.StorageIsTypeDefault)
	}
	if col := byName["overridden"]; col == nil || col.Storage == nil || *col.Storage != "EXTERNAL" || col.StorageIsTypeDefault {
		t.Errorf("overridden: got Storage=%v StorageIsTypeDefault=%v, want EXTERNAL/false", col.Storage, col != nil && col.StorageIsTypeDefault)
	}
}

// TestIntrospectFunctionAndProcedureBody is the live regression guard for
// the dump function/procedure fix: pg_proc.prosrc was previously selected
// only to compute BodyHash, then discarded — dump had no raw body text to
// work with, so it could only emit a placeholder comment for a function
// (and had no case at all for procedures). Confirms the raw body now lands
// on Attrs.Body against a real PostgreSQL 17 catalog for both.
func TestIntrospectFunctionAndProcedureBody(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
CREATE FUNCTION public.add_ints(a integer, b integer) RETURNS integer
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    RETURN a + b;
END;
$$;

CREATE PROCEDURE public.recalc() LANGUAGE plpgsql AS $$
BEGIN
    NULL;
END;
$$;`)
	if err != nil {
		t.Fatalf("create function/procedure: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var fn *ir.Function
	var proc *ir.Procedure
	for _, obj := range objects {
		switch o := obj.(type) {
		case *ir.Function:
			if o.Name == "add_ints" {
				fn = o
			}
		case *ir.Procedure:
			if o.Name == "recalc" {
				proc = o
			}
		}
	}
	if fn == nil {
		t.Fatal("introspect: function public.add_ints not found")
	}
	if !strings.Contains(fn.Attrs.Body, "RETURN a + b") {
		t.Errorf("function Attrs.Body = %q, want it to contain the real function body", fn.Attrs.Body)
	}
	if proc == nil {
		t.Fatal("introspect: procedure public.recalc not found")
	}
	if !strings.Contains(proc.Attrs.Body, "NULL") {
		t.Errorf("procedure Attrs.Body = %q, want it to contain the real procedure body", proc.Attrs.Body)
	}
}

// TestIntrospectFunctionRowsSuppressesDefault is the live-catalog guard for
// ROWS suppression specifically: ROWS is only syntactically valid on a
// set-returning function ("RETURNS SETOF ..."), which DPG's own compiler
// can't yet author (ir.TypeRef has no SETOF representation — a separate,
// pre-existing gap). So this exercises introspection directly against a
// SETOF function created via raw SQL: one with an explicit ROWS override,
// one left at PostgreSQL's own default (1000 for a set-returning function),
// confirming Attrs.Rows is populated only when it genuinely differs from
// that live default — mirroring TestIntrospectColumnStorageIsTypeDefault's
// suppress-when-default pattern for COST/ROWS/PARALLEL.
func TestIntrospectFunctionRowsSuppressesDefault(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
CREATE FUNCTION public.gen_default(n integer) RETURNS SETOF integer
LANGUAGE sql AS $$
    SELECT generate_series(1, n);
$$;

CREATE FUNCTION public.gen_explicit(n integer) RETURNS SETOF integer
LANGUAGE sql ROWS 50 AS $$
    SELECT generate_series(1, n);
$$;`)
	if err != nil {
		t.Fatalf("create functions: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var byName = map[string]*ir.Function{}
	for _, obj := range objects {
		if fn, ok := obj.(*ir.Function); ok {
			byName[fn.Name] = fn
		}
	}

	def := byName["gen_default"]
	if def == nil {
		t.Fatal("introspect: function public.gen_default not found")
	}
	if def.Attrs.Rows != nil {
		t.Errorf("gen_default: Attrs.Rows = %v, want nil (matches PostgreSQL's own default of 1000 for a set-returning function)", *def.Attrs.Rows)
	}

	explicit := byName["gen_explicit"]
	if explicit == nil {
		t.Fatal("introspect: function public.gen_explicit not found")
	}
	if explicit.Attrs.Rows == nil || *explicit.Attrs.Rows != 50 {
		got := "nil"
		if explicit.Attrs.Rows != nil {
			got = fmt.Sprintf("%v", *explicit.Attrs.Rows)
		}
		t.Errorf("gen_explicit: Attrs.Rows = %s, want 50", got)
	}
}

// TestIntrospectFunctionArgModes is the live-catalog guard for the gap found
// while fixing RETURNS TABLE(...) introspection: oidvectortypes(proargtypes)
// — the source introspectFunctions used for Args — only ever reports
// IN/INOUT/VARIADIC argument TYPES with no mode or name at all, and OUT
// arguments are invisible to it entirely. Before this fix: a plain OUT-only
// function's OUT columns were completely missing from introspected Args
// (not just mis-rendered, the same severity as the RETURNS TABLE bug); an
// INOUT or VARIADIC function's mode keyword was silently lost even though
// its type was captured correctly. Covers all three plus a plain-IN control
// (confirmed to still take the original, unmodified introspection path).
func TestIntrospectFunctionArgModes(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
CREATE FUNCTION public.f_out(n integer, OUT a integer, OUT b text) LANGUAGE sql AS $$
    SELECT n, 'x';
$$;

CREATE FUNCTION public.f_inout(INOUT n integer) LANGUAGE sql AS $$
    SELECT n;
$$;

CREATE FUNCTION public.f_variadic(VARIADIC n integer[]) RETURNS integer LANGUAGE sql AS $$
    SELECT 1;
$$;

CREATE FUNCTION public.f_plain(n integer, m text) RETURNS integer LANGUAGE sql AS $$
    SELECT n;
$$;

CREATE FUNCTION public.f_unnamed_out(integer, OUT integer) LANGUAGE sql AS $$
    SELECT $1;
$$;`)
	if err != nil {
		t.Fatalf("create functions: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	byName := map[string]*ir.Function{}
	for _, obj := range objects {
		if fn, ok := obj.(*ir.Function); ok {
			byName[fn.Name] = fn
		}
	}

	fOut := byName["f_out"]
	if fOut == nil {
		t.Fatal("introspect: function public.f_out not found")
	}
	if len(fOut.Args) != 3 {
		t.Fatalf("f_out: got %d args, want 3 (n, a, b) — OUT columns must not be missing", len(fOut.Args))
	}
	if fOut.Args[1].Mode != "OUT" || fOut.Args[1].Name != "a" || fOut.Args[1].Type.Name != "integer" {
		t.Errorf("f_out.Args[1]: got %+v, want Mode=OUT Name=a Type=integer", fOut.Args[1])
	}
	if fOut.Args[2].Mode != "OUT" || fOut.Args[2].Name != "b" || fOut.Args[2].Type.Name != "text" {
		t.Errorf("f_out.Args[2]: got %+v, want Mode=OUT Name=b Type=text", fOut.Args[2])
	}

	fInout := byName["f_inout"]
	if fInout == nil {
		t.Fatal("introspect: function public.f_inout not found")
	}
	if len(fInout.Args) != 1 || fInout.Args[0].Mode != "INOUT" || fInout.Args[0].Name != "n" {
		t.Errorf("f_inout.Args: got %+v, want a single Mode=INOUT Name=n arg", fInout.Args)
	}

	fVariadic := byName["f_variadic"]
	if fVariadic == nil {
		t.Fatal("introspect: function public.f_variadic not found")
	}
	if len(fVariadic.Args) != 1 || fVariadic.Args[0].Mode != "VARIADIC" || fVariadic.Args[0].Name != "n" {
		t.Errorf("f_variadic.Args: got %+v, want a single Mode=VARIADIC Name=n arg", fVariadic.Args)
	}

	fPlain := byName["f_plain"]
	if fPlain == nil {
		t.Fatal("introspect: function public.f_plain not found")
	}
	if len(fPlain.Args) != 2 {
		t.Errorf("f_plain.Args: got %d, want 2 (unaffected by this fix, still uses the original oidvectortypes path)", len(fPlain.Args))
	}

	// An unnamed OUT parameter is a real, valid PostgreSQL construct (verified
	// live: proargnames comes back as an entirely NULL array in this case, not
	// per-position empty strings) — guards against a naive non-nullable scan
	// crashing introspection for any user function shaped this way.
	fUnnamedOut := byName["f_unnamed_out"]
	if fUnnamedOut == nil {
		t.Fatal("introspect: function public.f_unnamed_out not found")
	}
	if len(fUnnamedOut.Args) != 2 || fUnnamedOut.Args[1].Mode != "OUT" || fUnnamedOut.Args[1].Name != "" {
		t.Errorf("f_unnamed_out.Args: got %+v, want [{Mode:IN} {Mode:OUT Name:\"\"}]", fUnnamedOut.Args)
	}
}

func TestIntrospectEnum(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `CREATE TYPE public.mood AS ENUM ('happy', 'sad', 'neutral')`)
	if err != nil {
		t.Fatalf("create enum: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var found *ir.Type
	for _, obj := range objects {
		if typ, ok := obj.(*ir.Type); ok && typ.Name == "mood" && typ.Schema == "public" {
			found = typ
			break
		}
	}
	if found == nil {
		t.Fatal("introspect: type public.mood not found in results")
	}
	if found.Variant != "ENUM" {
		t.Errorf("type variant = %q, want ENUM", found.Variant)
	}
	wantVals := []string{"happy", "sad", "neutral"}
	if len(found.EnumValues) != len(wantVals) {
		t.Fatalf("enum values = %v, want %v", found.EnumValues, wantVals)
	}
	for i, v := range wantVals {
		if found.EnumValues[i] != v {
			t.Errorf("enum value[%d] = %q, want %q", i, found.EnumValues[i], v)
		}
	}
}

func TestIntrospectView(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `CREATE TABLE public.products (id bigint PRIMARY KEY, name text)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = conn.Exec(ctx, `CREATE VIEW public.product_names AS SELECT id, name FROM public.products`)
	if err != nil {
		t.Fatalf("create view: %v", err)
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var found *ir.View
	for _, obj := range objects {
		if v, ok := obj.(*ir.View); ok && v.Name == "product_names" && v.Schema == "public" {
			found = v
			break
		}
	}
	if found == nil {
		t.Fatal("introspect: view public.product_names not found in results")
	}
}
