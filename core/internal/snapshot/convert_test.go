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

// TestPopulateColumnSerial proves Column.Serial threads through to the
// snapshot unchanged and survives a JSON marshal/unmarshal round-trip (the
// snapshot file is what's actually persisted to disk and compared against
// on the next plan/apply).
func TestPopulateColumnSerial(t *testing.T) {
	marker := "SERIAL"
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "widgets", Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "integer"}, Serial: &marker, NotNull: true},
		}},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	raw, ok := snap.Objects["public.widgets"]
	if !ok {
		t.Fatal("expected public.widgets in snapshot")
	}

	// raw is already the persisted JSON form (json.RawMessage) — unmarshal
	// it back the same way a fresh `dpg plan` reads the snapshot file.
	var obj SnapObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj.Table == nil || len(obj.Table.Columns) != 1 {
		t.Fatal("expected one column after round-trip")
	}
	col := obj.Table.Columns[0]
	if col.Serial == nil || *col.Serial != "SERIAL" {
		t.Errorf("Serial got %v after round-trip, want \"SERIAL\"", col.Serial)
	}
}

// TestPopulateColumnGeneratedVirtual proves Column.Generated's Stored=false
// (VIRTUAL, PostgreSQL 18+) threads through toSnapColumn into
// SnapColumn.GeneratedVirtual and survives a JSON round-trip, the same way
// TestPopulateColumnSerial proves it for Serial above.
func TestPopulateColumnGeneratedVirtual(t *testing.T) {
	snap := &pipeline.Snapshot{}
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "orders", Columns: []*ir.Column{
			{Name: "amount", Type: ir.TypeRef{Name: "numeric"}},
			{
				Name: "amount_with_tax", Type: ir.TypeRef{Name: "numeric"},
				Generated: &ir.Generated{Expr: "amount * 1.08", Stored: false},
			},
		}},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	raw, ok := snap.Objects["public.orders"]
	if !ok {
		t.Fatal("expected public.orders in snapshot")
	}
	var obj SnapObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj.Table == nil || len(obj.Table.Columns) != 2 {
		t.Fatal("expected two columns after round-trip")
	}
	col := obj.Table.Columns[1]
	if col.Generated == nil || *col.Generated != "amount * 1.08" {
		t.Errorf("Generated got %v after round-trip, want \"amount * 1.08\"", col.Generated)
	}
	if !col.GeneratedVirtual {
		t.Error("GeneratedVirtual got false after round-trip, want true")
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

// TestPopulateOperatorFamilyMembers guards RFC Section 14.4's loose-member
// persistence, including the G-live regression case: members must survive
// Populate even for a Reconstructed (live-introspected) family, unlike
// BodyHash — sourceBodyHash intentionally returns "" for a reconstructed
// body, but Members/OpFamilyMembersStructured are populated unconditionally
// (see convert.go's OperatorFamily case), since they carry no
// hand-written-vs-catalog-form ambiguity the way a whole-body hash does.
func TestPopulateOperatorFamilyMembers(t *testing.T) {
	for _, reconstructed := range []bool{false, true} {
		snap := &pipeline.Snapshot{}
		objects := []pipeline.IRObject{
			&ir.OperatorFamily{
				Schema: "public", Name: "my_family2", AccessMethod: "btree",
				Body:          "CREATE OPERATOR FAMILY public.my_family2 USING btree",
				Reconstructed: reconstructed,
				Members: []pipeline.OpFamilyMember{
					{Number: 1, Name: pipeline.Identifier{Name: "<"}, LeftType: "integer", RightType: "bigint"},
				},
			},
		}
		if err := Populate(snap, objects); err != nil {
			t.Fatal(err)
		}
		raw, ok := snap.Objects["public.my_family2 USING btree FAMILY"]
		if !ok {
			t.Fatal("expected public.my_family2 USING btree FAMILY in snapshot")
		}
		var so SnapObject
		if err := json.Unmarshal(raw, &so); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if so.Opaque == nil || !so.Opaque.OpFamilyMembersStructured {
			t.Fatalf("reconstructed=%v: OpFamilyMembersStructured not set: %+v", reconstructed, so.Opaque)
		}
		if len(so.Opaque.OpFamilyMembers) != 1 || so.Opaque.OpFamilyMembers[0].Name != "<" {
			t.Errorf("reconstructed=%v: OpFamilyMembers: got %+v", reconstructed, so.Opaque.OpFamilyMembers)
		}
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

// TestPopulateOperatorOverloadNoCollision guards the same class of data-loss
// bug for a different pair: PostgreSQL operators can be overloaded — the same
// symbol declared for different operand types (e.g. + for integer vs
// numeric) — but ir.Operator.QualifiedName previously keyed only on
// (schema, name), so a second overload silently overwrote the first in the
// flat snapshot map. Both must survive Populate under distinct keys.
func TestPopulateOperatorOverloadNoCollision(t *testing.T) {
	snap := &pipeline.Snapshot{}
	intType := ir.TypeRef{Name: "integer"}
	numType := ir.TypeRef{Name: "numeric"}
	objects := []pipeline.IRObject{
		&ir.Operator{
			Schema: "public", Name: "+", LeftType: &intType, RightType: &intType,
			Body: "CREATE OPERATOR public.+ (FUNCTION = int4pl, LEFTARG = integer, RIGHTARG = integer)",
		},
		&ir.Operator{
			Schema: "public", Name: "+", LeftType: &numType, RightType: &numType,
			Body: "CREATE OPERATOR public.+ (FUNCTION = numeric_add, LEFTARG = numeric, RIGHTARG = numeric)",
		},
	}
	if err := Populate(snap, objects); err != nil {
		t.Fatal(err)
	}
	if len(snap.Objects) != 2 {
		t.Errorf("expected 2 distinct operators (same symbol, different operand types) to survive; got %d (collision dropped one)", len(snap.Objects))
	}
}

// TestToSnapIndexWithQuoteNormalization guards against a spurious-drift
// regression found while live-testing the diffIndexes content-comparison fix:
// pg_get_indexdef always quotes reloption values (e.g. fillfactor='70'), while
// hand-written DPG source may not (fillfactor = 70). Without normalization, an
// unchanged WITH-bearing index would show drift on every verify/plan --live
// against a real catalog. Both spellings must produce the same SnapIndex.With.
func TestToSnapIndexWithQuoteNormalization(t *testing.T) {
	unquoted := ToSnapIndex(&ir.Index{
		Name: "i", Columns: []pipeline.IndexColumn{{Name: "a"}},
		With: []pipeline.StorageParam{{Key: "fillfactor", Value: "70"}},
	})
	quoted := ToSnapIndex(&ir.Index{
		Name: "i", Columns: []pipeline.IndexColumn{{Name: "a"}},
		With: []pipeline.StorageParam{{Key: "fillfactor", Value: "'70'"}},
	})
	if unquoted.With != "fillfactor=70" {
		t.Errorf("unquoted With = %q, want %q", unquoted.With, "fillfactor=70")
	}
	if unquoted != quoted {
		t.Errorf("quoted vs unquoted WITH value must compare equal: %+v != %+v", unquoted, quoted)
	}
}

// TestToSnapIndexSortOrderAndNulls guards the fuller content-comparison fix:
// Columns must now carry ASC/DESC and NULLS FIRST/LAST so a sort-order-only
// edit on an otherwise-identical index is detected (previously the joined
// Columns string dropped this information entirely).
func TestToSnapIndexSortOrderAndNulls(t *testing.T) {
	si := ToSnapIndex(&ir.Index{
		Name: "i",
		Columns: []pipeline.IndexColumn{
			{Name: "a", SortOrder: "DESC", Nulls: "LAST"},
			{Name: "b"},
		},
	})
	want := `a DESC NULLS LAST, b`
	if si.Columns != want {
		t.Errorf("Columns = %q, want %q", si.Columns, want)
	}
}
