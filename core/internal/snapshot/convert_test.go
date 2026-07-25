package snapshot

import (
	"encoding/json"
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

// ── VirtualType snapshot round-trip ───────────────────────────────────────────

func TestPopulateVirtualTypeTypeRef(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema: "public",
			Name:   "label",
			Body:   ir.VtypeTypeRef{Name: "text"},
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	raw, ok := snap.Objects["public.label"]
	if !ok {
		t.Fatal("expected public.label in snapshot")
	}
	var so SnapObject
	if err := json.Unmarshal(raw, &so); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if so.Kind != "virtual_type" {
		t.Errorf("Kind: got %q, want %q", so.Kind, "virtual_type")
	}
	if so.VirtualType == nil {
		t.Fatal("VirtualType field is nil")
	}
	if so.VirtualType.Body.Kind != "type_ref" {
		t.Errorf("Body.Kind: got %q, want %q", so.VirtualType.Body.Kind, "type_ref")
	}
	if so.VirtualType.Body.Name != "text" {
		t.Errorf("Body.Name: got %q, want %q", so.VirtualType.Body.Name, "text")
	}
	if so.VirtualType.Body.IsArray {
		t.Errorf("Body.IsArray: want false")
	}
}

func TestPopulateVirtualTypeTypeRefArray(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema: "public",
			Name:   "tags",
			Body:   ir.VtypeTypeRef{Name: "text", IsArray: true},
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["public.tags"], &so)
	if so.VirtualType.Body.Kind != "type_ref" || !so.VirtualType.Body.IsArray {
		t.Errorf("Body: got kind=%q array=%v, want type_ref/true", so.VirtualType.Body.Kind, so.VirtualType.Body.IsArray)
	}
}

func TestPopulateVirtualTypeComposite(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema: "public",
			Name:   "point",
			Body: ir.VtypeComposite{
				Fields: []ir.VtypeField{
					{Name: "x", Type: ir.VtypeTypeRef{Name: "float8"}},
					{Name: "y", Type: ir.VtypeTypeRef{Name: "float8"}},
				},
			},
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["public.point"], &so)
	body := so.VirtualType.Body
	if body.Kind != "composite" {
		t.Errorf("Body.Kind: got %q, want composite", body.Kind)
	}
	if len(body.Fields) != 2 {
		t.Fatalf("Body.Fields: got %d, want 2", len(body.Fields))
	}
	if body.Fields[0].Name != "x" || body.Fields[0].Type.Name != "float8" {
		t.Errorf("Fields[0]: got %+v", body.Fields[0])
	}
	if body.Fields[1].Name != "y" || body.Fields[1].Type.Name != "float8" {
		t.Errorf("Fields[1]: got %+v", body.Fields[1])
	}
}

func TestPopulateVirtualTypeUnion(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema: "public",
			Name:   "shape",
			Body: ir.VtypeUnion{
				Members: []ir.VtypeBody{
					ir.VtypeComposite{Fields: []ir.VtypeField{
						{Name: "radius", Type: ir.VtypeTypeRef{Name: "float8"}},
					}},
					ir.VtypeTypeRef{Name: "text"},
				},
			},
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["public.shape"], &so)
	body := so.VirtualType.Body
	if body.Kind != "union" {
		t.Errorf("Body.Kind: got %q, want union", body.Kind)
	}
	if len(body.Members) != 2 {
		t.Fatalf("Body.Members: got %d, want 2", len(body.Members))
	}
	if body.Members[0].Kind != "composite" {
		t.Errorf("Members[0].Kind: got %q, want composite", body.Members[0].Kind)
	}
	if body.Members[1].Kind != "type_ref" || body.Members[1].Name != "text" {
		t.Errorf("Members[1]: got kind=%q name=%q", body.Members[1].Kind, body.Members[1].Name)
	}
}

func TestPopulateVirtualTypeSchemaQualifiedRef(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema: "billing",
			Name:   "status",
			Body:   ir.VtypeTypeRef{Schema: "billing", Name: "payment_method"},
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["billing.status"], &so)
	body := so.VirtualType.Body
	if body.Schema != "billing" || body.Name != "payment_method" {
		t.Errorf("Body: got schema=%q name=%q", body.Schema, body.Name)
	}
}

