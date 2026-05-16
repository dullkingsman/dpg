; Inject SQL into plpgsql / sql function bodies
((function_declaration
  (sql_expression) @_lang_clause
  body: (dollar_quoted_string) @injection.content)
 (#match? @_lang_clause "(?i)LANGUAGE\\s+(plpgsql|sql)")
 (#set! injection.language "sql"))

; Inject Python into python function bodies
((function_declaration
  (sql_expression) @_lang_clause
  body: (dollar_quoted_string) @injection.content)
 (#match? @_lang_clause "(?i)LANGUAGE\\s+plpython")
 (#set! injection.language "python"))

; Inject SQL into procedure bodies
((procedure_declaration
  language: (identifier) @_lang
  body: (dollar_quoted_string) @injection.content)
 (#match? @_lang "(?i)^(plpgsql|sql)$")
 (#set! injection.language "sql"))

; Inject SQL into CHECK constraint expressions, DEFAULT values, and policy USING expressions
(check_directive
  (sql_expression) @injection.content
  (#set! injection.language "sql"))

; Inject SQL into MIGRATE REMOVE bodies
(migrate_remove_directive
  (sql_expression) @injection.content
  (#set! injection.language "sql"))
