; SCHEMA block creates a namespace scope
(schema_block
  name: (identifier) @definition.namespace)

; MACRO declaration
(macro_declaration
  name: (identifier) @definition.macro)

; Object definitions
(table_declaration
  name: (_) @definition.type)

(view_declaration
  name: (_) @definition.type)

(materialized_view_declaration
  name: (_) @definition.type)

(function_declaration
  name: (_) @definition.function)

(procedure_declaration
  name: (_) @definition.function)

(aggregate_declaration
  name: (_) @definition.function)

(enum_declaration
  name: (_) @definition.type)

(composite_type_declaration
  name: (_) @definition.type)

(range_type_declaration
  name: (_) @definition.type)

(domain_declaration
  name: (_) @definition.type)

(virtual_type_declaration
  name: (_) @definition.type)

(sequence_declaration
  name: (_) @definition.var)

(role_declaration
  name: (identifier) @definition.var)

; Column definitions (members of a table scope)
(column_def
  name: (identifier) @definition.var)

; References to objects
(table_constraint
  name: (identifier) @reference)

(column_def
  type: (sql_type (_) @reference))
