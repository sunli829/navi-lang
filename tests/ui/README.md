# Diagnostic tests

As rustc's ui tests: `navi build` must report exactly the annotated errors
and warnings. `//~ ERROR text` (or `//~ WARN text`) expects a diagnostic on
that line whose message contains `text`; `//~^` refers to the line above
(one `^` a line), `//~|` to the line of the annotation before it. A file
without `ERROR` annotations must build.
