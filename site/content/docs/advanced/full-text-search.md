---
title: "Full-Text Search"
description: "TEXT SEARCH CONFIGURATION, DICTIONARY, PARSER, and TEMPLATE declarations."
weight: 1
---

## TEXT SEARCH CONFIGURATION

```sql
SCHEMA public {
    TEXT SEARCH CONFIGURATION english_unaccented (COPY = pg_catalog.english) {
        MAPPING FOR hword, hword_part, word
            WITH unaccent, english_stem;
    }
}
```

```sql
-- emits
CREATE TEXT SEARCH CONFIGURATION "public"."english_unaccented"
    (COPY = pg_catalog.english);

ALTER TEXT SEARCH CONFIGURATION "public"."english_unaccented"
    ALTER MAPPING FOR hword, hword_part, word
    WITH unaccent, english_stem;
```

## TEXT SEARCH DICTIONARY

```sql
SCHEMA public {
    TEXT SEARCH DICTIONARY english_ispell (
        TEMPLATE  = ispell,
        DictFile  = english,
        AffFile   = english,
        StopWords = english
    );
}
```

```sql
-- emits
CREATE TEXT SEARCH DICTIONARY "public"."english_ispell" (
    TEMPLATE  = ispell,
    DictFile  = english,
    AffFile   = english,
    StopWords = english
);
```

## TEXT SEARCH PARSER

```sql
SCHEMA public {
    TEXT SEARCH PARSER my_parser (
        START    = prsd_start,
        GETTOKEN = prsd_nexttoken,
        END      = prsd_end,
        LEXTYPES = prsd_lextype,
        HEADLINE = prsd_headline
    );
}
```

```sql
-- emits
CREATE TEXT SEARCH PARSER "public"."my_parser" (
    START    = prsd_start,
    GETTOKEN = prsd_nexttoken,
    END      = prsd_end,
    LEXTYPES = prsd_lextype,
    HEADLINE = prsd_headline
);
```

## TEXT SEARCH TEMPLATE

```sql
SCHEMA public {
    TEXT SEARCH TEMPLATE ispell_template (
        LEXIZE = dispell_lexize,
        INIT   = dispell_init
    );
}
```

```sql
-- emits
CREATE TEXT SEARCH TEMPLATE "public"."ispell_template" (
    LEXIZE = dispell_lexize,
    INIT   = dispell_init
);
```

## Diffing behaviour

| Object | Change | SQL | Safety |
|--------|--------|-----|--------|
| Configuration | Add/change mapping | `ALTER TEXT SEARCH CONFIGURATION ALTER MAPPING` | `SAFE` |
| Configuration | Change template (`COPY`) | `DROP` + `CREATE` | `DESTRUCTIVE` |
| Dictionary | Any option change | `ALTER TEXT SEARCH DICTIONARY` | `SAFE` |
| Parser / Template | Any change | `DROP` + `CREATE` | `DESTRUCTIVE` |
