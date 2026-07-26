//go:build integration

package introspect_test

import (
	"context"
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
