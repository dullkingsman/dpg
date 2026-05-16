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

	ci := New()
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

	ci := New()
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

	ci := New()
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
