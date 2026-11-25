# navi doc

HTML documentation of a crate, its dependencies and std, as rustdoc's
(`cargo doc`), and doctests: the code blocks of the documentation run by
`navi test`, as rustdoc's.

## The command

    navi doc [path] [-p name] [--no-deps] [--open] [-o dir]

- Documents the crate (a workspace: its members, or `-p name`), every
  dependency and std into `target/doc/` (`-o` another directory);
  `--no-deps` only the crate (links into the others are left out).
- `--open` opens `target/doc/<crate>/index.html` (open, xdg-open, start).
- std's pages depend only on std's source: they are made once per std
  and kept in NAVI_CACHE, then copied.
- Warnings, as rustdoc's: intra-doc links that name nothing.

## What is documented

What another crate can name: the items a module path of `pub` modules
reaches, with `pub` visibility (`pub(crate)` and the like are not), and
what `pub use` re-exports. As rustdoc:

- An item is documented where it is defined if that path is public; one
  reached only by a re-export (from a private module or another crate) is
  documented at the re-export (inlined); a re-export of an item documented
  elsewhere is listed under "Re-exports" with a link.
- `#[doc(hidden)]` hides an item (and what is in it).
- A struct's private fields are shown as `/* private fields */`.
- Impls: inherent impls on the type's page (their `pub` items), trait
  impls on the type's page and among the trait's implementors, whatever
  module (or documented crate) holds them. Impls on primitive types (in
  std) make primitive pages (`primitive.str.html`).
- Trait items: required and provided methods, associated types and
  constants; an impl's method without docs shows the trait's.

## Pages (as rustdoc's)

```
target/doc/
  index.html                 the documented crates
  static/style.css, main.js  one copy for all
  search-index.js            every crate's items (name, kind, path, summary)
  <crate>/index.html         the crate's root module
  <crate>/<mod>/index.html   a module
  <crate>/<mod>/struct.Name.html  enum.  trait.  fn.  type.  constant.
                             static.  derive.  primitive.
```

- Each page: a sidebar (the crate, the module's items or the type's
  methods and impls), a search box, the declaration (signatures with
  every named type linked), the documentation, then the sections.
- A module page lists Re-exports, Modules, Structs, Enums, Traits,
  Functions, Type Aliases, Constants, Statics, Derive Macros, each with the
  first paragraph of its documentation.
- Search: main.js filters search-index.js as one types (by name, then by
  path), without a server.
- Code in documentation and declarations is highlighted (keywords,
  strings, comments, numbers).

## Markdown

CommonMark's blocks and inlines that documentation uses, as rustdoc's
(pulldown-cmark with tables and strikethrough):

- blocks: ATX headings, paragraphs, fenced (``` and ~~~) and indented code,
  block quotes, lists (bullet and ordered, nested by indentation), thematic
  breaks, GitHub tables;
- inlines: code spans, emphasis and strong (`*`, `_`), strikethrough
  (`~~`), links (inline, reference, autolinks), images, backslash escapes,
  hard breaks;
- intra-doc links: `` [`Name`] ``, `[Name]`, `[text](path::Name)` resolve as
  a path written in the item's module would (std::vec::Vec, Self::new,
  crate::a::B);
- raw HTML is shown as text (escaped).

## Doctests

`navi test` runs the code blocks in the documentation (`///` and `//!`) of
a library crate (its `src/lib.navi` tree), as rustdoc's:

- A block without a language, or `navi` (or `rust`), is a test; other
  languages are not code. Attributes after the language: `ignore` (not
  built), `no_run` (built, not run), `should_panic`, `compile_fail` (built
  alone; it must not compile).
- A line starting with `# ` (or a lone `#`) is compiled but hidden in the
  pages.
- Each block becomes a `#[test]` of one program that depends on the crate:
  its code is the body of the test unless it has `fn main`, then its items
  are in a module of their own and the test calls its main. The crate is
  named by its name (`use mycrate::Thing;`).
- Names as rustdoc's: `src/lib.navi - Thing::new (line 12)`; `navi test
  name` filters them with the others; `navi test --doc` runs only them.

## Layout

```
compiler/src/doc/
  mod.navi       the command: crates, front end, output, std's cache
  model.navi     what is documented: modules, items, impls, URLs
  render.navi    declarations as HTML (types linked)
  markdown.navi  Markdown to HTML, intra-doc links, highlighting
  page.navi      pages, sidebar, search index, CSS and JS
  doctest.navi   doctests: blocks, the program, running it
```