func TestPopulateVirtualTypeJsonFormat(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema:     "public",
			Name:       "event",
			Body:       ir.VtypeTypeRef{Name: "text"},
			JsonFormat: "json",
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["public.event"], &so)
	if so.VirtualType.JsonFormat != "json" {
		t.Errorf("JsonFormat: got %q, want %q", so.VirtualType.JsonFormat, "json")
	}
}

func TestPopulateVirtualTypeJsonFormatDefaultOmitted(t *testing.T) {
	// When JsonFormat is empty (default), the json_format field is omitted from JSON.
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{Schema: "public", Name: "tag", Body: ir.VtypeTypeRef{Name: "text"}},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["public.tag"], &so)
	if so.VirtualType.JsonFormat != "" {
		t.Errorf("JsonFormat: got %q, want empty (default jsonb)", so.VirtualType.JsonFormat)
	}
}

func TestPopulateVirtualTypeWithComment(t *testing.T) {
	comment := "user profile shape"
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.VirtualType{
			Schema:  "public",
			Name:    "user_profile",
			Body:    ir.VtypeTypeRef{Name: "text"},
			Comment: &comment,
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var so SnapObject
	_ = json.Unmarshal(snap.Objects["public.user_profile"], &so)
	if so.VirtualType.Comment == nil || *so.VirtualType.Comment != comment {
		t.Errorf("Comment: got %v, want %q", so.VirtualType.Comment, comment)
	}
}

// ── Operator class access method round-trip ───────────────────────────────────

func TestPopulateOperatorClassUsing(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.OperatorClass{
			Schema:       "public",
			Name:         "my_ops",
			AccessMethod: "gin",
			Body:         "CREATE OPERATOR CLASS public.my_ops FOR TYPE int4 USING gin AS STORAGE int4",
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	raw, ok := snap.Objects["public.my_ops USING gin"]
	if !ok {
		t.Fatal("expected public.my_ops USING gin in snapshot")
	}
	var so SnapObject
	if err := json.Unmarshal(raw, &so); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if so.Opaque == nil {
		t.Fatal("expected opaque payload")
	}
	if so.Opaque.Using != "gin" {
		t.Errorf("Using: got %q, want %q", so.Opaque.Using, "gin")
	}
}

func TestPopulateOperatorFamilyUsing(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.OperatorFamily{
			Schema:       "public",
			Name:         "my_family",
			AccessMethod: "gist",
			Body:         "CREATE OPERATOR FAMILY public.my_family USING gist",
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	raw, ok := snap.Objects["public.my_family USING gist FAMILY"]
	if !ok {
		t.Fatal("expected public.my_family USING gist FAMILY in snapshot")
	}
	var so SnapObject
	if err := json.Unmarshal(raw, &so); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if so.Opaque == nil || so.Opaque.Using != "gist" {
		t.Errorf("Using: got %+v, want gist", so.Opaque)
	}
}

// TestPopulateOperatorClassFamilyNoCollision guards the data-loss bug where an
// operator class and the same-named operator family PostgreSQL auto-creates for
// it both keyed to qualName(schema,name), so one silently overwrote the other in
// the flat snapshot map. Both must survive Populate under distinct keys.
func TestPopulateOperatorClassFamilyNoCollision(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.OperatorClass{
			Schema: "public", Name: "widget", AccessMethod: "btree",
			Body: "CREATE OPERATOR CLASS public.widget FOR TYPE int4 USING btree AS STORAGE int4",
		},
		&ir.OperatorFamily{
			Schema: "public", Name: "widget", AccessMethod: "btree",
			Body: "CREATE OPERATOR FAMILY public.widget USING btree",
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	var classKey, familyKey int
	for key, raw := range snap.Objects {
		var so SnapObject
		if err := json.Unmarshal(raw, &so); err != nil {
			t.Fatalf("unmarshal %q: %v", key, err)
		}
		switch so.Opaque.Kind {
		case "operator_class":
			classKey++
		case "operator_family":
			familyKey++
		}
	}
	if classKey != 1 || familyKey != 1 {
		t.Errorf("expected one class and one family to survive; got class=%d family=%d (collision dropped one)", classKey, familyKey)
	}
}
