// Package ast provides typed accessors for pg_query_go parse results and is
// the bridge between pipeline.PGParseResult (which stores Raw as any to avoid
// importing pg_query in the pipeline package) and the concrete protobuf types.
package ast

import (
	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// Unwrap extracts the *pg_query.ParseResult stored in a pipeline.PGParseResult.
// Returns nil if Raw was not populated (e.g. during tests that skip the parser).
func Unwrap(r pipeline.PGParseResult) *pg_query.ParseResult {
	if r.Raw == nil {
		return nil
	}
	pr, _ := r.Raw.(*pg_query.ParseResult)
	return pr
}

// FirstStmt returns the first statement node from the parse result, or nil.
func FirstStmt(r pipeline.PGParseResult) *pg_query.Node {
	pr := Unwrap(r)
	if pr == nil || len(pr.Stmts) == 0 {
		return nil
	}
	return pr.Stmts[0].Stmt
}

// AsCreateTable returns the CreateStmt if the parse result is a CREATE TABLE.
func AsCreateTable(r pipeline.PGParseResult) (*pg_query.CreateStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateStmt, true
}

// AsCreateFunction returns the CreateFunctionStmt for a CREATE FUNCTION/PROCEDURE.
func AsCreateFunction(r pipeline.PGParseResult) (*pg_query.CreateFunctionStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateFunctionStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateFunctionStmt, true
}

// AsViewStmt returns the ViewStmt for a CREATE VIEW.
func AsViewStmt(r pipeline.PGParseResult) (*pg_query.ViewStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	vs, ok := node.Node.(*pg_query.Node_ViewStmt)
	if !ok {
		return nil, false
	}
	return vs.ViewStmt, true
}

// AsCreateEnum returns the CreateEnumStmt for a CREATE TYPE ... AS ENUM.
func AsCreateEnum(r pipeline.PGParseResult) (*pg_query.CreateEnumStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateEnumStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateEnumStmt, true
}

// AsCreateSchema returns the CreateSchemaStmt for a CREATE SCHEMA.
func AsCreateSchema(r pipeline.PGParseResult) (*pg_query.CreateSchemaStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateSchemaStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateSchemaStmt, true
}

// AsCreateExtension returns the CreateExtensionStmt for a CREATE EXTENSION.
func AsCreateExtension(r pipeline.PGParseResult) (*pg_query.CreateExtensionStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateExtensionStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateExtensionStmt, true
}

// AsCreateSeq returns the CreateSeqStmt for a CREATE SEQUENCE.
func AsCreateSeq(r pipeline.PGParseResult) (*pg_query.CreateSeqStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateSeqStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateSeqStmt, true
}

// AsCreateRole returns the CreateRoleStmt for a CREATE ROLE.
func AsCreateRole(r pipeline.PGParseResult) (*pg_query.CreateRoleStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateRoleStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateRoleStmt, true
}

// AsCreateDomain returns the CreateDomainStmt for a CREATE DOMAIN.
func AsCreateDomain(r pipeline.PGParseResult) (*pg_query.CreateDomainStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateDomainStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateDomainStmt, true
}

// AsCreateForeignTable returns the CreateForeignTableStmt for a CREATE FOREIGN TABLE.
func AsCreateForeignTable(r pipeline.PGParseResult) (*pg_query.CreateForeignTableStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateForeignTableStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateForeignTableStmt, true
}

// AsCreateFdw returns the CreateFdwStmt for a CREATE FOREIGN DATA WRAPPER.
func AsCreateFdw(r pipeline.PGParseResult) (*pg_query.CreateFdwStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateFdwStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateFdwStmt, true
}

// AsCreateForeignServer returns the CreateForeignServerStmt for a CREATE SERVER.
func AsCreateForeignServer(r pipeline.PGParseResult) (*pg_query.CreateForeignServerStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateForeignServerStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateForeignServerStmt, true
}

// AsCreateUserMapping returns the CreateUserMappingStmt for a CREATE USER MAPPING.
func AsCreateUserMapping(r pipeline.PGParseResult) (*pg_query.CreateUserMappingStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateUserMappingStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateUserMappingStmt, true
}

// AsCreateTablespace returns the CreateTableSpaceStmt for a CREATE TABLESPACE.
func AsCreateTablespace(r pipeline.PGParseResult) (*pg_query.CreateTableSpaceStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateTableSpaceStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateTableSpaceStmt, true
}

// AsCreateEventTrigger returns the CreateEventTrigStmt for a CREATE EVENT TRIGGER.
func AsCreateEventTrigger(r pipeline.PGParseResult) (*pg_query.CreateEventTrigStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateEventTrigStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateEventTrigStmt, true
}

// AsCreateOpClass returns the CreateOpClassStmt for a CREATE OPERATOR CLASS.
func AsCreateOpClass(r pipeline.PGParseResult) (*pg_query.CreateOpClassStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateOpClassStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateOpClassStmt, true
}

// AsCreateOpFamily returns the CreateOpFamilyStmt for a CREATE OPERATOR FAMILY.
func AsCreateOpFamily(r pipeline.PGParseResult) (*pg_query.CreateOpFamilyStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateOpFamilyStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateOpFamilyStmt, true
}

// AsCreateStats returns the CreateStatsStmt for a CREATE STATISTICS.
func AsCreateStats(r pipeline.PGParseResult) (*pg_query.CreateStatsStmt, bool) {
	node := FirstStmt(r)
	if node == nil {
		return nil, false
	}
	cs, ok := node.Node.(*pg_query.Node_CreateStatsStmt)
	if !ok {
		return nil, false
	}
	return cs.CreateStatsStmt, true
}
