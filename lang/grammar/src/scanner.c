#include "tree_sitter/parser.h"
#include <string.h>
#include <stdlib.h>

// Token types (must match the order in grammar.js externals array)
enum TokenType {
  DOLLAR_QUOTED_STRING,
  BLOCK_COMMENT_CONTENT,
};

typedef struct {
  char *tag;
  size_t tag_len;
  size_t tag_cap;
} Scanner;

static Scanner *scanner_new(void) {
  Scanner *s = malloc(sizeof(Scanner));
  s->tag = NULL;
  s->tag_len = 0;
  s->tag_cap = 0;
  return s;
}

static void scanner_free(Scanner *s) {
  free(s->tag);
  free(s);
}

static bool scan_dollar_quoted_string(Scanner *s, TSLexer *lexer) {
  // Already consumed the leading '$' — read optional tag until next '$'
  s->tag_len = 0;
  while (lexer->lookahead != '$' && lexer->lookahead != 0) {
    char c = (char)lexer->lookahead;
    if (s->tag_len + 1 >= s->tag_cap) {
      s->tag_cap = s->tag_cap == 0 ? 16 : s->tag_cap * 2;
      s->tag = realloc(s->tag, s->tag_cap);
    }
    s->tag[s->tag_len++] = c;
    lexer->advance(lexer, false);
  }
  if (lexer->lookahead != '$') return false;
  lexer->advance(lexer, false); // consume closing '$' of opening tag

  // Scan body until we find $tag$
  while (lexer->lookahead != 0) {
    if (lexer->lookahead == '$') {
      lexer->advance(lexer, false);
      // Try to match the tag
      size_t matched = 0;
      while (matched < s->tag_len && lexer->lookahead == (unsigned char)s->tag[matched]) {
        lexer->advance(lexer, false);
        matched++;
      }
      if (matched == s->tag_len && lexer->lookahead == '$') {
        lexer->advance(lexer, false); // consume closing '$'
        lexer->result_symbol = DOLLAR_QUOTED_STRING;
        return true;
      }
      // Not a match — continue scanning
      continue;
    }
    lexer->advance(lexer, false);
  }
  return false;
}

static bool scan_block_comment_content(TSLexer *lexer) {
  int depth = 1;
  while (lexer->lookahead != 0) {
    if (lexer->lookahead == '/') {
      lexer->advance(lexer, false);
      if (lexer->lookahead == '*') {
        lexer->advance(lexer, false);
        depth++;
      }
    } else if (lexer->lookahead == '*') {
      lexer->advance(lexer, false);
      if (lexer->lookahead == '/') {
        lexer->advance(lexer, false);
        depth--;
        if (depth == 0) {
          lexer->result_symbol = BLOCK_COMMENT_CONTENT;
          return true;
        }
      }
    } else {
      lexer->advance(lexer, false);
    }
  }
  return false;
}

void *tree_sitter_dpg_external_scanner_create(void) {
  return scanner_new();
}

void tree_sitter_dpg_external_scanner_destroy(void *payload) {
  scanner_free((Scanner *)payload);
}

bool tree_sitter_dpg_external_scanner_scan(
  void *payload,
  TSLexer *lexer,
  const bool *valid_symbols
) {
  Scanner *s = (Scanner *)payload;

  // Skip whitespace
  while (lexer->lookahead == ' ' || lexer->lookahead == '\t' ||
         lexer->lookahead == '\n' || lexer->lookahead == '\r') {
    lexer->advance(lexer, true);
  }

  if (valid_symbols[DOLLAR_QUOTED_STRING] && lexer->lookahead == '$') {
    lexer->advance(lexer, false);
    return scan_dollar_quoted_string(s, lexer);
  }

  if (valid_symbols[BLOCK_COMMENT_CONTENT]) {
    return scan_block_comment_content(lexer);
  }

  return false;
}

unsigned tree_sitter_dpg_external_scanner_serialize(
  void *payload,
  char *buffer
) {
  Scanner *s = (Scanner *)payload;
  if (s->tag_len == 0) return 0;
  size_t len = s->tag_len > 127 ? 127 : s->tag_len;
  memcpy(buffer, s->tag, len);
  return (unsigned)len;
}

void tree_sitter_dpg_external_scanner_deserialize(
  void *payload,
  const char *buffer,
  unsigned length
) {
  Scanner *s = (Scanner *)payload;
  if (length == 0) { s->tag_len = 0; return; }
  if (s->tag_cap < length + 1) {
    s->tag_cap = length + 1;
    s->tag = realloc(s->tag, s->tag_cap);
  }
  memcpy(s->tag, buffer, length);
  s->tag_len = length;
}
