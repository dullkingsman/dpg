package snapshot

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestPopulateTable(t *testing.T) {
	comment := "main users table"
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Comment: &comment, Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "integer"}, NotNull: true},
		}},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	raw, ok := snap.Objects["public.users"]
	if !ok {
		t.Fatal("expected public.users in snapshot")
	}
	if raw == nil {
		t.Fatal("raw entry is nil")
	}
}

func TestPopulateView(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "active_users", Query: "SELECT * FROM users WHERE active"},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Objects["public.active_users"]; !ok {
		t.Fatal("expected public.active_users in snapshot")
	}
}

func TestPopulateFunction(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.Function{Schema: "public", Name: "get_user", ReturnType: ir.TypeRef{Name: "integer"}},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Objects["public.get_user()"]; !ok {
		t.Fatal("expected public.get_user() in snapshot")
	}
}

func TestPopulateMultipleObjects(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "t1"},
		&ir.Table{Schema: "public", Name: "t2"},
		&ir.Schema{Name: "myschema"},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	if len(snap.Objects) != 3 {
		t.Errorf("expected 3 objects in snapshot, got %d", len(snap.Objects))
	}
}

func TestPopulateRole(t *testing.T) {
	snap := &pipeline.Snapshot{}
	comment := "service account"
	objects := []pipeline.IRObject{
		&ir.Role{Name: "app_user", Comment: &comment},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Objects["app_user"]; !ok {
		t.Fatal("expected app_user in snapshot")
	}
}
