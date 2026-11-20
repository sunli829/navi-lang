# Name resolution tests

- `ok/*.navi`: single-file crates that must resolve without errors.
- `err/*.navi`: single-file crates with errors, annotated as rustc's ui
  tests: `//~ ERROR text` expects an error on that line whose message
  contains `text` (`//~^` the line above); every reported error must be
  expected.
