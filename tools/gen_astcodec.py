#!/usr/bin/env python3
"""Writes compiler/src/syntax/astcodec.navi: the binary encoding of the AST
(compiler/src/syntax/ast.navi) that a crate's export data holds
(compiler/src/navih.navi). Run it again after changing ast.navi:

    python3 tools/gen_astcodec.py

Each struct and enum of ast.navi gets enc_X (writing it) and dec_X (making
it again). Nodes with an id (expressions, types, patterns, paths, ...) are
written once: a node met again (the parser shares a few) is written as a
reference to the first, so the decoded tree shares it too. Ids are not
written: decoding gives the nodes new ones, from its own range (files are
decoded on several threads). The few parts written by hand
(a file's text, a function's body left out) are in navih.navi.
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, "compiler/src/syntax/ast.navi")
OUT = os.path.join(ROOT, "compiler/src/syntax/astcodec.navi")

# Types written by hand (navih.navi) or as plain values.
SCALARS = {"u32": "u", "bool": "b", "String": "s", "Pos": "pos", "Kind": "k", "i32": "i"}
# Types not part of the encoding.
SKIP_TYPES = {"IdGen"}
# Types from elsewhere, with enc_X / dec_val_X written by hand (navih.navi).
BY_HAND = {"Token"}


def strip_comments(text):
    out = []
    for line in text.split("\n"):
        i = line.find("//")
        if i >= 0:
            line = line[:i]
        out.append(line)
    return "\n".join(out)


def split_top(s, sep=","):
    parts, depth, cur = [], 0, ""
    for ch in s:
        if ch in "<({":
            depth += 1
        elif ch in ">)}":
            depth -= 1
        if ch == sep and depth == 0:
            parts.append(cur)
            cur = ""
        else:
            cur += ch
    if cur.strip():
        parts.append(cur)
    return [p.strip() for p in parts if p.strip()]


def parse(text):
    text = strip_comments(text)
    structs, enums, order = {}, {}, []
    for m in re.finditer(r"pub struct (\w+) \{(.*?)\n\}", text, re.S):
        name, body = m.group(1), m.group(2)
        fields = []
        for f in split_top(body):
            fm = re.match(r"(pub\s+)?(\w+)\s*:\s*(.+)$", f, re.S)
            if fm:
                fields.append((fm.group(2), " ".join(fm.group(3).split())))
        structs[name] = fields
        order.append(name)
    for m in re.finditer(r"pub enum (\w+) \{(.*?)\}\n", text, re.S):
        name, body = m.group(1), m.group(2)
        variants = []
        for v in split_top(body):
            vm = re.match(r"(\w+)\s*(\((.*)\)|\{(.*)\})?$", v, re.S)
            vname = vm.group(1)
            if vm.group(3) is not None:
                variants.append((vname, "tuple", [" ".join(t.split()) for t in split_top(vm.group(3))]))
            elif vm.group(4) is not None:
                fs = []
                for f in split_top(vm.group(4)):
                    a, b = f.split(":", 1)
                    fs.append((a.strip(), " ".join(b.split())))
                variants.append((vname, "struct", fs))
            else:
                variants.append((vname, "unit", []))
        enums[name] = variants
        order.append(name)
    return structs, enums, order


class Gen:
    def __init__(self, structs, enums):
        self.structs = structs
        self.enums = enums
        self.out = []

    def has_id(self, name):
        return name in self.structs and any(f == "id" and t == "u32" for f, t in self.structs[name])

    # Code writing value v of type t (k: the nesting, for fresh names).
    def enc(self, t, v, k=0):
        if t in SCALARS:
            return f"e.{SCALARS[t]}({v});"
        if t == "&str":
            return f"e.s_str({v});"
        m = re.match(r"Option<(.+)>$", t)
        if m:
            y = f"y{k}"
            return f"match {v} {{ Some({y}) => {{ e.b(true); {self.enc(m.group(1), y, k + 1)} }} None => e.b(false), }}"
        m = re.match(r"Vec<(.+)>$", t)
        if m:
            inner = m.group(1)
            y = f"y{k}"
            w = f"({v})" if v.startswith("*") else v
            return f"e.u({w}.len() as u32); for {y} in {w}.iter() {{ {self.enc(inner, '*' + y, k + 1)} }}"
        m = re.match(r"&(mut )?(\w+)$", t)
        if m:
            return f"enc_{m.group(2)}(e, {v});"
        if t in self.structs or t in self.enums or t in BY_HAND:
            return f"enc_{t}(e, &{v});"
        raise Exception("enc: unknown type " + t)

    # An expression decoding a value of type t.
    def dec(self, t):
        if t in SCALARS:
            return f"d.{SCALARS[t]}()"
        if t == "&str":
            return "d.s_str()"
        if t == "&File":
            return "d.file"
        m = re.match(r"Option<(.+)>$", t)
        if m:
            inner = m.group(1)
            if inner.startswith("&") and not inner.startswith("&mut"):
                return f"if d.b() {{ let y: {inner} = {self.dec(inner)}; Some(y) }} else {{ None }}"
            return f"if d.b() {{ Some({self.dec(inner)}) }} else {{ None }}"
        m = re.match(r"Vec<(.+)>$", t)
        if m:
            inner = m.group(1)
            return f"{{ let n = d.u() as usize; let mut v: Vec<{inner}> = Vec::with_capacity(n); let mut i = 0; while i < n {{ v.push({self.dec(inner)}); i += 1; }} v }}"
        m = re.match(r"&(mut )?(\w+)$", t)
        if m:
            return f"dec_{m.group(2)}(d)"
        if t in self.structs or t in self.enums or t in BY_HAND:
            return f"dec_val_{t}(d)"
        raise Exception("dec: unknown type " + t)

    def struct(self, name, fields):
        o = self.out
        data = [(f, t) for f, t in fields if not (f == "id" and t == "u32")]
        by_ref = name not in ("ItemBase", "Token", "Name") or True
        # enc_X(e, x: &X)
        o.append(f"pub fn enc_{name}(e: &mut Enc, x: &{name}) {{")
        if self.has_id(name):
            o.append(f"    if e.again(x.id) {{ return; }}")
        for f, t in data:
            if name == "FnDecl" and f == "body":
                o.append("    e.fn_body(x);")
                continue
            if name == "FnDecl" and f == "elided":
                continue
            o.append("    " + self.enc(t, f"x.{f}"))
        if self.has_id(name):
            o.append(f"    e.done(x.id, {self.kind_no(name)});")
        o.append("}")
        o.append("")
        # dec_X(d) -> &mut X
        o.append(f"pub fn dec_{name}(d: &mut Dec) -> &mut {name} {{")
        if self.has_id(name):
            o.append(f"    if let Some(i) = d.again() {{ return d.{lname(name)}[i]; }}")
        for f, t in data:
            if name == "FnDecl" and f == "body":
                o.append("    let (body, elided) = d.fn_body();")
                continue
            if name == "FnDecl" and f == "elided":
                continue
            o.append(f"    let {safe(f)} = {self.dec(t)};")
        parts = []
        for f, t in fields:
            if f == "id" and t == "u32":
                parts.append("id: d.id()")
            else:
                parts.append(f"{f}: {safe(f)}" if safe(f) != f else f)
        o.append(f"    let x = &mut {name} {{ {', '.join(parts)} }};")
        if self.has_id(name):
            o.append(f"    d.{lname(name)}.push(x);")
        o.append("    x")
        o.append("}")
        o.append("")
        # by value
        o.append(f"pub fn dec_val_{name}(d: &mut Dec) -> {name} {{ *dec_{name}(d) }}")
        o.append("")

    # The number of a kind of node with an id (Enc counts each kind's
    # nodes, as Dec keeps them, apart).
    def kind_no(self, name):
        ids = [n for n in self.order if self.has_id(n)]
        return ids.index(name)

    def enum(self, name, variants):
        o = self.out
        o.append(f"pub fn enc_{name}(e: &mut Enc, x: &{name}) {{")
        o.append("    match x {")
        for i, (vn, kind, fs) in enumerate(variants):
            if kind == "unit":
                o.append(f"        {name}::{vn} => e.u({i}),")
            elif kind == "tuple":
                names = [f"a{j}" for j in range(len(fs))]
                body = " ".join(self.enc(t, "*" + n) for n, t in zip(names, fs))
                o.append(f"        {name}::{vn}({', '.join(names)}) => {{ e.u({i}); {body} }}")
            else:
                names = [f for f, _ in fs]
                body = " ".join(self.enc(t, "*" + f) for f, t in fs)
                o.append(f"        {name}::{vn} {{ {', '.join(names)} }} => {{ e.u({i}); {body} }}")
        o.append("    }")
        o.append("}")
        o.append("")
        o.append(f"pub fn dec_{name}(d: &mut Dec) -> &mut {name} {{")
        o.append(f"    let mut v = dec_val_{name}(d);")
        o.append("    &mut v")
        o.append("}")
        o.append("")
        o.append(f"pub fn dec_val_{name}(d: &mut Dec) -> {name} {{")
        o.append("    match d.u() {")
        for i, (vn, kind, fs) in enumerate(variants):
            if kind == "unit":
                o.append(f"        {i} => {name}::{vn},")
            elif kind == "tuple":
                lets = " ".join(f"let a{j} = {self.dec(t)};" for j, t in enumerate(fs))
                o.append(f"        {i} => {{ {lets} {name}::{vn}({', '.join(f'a{j}' for j in range(len(fs)))}) }}")
            else:
                lets = " ".join(f"let {safe(f)} = {self.dec(t)};" for f, t in fs)
                inits = ", ".join(f"{f}: {safe(f)}" if safe(f) != f else f for f, _ in fs)
                o.append(f"        {i} => {{ {lets} {name}::{vn} {{ {inits} }} }}")
        o.append(f"        _ => {self.default(name, variants)},")
        o.append("    }")
        o.append("}")
        o.append("")

    # The value to pass to enc for a field bound by reference in a match.
    def deref(self, t, n):
        if t in SCALARS:
            return "*" + n
        if t.startswith("&"):
            return "*" + n
        return n

    def default(self, name, variants):
        # (Export data is checked against its hash before decoding: a bad
        # tag is a bug.)
        return 'panic("navih: bad export data")'


def lname(name):
    return "n_" + re.sub(r"(?<!^)([A-Z])", r"_\1", name).lower()


def safe(f):
    return f + "_" if f in ("type", "match", "loop", "ref", "mod", "fn", "impl", "use", "in", "as") else f


def main():
    structs, enums, order = parse(open(SRC).read())
    g = Gen(structs, enums)
    g.order = [n for n in order if n not in SKIP_TYPES and n != "SourceFile"]
    for name in order:
        if name in SKIP_TYPES or name == "SourceFile":
            continue
        if name in structs:
            g.struct(name, structs[name])
        else:
            g.enum(name, enums[name])
    id_types = [n for n in order if g.has_id(n)]
    head = [
        "//! The binary encoding of the AST, for a crate's export data",
        "//! (crate::navih). Written by tools/gen_astcodec.py from ast.navi: do",
        "//! not edit, run the script again.",
        "",
        "use crate::syntax::ast::*;",
        "use crate::syntax::source::{File, Pos};",
        "use crate::syntax::token::{Token, Kind};",
        "pub use crate::navih::{Enc, Dec, enc_Token, dec_val_Token};",
        "",
        "/// The nodes decoded so far that later ones may refer to again, by",
        "/// kind.",
        "pub struct Seen {",
    ]
    for n in id_types:
        head.append(f"    pub {lname(n)}: Vec<&mut {n}>,")
    head.append("}")
    head.append("")
    head.append("pub fn new_seen() -> Seen {")
    head.append("    Seen { " + ", ".join(f"{lname(n)}: Vec::new()" for n in id_types) + " }")
    head.append("}")
    head.append("")
    text = "\n".join(head + g.out).rstrip() + "\n"
    # Dec's lists live in its Seen.
    for n in id_types:
        text = text.replace(f"d.{lname(n)}", f"d.seen.{lname(n)}")
    open(OUT, "w").write(text)
    print(f"wrote {OUT}: {len(structs)} structs, {len(enums)} enums")


if __name__ == "__main__":
    main()
