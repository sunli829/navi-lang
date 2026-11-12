# Name resolution tests

- `ok/*.navi`: single-file crates that must resolve without errors.
- `err/*.navi`: single-file crates with errors. A line containing
  `// error: text` expects an error on that line whose message contains
  `text`; every reported error must be expected by such a line.
