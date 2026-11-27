#!/usr/bin/env python3
"""Writes the /// docs of the integer and float types' inherent methods and
constants in lib/std/src/num.navi and lib/std/src/num/ext.navi, as Rust's
std documents them (summary, details, # Panics, # Examples per type).

Usage: tools/numdocs.py [lib/std/src]: rewrites the docs of num.navi and
num/ext.navi in place (the docs above each item are replaced, so running it
again is a no-op). numdocs.py <dir> <out-dir>: reads num.navi and ext.navi
from dir, writes them to out-dir.

Prose is wrapped as navi fmt wraps comments (80 columns). Then check with
navi test --doc lib/std src_num.
"""
import re
import sys
import textwrap

INTS = ["i8", "i16", "i32", "i64", "isize", "i128", "u8", "u16", "u32", "u64", "usize", "u128"]
FLOATS = ["f32", "f64"]
BITS = {"i8": 8, "i16": 16, "i32": 32, "i64": 64, "isize": 64, "i128": 128,
        "u8": 8, "u16": 16, "u32": 32, "u64": 64, "usize": 64, "u128": 128}
SUP = str.maketrans("0123456789-", "⁰¹²³⁴⁵⁶⁷⁸⁹⁻")


def sup(n):
    return str(n).translate(SUP)


class C:
    """What the templates know about a type."""

    def __init__(self, t):
        self.T = t
        self.float = t in FLOATS
        if self.float:
            self.bits = 32 if t == "f32" else 64
            self.signed = True
            self.U = "u32" if t == "f32" else "u64"
            return
        self.bits = BITS[t]
        self.signed = t.startswith("i")
        self.U = "u" + t[1:]
        self.S = "i" + t[1:]
        self.nbytes = self.bits // 8
        if self.signed:
            self.min = -(1 << (self.bits - 1))
            self.max = (1 << (self.bits - 1)) - 1
        else:
            self.min = 0
            self.max = (1 << self.bits) - 1

    def r(self, v):
        """A literal of this type as a method's receiver."""
        if str(v).startswith("0x") and self.T in ("isize", "usize"):
            # (navi takes no isize/usize suffix on a hex literal: a bug)
            return f"({v} as {self.T})"
        return f"({v}{self.T})" if str(v).startswith("-") else f"{v}{self.T}"

    def hexv(self, v, typed=False):
        """v (taken as the type's bits, 0 <= v < 2^bits) as a literal or an
        expression of the type; typed: with the type's suffix."""
        T = self.T
        if typed and T in ("isize", "usize"):
            return f"({C('i64' if T == 'isize' else 'u64').hexv(v, True)} as {T})"
        sfx = T if typed else ""
        if self.signed and v >> (self.bits - 1):
            neg = (1 << self.bits) - v
            if neg < (1 << 64):
                return f"-0x{neg:x}{sfx}"
            u = C(self.U)
            return f"({u.hexv(v, True)} as {T})"
        if v < (1 << 64):
            return f"0x{v:x}{sfx}"
        return f"((0x{v >> 64:x}{T} << 64) | 0x{v & ((1 << 64) - 1):x})"

    def pattern(self):
        """Rust's byte pattern for the type's size: 0x12, 0x1234, 0x12345678, ..."""
        s = ("1234567890" * 4)[: 2 * self.nbytes]
        return int(s, 16), [s[i:i + 2] for i in range(0, len(s), 2)]


def swap(v, nbytes):
    return int.from_bytes(v.to_bytes(nbytes, "big"), "little")


def revbits(v, bits):
    return int(format(v, f"0{bits}b")[::-1], 2)


def rotl(v, k, bits):
    m = (1 << bits) - 1
    return ((v << k) | (v >> (bits - k))) & m


def byte_array(bs):
    """[0x12, 0x34, ...], split over lines of 8 when long."""
    items = [f"0x{b}" for b in bs]
    if len(items) <= 8:
        return ["[" + ", ".join(items) + "]"]
    lines = ["["]
    for i in range(0, len(items), 8):
        lines.append("    " + ", ".join(items[i:i + 8]) + ",")
    lines.append("]")
    return lines


def doc(summary, details=None, panics=None, ex=None, attr="", extra=None):
    """A doc block (markdown) from its parts. ex: lines of the example."""
    parts = [summary]
    if details:
        parts += details if isinstance(details, list) else [details]
    if panics:
        parts.append("# Panics")
        parts += panics if isinstance(panics, list) else [panics]
    if ex is not None:
        parts.append("# Examples")
        if extra:
            parts.append(extra)
        parts.append("```" + attr + "\n" + "\n".join(ex) + "\n```")
    return "\n\n".join(parts)


# --- Integer templates ------------------------------------------------------

def int_doc(c, name):
    T, b, s, U = c.T, c.bits, c.signed, c.U
    r = c.r
    if name == "BITS":
        return doc("The size of this integer type in bits.", ex=[f"assert_eq({T}::BITS, {b});"])
    if name == "MIN":
        if s:
            what = f"The smallest value that can be represented by this integer type (−2{sup(b - 1)})."
            if b <= 64:
                ex = [f"assert_eq({T}::MIN, {c.min});"]
            else:
                ex = [f'assert_eq({T}::MIN.to_string(), "{c.min}");']
        else:
            what = "The smallest value that can be represented by this integer type (0)."
            ex = [f"assert_eq({T}::MIN, 0);"]
        return doc(what, ex=ex)
    if name == "MAX":
        if s:
            what = f"The largest value that can be represented by this integer type (2{sup(b - 1)} − 1)."
        else:
            what = f"The largest value that can be represented by this integer type (2{sup(b)} − 1)."
        if T in ("isize", "usize"):
            what += f" navi's targets are all 64-bit, so this is `{'i64' if s else 'u64'}::MAX`."
        if c.max < (1 << 64):
            ex = [f"assert_eq({T}::MAX, {c.max});"]
        else:
            ex = [f'assert_eq({T}::MAX.to_string(), "{c.max}");']
        return doc(what, ex=ex)

    # Arithmetic: wrapping.
    if name == "wrapping_add":
        ex = ([f"assert_eq({r(100)}.wrapping_add(27), 127);",
               f"assert_eq({T}::MAX.wrapping_add(2), {T}::MIN + 1);"] if s else
              [f"assert_eq({r(200)}.wrapping_add(55), 255);",
               f"assert_eq({r(200)}.wrapping_add({T}::MAX), 199);"])
        return doc("Wrapping (modular) addition. Computes `self + rhs`, wrapping around at the boundary of the type.", ex=ex)
    if name == "wrapping_sub":
        ex = ([f"assert_eq({r(0)}.wrapping_sub(127), -127);",
               f"assert_eq({r(-2)}.wrapping_sub({T}::MAX), {T}::MAX);"] if s else
              [f"assert_eq({r(100)}.wrapping_sub(100), 0);",
               f"assert_eq({r(100)}.wrapping_sub({T}::MAX), 101);"])
        return doc("Wrapping (modular) subtraction. Computes `self - rhs`, wrapping around at the boundary of the type.", ex=ex)
    if name == "wrapping_mul":
        ex = [f"assert_eq({r(10)}.wrapping_mul(12), 120);",
              f"assert_eq({T}::MAX.wrapping_mul(2), {'-2' if s else T + '::MAX - 1'});"]
        return doc("Wrapping (modular) multiplication. Computes `self * rhs`, wrapping around at the boundary of the type.", ex=ex)
    if name == "wrapping_neg":
        if s:
            return doc("Wrapping (modular) negation. Computes `-self`, wrapping around at the boundary of the type.",
                       "The only case where such wrapping can occur is when one negates `MIN` on a signed type "
                       "(where `MIN` is the negative minimal value for the type); this is a positive value that is "
                       "too large to represent in the type. In such a case, this function returns `MIN` itself.",
                       ex=[f"assert_eq({r(100)}.wrapping_neg(), -100);",
                           f"assert_eq({r(-100)}.wrapping_neg(), 100);",
                           f"assert_eq({T}::MIN.wrapping_neg(), {T}::MIN);"])
        return doc("Wrapping (modular) negation. Computes `-self`, wrapping around at the boundary of the type.",
                   "Since unsigned types do not have negative equivalents all applications of this function will "
                   "wrap (except for `-0`). For values smaller than the corresponding signed type's maximum the "
                   "result is the same as casting the corresponding signed value. Any larger values are equivalent "
                   "to `MAX + 1 - (val - MAX - 1)` where `MAX` is the corresponding signed type's maximum.",
                   ex=[f"assert_eq({r(0)}.wrapping_neg(), 0);",
                       f"assert_eq({T}::MAX.wrapping_neg(), 1);",
                       f"assert_eq({r(13)}.wrapping_neg(), (!13) + 1);",
                       f"assert_eq({r(42)}.wrapping_neg(), !(42 - 1));"])
    if name == "wrapping_pow":
        return doc("Wrapping (modular) exponentiation. Computes `self.pow(exp)`, wrapping around at the boundary of the type.",
                   ex=[f"assert_eq({r(3)}.wrapping_pow(4), 81);",
                       f"assert_eq({T}::MAX.wrapping_pow(2), 1);"])
    if name == "wrapping_div":
        if s:
            return doc("Wrapping (modular) division. Computes `self / rhs`, wrapping around at the boundary of the type.",
                       "The only case where such wrapping can occur is when one divides `MIN / -1` on a signed type "
                       "(where `MIN` is the negative minimal value for the type); this is equivalent to `-MIN`, a "
                       "positive value that is too large to represent in the type. In such a case, this function "
                       "returns `MIN` itself.",
                       panics="This function will panic if `rhs` is zero.",
                       ex=[f"assert_eq({r(100)}.wrapping_div(10), 10);",
                           f"assert_eq({T}::MIN.wrapping_div(-1), {T}::MIN);"])
        return doc("Wrapping (modular) division. Computes `self / rhs`.",
                   "Wrapped division on unsigned types is just normal division. There's no way wrapping could ever "
                   "happen. This function exists so that all operations are accounted for in the wrapping operations.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(100)}.wrapping_div(10), 10);"])
    if name == "wrapping_rem":
        if s:
            return doc("Wrapping (modular) remainder. Computes `self % rhs`, wrapping around at the boundary of the type.",
                       "Such wrap-around never actually occurs mathematically; implementation artifacts make `x % y` "
                       "invalid for `MIN / -1` on a signed type (where `MIN` is the negative minimal value). In such "
                       "a case, this function returns `0`.",
                       panics="This function will panic if `rhs` is zero.",
                       ex=[f"assert_eq({r(100)}.wrapping_rem(10), 0);",
                           f"assert_eq({T}::MIN.wrapping_rem(-1), 0);"])
        return doc("Wrapping (modular) remainder. Computes `self % rhs`.",
                   "Wrapped remainder calculation on unsigned types is just the regular remainder calculation. "
                   "There's no way wrapping could ever happen. This function exists so that all operations are "
                   "accounted for in the wrapping operations.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(100)}.wrapping_rem(10), 0);"])
    if name == "wrapping_div_euclid":
        if s:
            return doc("Wrapping Euclidean division. Computes `self.div_euclid(rhs)`, wrapping around at the boundary of the type.",
                       "Wrapping will only occur in `MIN / -1` on a signed type (where `MIN` is the negative minimal "
                       "value for the type). This is equivalent to `-MIN`, a positive value that is too large to "
                       "represent in the type. In this case, this method returns `MIN` itself.",
                       panics="This function will panic if `rhs` is zero.",
                       ex=[f"assert_eq({r(100)}.wrapping_div_euclid(10), 10);",
                           f"assert_eq({T}::MIN.wrapping_div_euclid(-1), {T}::MIN);"])
        return doc("Wrapping Euclidean division. Computes `self.div_euclid(rhs)`.",
                   "Wrapped division on unsigned types is just normal division. There's no way wrapping could ever "
                   "happen. This function exists so that all operations are accounted for in the wrapping "
                   "operations. Since, for the positive integers, all common definitions of division are equal, "
                   "this is exactly equal to `self.wrapping_div(rhs)`.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(100)}.wrapping_div_euclid(10), 10);"])
    if name == "wrapping_rem_euclid":
        if s:
            return doc("Wrapping Euclidean remainder. Computes `self.rem_euclid(rhs)`, wrapping around at the boundary of the type.",
                       "Wrapping will only occur in `MIN % -1` on a signed type (where `MIN` is the negative minimal "
                       "value for the type). In this case, this method returns 0.",
                       panics="This function will panic if `rhs` is zero.",
                       ex=[f"assert_eq({r(100)}.wrapping_rem_euclid(10), 0);",
                           f"assert_eq({T}::MIN.wrapping_rem_euclid(-1), 0);"])
        return doc("Wrapping Euclidean modulo. Computes `self.rem_euclid(rhs)`.",
                   "Wrapped modulo calculation on unsigned types is just the regular remainder calculation. There's "
                   "no way wrapping could ever happen. This function exists so that all operations are accounted for "
                   "in the wrapping operations. Since, for the positive integers, all common definitions of division "
                   "are equal, this is exactly equal to `self.wrapping_rem(rhs)`.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(100)}.wrapping_rem_euclid(10), 0);"])
    if name == "wrapping_abs":
        return doc("Wrapping (modular) absolute value. Computes `self.abs()`, wrapping around at the boundary of the type.",
                   "The only case where such wrapping can occur is when one takes the absolute value of the negative "
                   "minimal value for the type; this is a positive value that is too large to represent in the type. "
                   "In such a case, this function returns `MIN` itself.",
                   ex=[f"assert_eq({r(100)}.wrapping_abs(), 100);",
                       f"assert_eq({r(-100)}.wrapping_abs(), 100);",
                       f"assert_eq({T}::MIN.wrapping_abs(), {T}::MIN);",
                       f"assert_eq({T}::MIN.wrapping_abs() as {U}, {T}::MAX as {U} + 1);"])
    if name in ("wrapping_shl", "wrapping_shr"):
        left = name == "wrapping_shl"
        op, dirn = ("<<", "left") if left else (">>", "right")
        summary = (f"Panic-free bitwise shift-{dirn}; yields `self {op} mask(rhs)`, where `mask` removes any "
                   "high-order bits of `rhs` that would cause the shift to exceed the bitwidth of the type.")
        details = (f"Note that this is *not* the same as a rotate-{dirn}; the RHS of a wrapping shift-{dirn} is "
                   "restricted to the range of the type, rather than the bits shifted out of the LHS being returned "
                   f"to the other end. The primitive integer types all implement a `rotate_{dirn}` function, which "
                   "may be what you want instead.")
        if left:
            ex = ([f"assert_eq({r(-1)}.wrapping_shl(7), -128);", f"assert_eq({r(-1)}.wrapping_shl(128), -1);"] if s else
                  [f"assert_eq({r(1)}.wrapping_shl(7), 128);", f"assert_eq({r(1)}.wrapping_shl(128), 1);"])
        else:
            ex = ([f"assert_eq({r(-128)}.wrapping_shr(7), -1);", f"assert_eq({r(-128)}.wrapping_shr(128), -128);"] if s else
                  [f"assert_eq({r(128)}.wrapping_shr(7), 1);", f"assert_eq({r(128)}.wrapping_shr(128), 128);"])
        return doc(summary, details, ex=ex)

    # Arithmetic: overflowing.
    ovf = ("Returns a tuple of the {what} along with a boolean indicating whether an arithmetic overflow would "
           "occur. If an overflow would have occurred then the wrapped value is returned.")
    if name == "overflowing_add":
        return doc("Calculates `self + rhs`.", ovf.format(what="addition"),
                   ex=[f"assert_eq({r(5)}.overflowing_add(2), (7, false));",
                       f"assert_eq({T}::MAX.overflowing_add(1), ({T}::MIN, true));"])
    if name == "overflowing_sub":
        ex = ([f"assert_eq({r(5)}.overflowing_sub(2), (3, false));",
               f"assert_eq({T}::MIN.overflowing_sub(1), ({T}::MAX, true));"] if s else
              [f"assert_eq({r(5)}.overflowing_sub(2), (3, false));",
               f"assert_eq({r(0)}.overflowing_sub(1), ({T}::MAX, true));"])
        return doc("Calculates `self - rhs`.", ovf.format(what="subtraction"), ex=ex)
    if name == "overflowing_mul":
        return doc("Calculates the multiplication of `self` and `rhs`.", ovf.format(what="multiplication"),
                   ex=[f"assert_eq({r(5)}.overflowing_mul(2), (10, false));",
                       f"assert_eq({T}::MAX.overflowing_mul(2), ({'-2' if s else T + '::MAX - 1'}, true));"])
    if name == "overflowing_neg":
        if s:
            return doc("Negates self, overflowing if this is equal to the minimum value.",
                       "Returns a tuple of the negated version of self along with a boolean indicating whether an "
                       f"overflow happened. If `self` is the minimum value (`{T}::MIN`), then the minimum value will "
                       "be returned again and `true` will be returned for an overflow happening.",
                       ex=[f"assert_eq({r(2)}.overflowing_neg(), (-2, false));",
                           f"assert_eq({T}::MIN.overflowing_neg(), ({T}::MIN, true));"])
        return doc("Negates self in a wrapping fashion.",
                   "Returns `!self + 1` using wrapping operations to return the value that represents the negation "
                   "of this unsigned value. Note that for positive unsigned values overflow always occurs, but "
                   "negating 0 does not overflow.",
                   ex=[f"assert_eq({r(0)}.overflowing_neg(), (0, false));",
                       f"assert_eq({r(2)}.overflowing_neg(), ({T}::MAX - 1, true));"])
    if name == "overflowing_pow":
        return doc("Raises self to the power of `exp`, using exponentiation by squaring.",
                   "Returns a tuple of the exponentiation along with a bool indicating whether an overflow happened.",
                   ex=[f"assert_eq({r(3)}.overflowing_pow(4), (81, false));",
                       f"assert_eq({T}::MAX.overflowing_pow(2), (1, true));"])
    if name in ("overflowing_div", "overflowing_rem"):
        div = name == "overflowing_div"
        what = "divisor" if div else "remainder"
        if s:
            details = (f"Returns a tuple of the {what} along with a boolean indicating whether an arithmetic overflow "
                       f"would occur. If an overflow would occur then {'self' if div else '0'} is returned.")
            ex = ([f"assert_eq({r(5)}.overflowing_div(2), (2, false));",
                   f"assert_eq({T}::MIN.overflowing_div(-1), ({T}::MIN, true));"] if div else
                  [f"assert_eq({r(5)}.overflowing_rem(2), (1, false));",
                   f"assert_eq({T}::MIN.overflowing_rem(-1), (0, true));"])
        else:
            details = (f"Returns a tuple of the {what} along with a boolean indicating whether an arithmetic overflow "
                       "would occur. Note that for unsigned integers overflow never occurs, so the second value is "
                       "always `false`.")
            ex = ([f"assert_eq({r(5)}.overflowing_div(2), (2, false));"] if div else
                  [f"assert_eq({r(5)}.overflowing_rem(2), (1, false));"])
        summary = ("Calculates the divisor when `self` is divided by `rhs`." if div else
                   "Calculates the remainder when `self` is divided by `rhs`.")
        return doc(summary, details, panics="This function will panic if `rhs` is zero.", ex=ex)
    if name == "overflowing_abs":
        return doc("Computes the absolute value of `self`.",
                   "Returns a tuple of the absolute version of self along with a boolean indicating whether an "
                   f"overflow happened. If self is the minimum value (`{T}::MIN`), then the minimum value will be "
                   "returned again and true will be returned for an overflow happening.",
                   ex=[f"assert_eq({r(10)}.overflowing_abs(), (10, false));",
                       f"assert_eq({r(-10)}.overflowing_abs(), (10, false));",
                       f"assert_eq({T}::MIN.overflowing_abs(), ({T}::MIN, true));"])
    if name in ("overflowing_shl", "overflowing_shr"):
        left = name == "overflowing_shl"
        dirn = "left" if left else "right"
        details = ("Returns a tuple of the shifted version of self along with a boolean indicating whether the shift "
                   "value was larger than or equal to the number of bits. If the shift value is too large, then "
                   "value is masked (N-1) where N is the number of bits, and this value is then used to perform the "
                   "shift.")
        ex = ([f"assert_eq({r('0x1')}.overflowing_shl(4), (0x10, false));",
               f"assert_eq({r('0x1')}.overflowing_shl({b + 4}), (0x10, true));",
               f"assert_eq({r('0x10')}.overflowing_shl({b - 1}), (0, false));"] if left else
              [f"assert_eq({r('0x10')}.overflowing_shr(4), (0x1, false));",
               f"assert_eq({r('0x10')}.overflowing_shr({b + 4}), (0x1, true));"])
        return doc(f"Shifts self {dirn} by `rhs` bits.", details, ex=ex)

    # Arithmetic: checked.
    if name == "checked_add":
        return doc("Checked integer addition. Computes `self + rhs`, returning `None` if overflow occurred.",
                   ex=[f"assert_eq(({T}::MAX - 2).checked_add(1), Some({T}::MAX - 1));",
                       f"assert_eq(({T}::MAX - 2).checked_add(3), None);"])
    if name == "checked_sub":
        ex = ([f"assert_eq(({T}::MIN + 2).checked_sub(1), Some({T}::MIN + 1));",
               f"assert_eq(({T}::MIN + 2).checked_sub(3), None);"] if s else
              [f"assert_eq({r(1)}.checked_sub(1), Some(0));",
               f"assert_eq({r(0)}.checked_sub(1), None);"])
        return doc("Checked integer subtraction. Computes `self - rhs`, returning `None` if overflow occurred.", ex=ex)
    if name == "checked_mul":
        return doc("Checked integer multiplication. Computes `self * rhs`, returning `None` if overflow occurred.",
                   ex=[f"assert_eq({T}::MAX.checked_mul(1), Some({T}::MAX));",
                       f"assert_eq({T}::MAX.checked_mul(2), None);"])
    if name == "checked_div":
        if s:
            return doc("Checked integer division. Computes `self / rhs`, returning `None` if `rhs == 0` or the "
                       "division results in overflow.",
                       ex=[f"assert_eq(({T}::MIN + 1).checked_div(-1), Some({T}::MAX));",
                           f"assert_eq({T}::MIN.checked_div(-1), None);",
                           f"assert_eq({r(1)}.checked_div(0), None);"])
        return doc("Checked integer division. Computes `self / rhs`, returning `None` if `rhs == 0`.",
                   ex=[f"assert_eq({r(128)}.checked_div(2), Some(64));",
                       f"assert_eq({r(1)}.checked_div(0), None);"])
    if name == "checked_rem":
        if s:
            return doc("Checked integer remainder. Computes `self % rhs`, returning `None` if `rhs == 0` or the "
                       "division results in overflow.",
                       ex=[f"assert_eq({r(5)}.checked_rem(2), Some(1));",
                           f"assert_eq({r(5)}.checked_rem(0), None);",
                           f"assert_eq({T}::MIN.checked_rem(-1), None);"])
        return doc("Checked integer remainder. Computes `self % rhs`, returning `None` if `rhs == 0`.",
                   ex=[f"assert_eq({r(5)}.checked_rem(2), Some(1));",
                       f"assert_eq({r(5)}.checked_rem(0), None);"])
    if name == "checked_div_euclid":
        if s:
            return doc("Checked Euclidean division. Computes `self.div_euclid(rhs)`, returning `None` if `rhs == 0` "
                       "or the division results in overflow.",
                       ex=[f"assert_eq(({T}::MIN + 1).checked_div_euclid(-1), Some({T}::MAX));",
                           f"assert_eq({T}::MIN.checked_div_euclid(-1), None);",
                           f"assert_eq({r(1)}.checked_div_euclid(0), None);"])
        return doc("Checked Euclidean division. Computes `self.div_euclid(rhs)`, returning `None` if `rhs == 0`.",
                   ex=[f"assert_eq({r(128)}.checked_div_euclid(2), Some(64));",
                       f"assert_eq({r(1)}.checked_div_euclid(0), None);"])
    if name == "checked_rem_euclid":
        if s:
            return doc("Checked Euclidean remainder. Computes `self.rem_euclid(rhs)`, returning `None` if `rhs == 0` "
                       "or the division results in overflow.",
                       ex=[f"assert_eq({r(5)}.checked_rem_euclid(2), Some(1));",
                           f"assert_eq({r(5)}.checked_rem_euclid(0), None);",
                           f"assert_eq({T}::MIN.checked_rem_euclid(-1), None);"])
        return doc("Checked Euclidean modulo. Computes `self.rem_euclid(rhs)`, returning `None` if `rhs == 0`.",
                   ex=[f"assert_eq({r(5)}.checked_rem_euclid(2), Some(1));",
                       f"assert_eq({r(5)}.checked_rem_euclid(0), None);"])
    if name == "checked_neg":
        if s:
            return doc("Checked negation. Computes `-self`, returning `None` if `self == MIN`.",
                       ex=[f"assert_eq({r(5)}.checked_neg(), Some(-5));",
                           f"assert_eq({T}::MIN.checked_neg(), None);"])
        return doc("Checked negation. Computes `-self`, returning `None` unless `self == 0`.",
                   "Note that negating any positive integer will overflow.",
                   ex=[f"assert_eq({r(0)}.checked_neg(), Some(0));",
                       f"assert_eq({r(1)}.checked_neg(), None);"])
    if name == "checked_pow":
        return doc("Checked exponentiation. Computes `self.pow(exp)`, returning `None` if overflow occurred.",
                   ex=[f"assert_eq({r(8)}.checked_pow(2), Some(64));",
                       f"assert_eq({T}::MAX.checked_pow(2), None);"])
    if name == "checked_shl":
        return doc("Checked shift left. Computes `self << rhs`, returning `None` if `rhs` is larger than or equal to "
                   "the number of bits in `self`.",
                   ex=[f"assert_eq({r('0x1')}.checked_shl(4), Some(0x10));",
                       f"assert_eq({r('0x1')}.checked_shl({b}), None);",
                       f"assert_eq({r('0x10')}.checked_shl({b - 1}), Some(0));"])
    if name == "checked_shr":
        return doc("Checked shift right. Computes `self >> rhs`, returning `None` if `rhs` is larger than or equal to "
                   "the number of bits in `self`.",
                   ex=[f"assert_eq({r('0x10')}.checked_shr(4), Some(0x1));",
                       f"assert_eq({r('0x10')}.checked_shr({b}), None);"])
    if name == "checked_abs":
        return doc("Checked absolute value. Computes `self.abs()`, returning `None` if `self == MIN`.",
                   ex=[f"assert_eq({r(-5)}.checked_abs(), Some(5));",
                       f"assert_eq({T}::MIN.checked_abs(), None);"])
    if name == "checked_isqrt":
        return doc("Returns the square root of the number, rounded down.",
                   "Returns `None` if `self` is negative.",
                   ex=[f"assert_eq({r(10)}.checked_isqrt(), Some(3));",
                       f"assert_eq({r(-1)}.checked_isqrt(), None);"])
    neg_or_zero = "negative or zero" if s else "zero"
    if name == "checked_ilog":
        return doc("Returns the logarithm of the number with respect to an arbitrary base, rounded down.",
                   [f"Returns `None` if the number is {neg_or_zero}, or if the base is not at least 2.",
                    "This method might not be optimized owing to implementation details; `checked_ilog2` can "
                    "produce results more efficiently for base 2, and `checked_ilog10` can produce results more "
                    "efficiently for base 10."],
                   ex=[f"assert_eq({r(5)}.checked_ilog(5), Some(1));",
                       f"assert_eq({r(0)}.checked_ilog(5), None);",
                       f"assert_eq({r(5)}.checked_ilog(1), None);"])
    if name == "checked_ilog2":
        return doc("Returns the base 2 logarithm of the number, rounded down.",
                   f"Returns `None` if the number is {neg_or_zero}.",
                   ex=[f"assert_eq({r(2)}.checked_ilog2(), Some(1));",
                       f"assert_eq({r(0)}.checked_ilog2(), None);"])
    if name == "checked_ilog10":
        return doc("Returns the base 10 logarithm of the number, rounded down.",
                   f"Returns `None` if the number is {neg_or_zero}.",
                   ex=[f"assert_eq({r(10)}.checked_ilog10(), Some(1));",
                       f"assert_eq({r(0)}.checked_ilog10(), None);"])
    if name == "checked_next_power_of_two":
        return doc("Returns the smallest power of two greater than or equal to `self`. If the next power of two is "
                   "greater than the type's maximum value, `None` is returned, otherwise the power of two is wrapped "
                   "in `Some`.",
                   ex=[f"assert_eq({r(2)}.checked_next_power_of_two(), Some(2));",
                       f"assert_eq({r(3)}.checked_next_power_of_two(), Some(4));",
                       f"assert_eq({T}::MAX.checked_next_power_of_two(), None);"])
    if name == "checked_next_multiple_of":
        return doc("Calculates the smallest value greater than or equal to `self` that is a multiple of `rhs`. "
                   "Returns `None` if `rhs` is zero or the operation would result in overflow.",
                   ex=[f"assert_eq({r(16)}.checked_next_multiple_of(8), Some(16));",
                       f"assert_eq({r(23)}.checked_next_multiple_of(8), Some(24));",
                       f"assert_eq({r(1)}.checked_next_multiple_of(0), None);",
                       f"assert_eq({T}::MAX.checked_next_multiple_of(2), None);"])

    # Arithmetic: saturating.
    sat = "saturating at the numeric bounds instead of overflowing."
    if name == "saturating_add":
        ex = ([f"assert_eq({r(100)}.saturating_add(1), 101);",
               f"assert_eq({T}::MAX.saturating_add(100), {T}::MAX);",
               f"assert_eq({T}::MIN.saturating_add(-1), {T}::MIN);"] if s else
              [f"assert_eq({r(100)}.saturating_add(1), 101);",
               f"assert_eq({T}::MAX.saturating_add(127), {T}::MAX);"])
        return doc(f"Saturating integer addition. Computes `self + rhs`, {sat}", ex=ex)
    if name == "saturating_sub":
        ex = ([f"assert_eq({r(100)}.saturating_sub(127), -27);",
               f"assert_eq({T}::MIN.saturating_sub(100), {T}::MIN);",
               f"assert_eq({T}::MAX.saturating_sub(-1), {T}::MAX);"] if s else
              [f"assert_eq({r(100)}.saturating_sub(27), 73);",
               f"assert_eq({r(13)}.saturating_sub(127), 0);"])
        return doc(f"Saturating integer subtraction. Computes `self - rhs`, {sat}", ex=ex)
    if name == "saturating_mul":
        ex = ([f"assert_eq({r(10)}.saturating_mul(12), 120);",
               f"assert_eq({T}::MAX.saturating_mul(10), {T}::MAX);",
               f"assert_eq({T}::MIN.saturating_mul(10), {T}::MIN);"] if s else
              [f"assert_eq({r(2)}.saturating_mul(10), 20);",
               f"assert_eq({T}::MAX.saturating_mul(10), {T}::MAX);"])
        return doc(f"Saturating integer multiplication. Computes `self * rhs`, {sat}", ex=ex)
    if name == "saturating_pow":
        ex = ([f"assert_eq({r(-4)}.saturating_pow(3), -64);",
               f"assert_eq({T}::MIN.saturating_pow(2), {T}::MAX);",
               f"assert_eq({T}::MIN.saturating_pow(3), {T}::MIN);"] if s else
              [f"assert_eq({r(4)}.saturating_pow(3), 64);",
               f"assert_eq({T}::MAX.saturating_pow(2), {T}::MAX);"])
        return doc(f"Saturating integer exponentiation. Computes `self.pow(exp)`, {sat}", ex=ex)
    if name == "saturating_div":
        ex = ([f"assert_eq({r(5)}.saturating_div(2), 2);",
               f"assert_eq({T}::MAX.saturating_div(-1), {T}::MIN + 1);",
               f"assert_eq({T}::MIN.saturating_div(-1), {T}::MAX);"] if s else
              [f"assert_eq({r(5)}.saturating_div(2), 2);"])
        return doc(f"Saturating integer division. Computes `self / rhs`, {sat}",
                   panics="This function will panic if `rhs` is zero.", ex=ex)
    if name == "saturating_abs":
        return doc("Saturating absolute value. Computes `self.abs()`, returning `MAX` if `self == MIN` instead of overflowing.",
                   ex=[f"assert_eq({r(100)}.saturating_abs(), 100);",
                       f"assert_eq({r(-100)}.saturating_abs(), 100);",
                       f"assert_eq({T}::MIN.saturating_abs(), {T}::MAX);",
                       f"assert_eq(({T}::MIN + 1).saturating_abs(), {T}::MAX);"])
    if name == "saturating_neg":
        return doc("Saturating integer negation. Computes `-self`, returning `MAX` if `self == MIN` instead of overflowing.",
                   ex=[f"assert_eq({r(100)}.saturating_neg(), -100);",
                       f"assert_eq({r(-100)}.saturating_neg(), 100);",
                       f"assert_eq({T}::MIN.saturating_neg(), {T}::MAX);",
                       f"assert_eq({T}::MAX.saturating_neg(), {T}::MIN + 1);"])

    # Plain arithmetic.
    if name == "pow":
        return doc("Raises self to the power of `exp`, using exponentiation by squaring.",
                   panics="This function will panic if the result overflows (navi checks integer overflow in every "
                          "build; Rust only in debug builds). Use `checked_pow`, `wrapping_pow`, `saturating_pow` or "
                          "`overflowing_pow` when it may.",
                   ex=[f"let x = {r(2)};", "", "assert_eq(x.pow(5), 32);", "assert_eq(x.pow(0), 1);"])
    if name == "abs":
        return doc("Computes the absolute value of `self`.",
                   "The absolute value of " f"`{T}::MIN` cannot be represented as an `{T}`, and attempting to "
                   "calculate it will cause an overflow.",
                   panics=f"This function will panic if `self` is `{T}::MIN` (navi checks integer overflow in every "
                          "build; Rust only in debug builds). `wrapping_abs`, `checked_abs`, `saturating_abs`, "
                          "`overflowing_abs` and `unsigned_abs` handle it.",
                   ex=[f"assert_eq({r(10)}.abs(), 10);", f"assert_eq({r(-10)}.abs(), 10);"])
    if name == "unsigned_abs":
        return doc("Computes the absolute value of `self` without any wrapping or panicking.",
                   ex=[f"assert_eq({r(100)}.unsigned_abs(), 100{U});",
                       f"assert_eq({r(-100)}.unsigned_abs(), 100{U});",
                       f"assert_eq({T}::MIN.unsigned_abs(), {T}::MAX as {U} + 1);"])
    if name == "abs_diff":
        if s:
            return doc("Computes the absolute difference between `self` and `other`.",
                       "This function always returns the correct answer without overflow or panics by returning an "
                       "unsigned integer.",
                       ex=[f"assert_eq({r(100)}.abs_diff(80), 20{U});",
                           f"assert_eq({r(100)}.abs_diff(110), 10{U});",
                           f"assert_eq({r(-100)}.abs_diff(80), 180{U});",
                           f"assert_eq({r(-100)}.abs_diff(-120), 20{U});",
                           f"assert_eq({T}::MIN.abs_diff({T}::MAX), {U}::MAX);"])
        return doc("Computes the absolute difference between `self` and `other`.",
                   ex=[f"assert_eq({r(100)}.abs_diff(80), 20);",
                       f"assert_eq({r(100)}.abs_diff(110), 10);",
                       f"assert_eq({r(0)}.abs_diff({T}::MAX), {T}::MAX);"])
    if name == "signum":
        return doc("Returns a number representing sign of `self`.",
                   "- `0` if the number is zero\n- `1` if the number is positive\n- `-1` if the number is negative",
                   ex=[f"assert_eq({r(10)}.signum(), 1);",
                       f"assert_eq({r(0)}.signum(), 0);",
                       f"assert_eq({r(-10)}.signum(), -1);"])
    if name == "is_negative":
        return doc("Returns `true` if `self` is negative and `false` if the number is zero or positive.",
                   ex=[f"assert({r(-10)}.is_negative());", f"assert(!{r(10)}.is_negative());"])
    if name == "is_positive":
        return doc("Returns `true` if `self` is positive and `false` if the number is zero or negative.",
                   ex=[f"assert({r(10)}.is_positive());", f"assert(!{r(-10)}.is_positive());"])
    if name == "div_euclid":
        if s:
            return doc("Calculates the quotient of Euclidean division of `self` by `rhs`.",
                       ["This computes the integer `q` such that `self = q * rhs + r`, with "
                        "`r = self.rem_euclid(rhs)` and `0 <= r < abs(rhs)`.",
                        "In other words, the result is `self / rhs` rounded to the integer `q` such that "
                        "`self >= q * rhs`. If `self > 0`, this is equal to rounding towards zero (what `/` does); "
                        "if `self < 0`, this is equal to rounding away from zero (towards +/- infinity). If "
                        "`rhs > 0`, this is equal to rounding towards -infinity; if `rhs < 0`, this is equal to "
                        "rounding towards +infinity."],
                       panics="This function will panic if `rhs` is zero or if `self` is `MIN` and `rhs` is -1.",
                       ex=[f"let a = {r(7)};", "let b = 4;", "",
                           "assert_eq(a.div_euclid(b), 1); // 7 >= 4 * 1",
                           "assert_eq(a.div_euclid(-b), -1); // 7 >= -4 * -1",
                           "assert_eq((-a).div_euclid(b), -2); // -7 >= 4 * -2",
                           "assert_eq((-a).div_euclid(-b), 2); // -7 >= -4 * 2"])
        return doc("Performs Euclidean division.",
                   "Since, for the positive integers, all common definitions of division are equal, this is exactly "
                   "equal to `self / rhs`.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(7)}.div_euclid(4), 1);"])
    if name == "rem_euclid":
        if s:
            return doc("Calculates the least nonnegative remainder of `self (mod rhs)`.",
                       "This is done as if by the Euclidean division algorithm -- given `r = self.rem_euclid(rhs)`, "
                       "the result satisfies `self = rhs * self.div_euclid(rhs) + r` and `0 <= r < abs(rhs)`.",
                       panics="This function will panic if `rhs` is zero or if `self` is `MIN` and `rhs` is -1.",
                       ex=[f"let a = {r(7)};", "let b = 4;", "",
                           "assert_eq(a.rem_euclid(b), 3);",
                           "assert_eq((-a).rem_euclid(b), 1);",
                           "assert_eq(a.rem_euclid(-b), 3);",
                           "assert_eq((-a).rem_euclid(-b), 1);"])
        return doc("Calculates the least remainder of `self (mod rhs)`.",
                   "Since, for the positive integers, all common definitions of division are equal, this is exactly "
                   "equal to `self % rhs`.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(7)}.rem_euclid(4), 3);"])
    if name == "div_ceil":
        return doc("Calculates the quotient of `self` and `rhs`, rounding the result towards positive infinity.",
                   panics="This function will panic if `rhs` is zero.",
                   ex=[f"assert_eq({r(7)}.div_ceil(4), 2);", f"assert_eq({r(8)}.div_ceil(4), 2);"])
    if name == "next_multiple_of":
        return doc("Calculates the smallest value greater than or equal to `self` that is a multiple of `rhs`.",
                   panics="This function will panic if `rhs` is zero, or if the result overflows (navi checks "
                          "integer overflow in every build; Rust only in debug builds).",
                   ex=[f"assert_eq({r(16)}.next_multiple_of(8), 16);",
                       f"assert_eq({r(23)}.next_multiple_of(8), 24);"])
    if name == "is_multiple_of":
        return doc("Returns `true` if `self` is an integer multiple of `rhs`, and false otherwise.",
                   "This function is equivalent to `self % rhs == 0`, except that it will not panic for `rhs == 0`. "
                   "Instead, `0.is_multiple_of(0) == true`, and for any non-zero `n`, `n.is_multiple_of(0) == false`.",
                   ex=[f"assert({r(6)}.is_multiple_of(2));",
                       f"assert(!{r(5)}.is_multiple_of(2));",
                       "",
                       f"assert({r(0)}.is_multiple_of(0));",
                       f"assert(!{r(6)}.is_multiple_of(0));"])
    if name == "is_power_of_two":
        if s:
            return doc("Returns `true` if and only if `self == 2^k` for some `k`.",
                       "Zero and the negative numbers are not powers of two. (Rust has this method on the unsigned "
                       "types only.)",
                       ex=[f"assert({r(16)}.is_power_of_two());",
                           f"assert(!{r(10)}.is_power_of_two());",
                           f"assert(!{r(0)}.is_power_of_two());",
                           f"assert(!{T}::MIN.is_power_of_two());"])
        return doc("Returns `true` if and only if `self == 2^k` for some `k`.",
                   ex=[f"assert({r(16)}.is_power_of_two());",
                       f"assert(!{r(10)}.is_power_of_two());",
                       f"assert(!{r(0)}.is_power_of_two());"])
    if name == "next_power_of_two":
        return doc("Returns the smallest power of two greater than or equal to `self`.",
                   panics=(f"Panics when the return value overflows (that is, `self > (1 << {b - 1})`), as Rust's "
                           "debug builds do (Rust's release builds return 0). Use `checked_next_power_of_two` when "
                           "it may not fit."),
                   ex=[f"assert_eq({r(2)}.next_power_of_two(), 2);",
                       f"assert_eq({r(3)}.next_power_of_two(), 4);",
                       f"assert_eq({r(0)}.next_power_of_two(), 1);"])

    # Logarithms and roots.
    nonpos = "less than or equal to zero" if s else "zero"
    if name == "ilog":
        return doc("Returns the logarithm of the number with respect to an arbitrary base, rounded down.",
                   "This method might not be optimized owing to implementation details; `ilog2` can produce results "
                   "more efficiently for base 2, and `ilog10` can produce results more efficiently for base 10.",
                   panics=f"This function will panic if `self` is {nonpos}, or if `base` is less than 2.",
                   ex=[f"assert_eq({r(5)}.ilog(5), 1);", f"assert_eq({r(100)}.ilog(3), 4);"])
    if name == "ilog2":
        return doc("Returns the base 2 logarithm of the number, rounded down.",
                   panics=f"This function will panic if `self` is {nonpos}.",
                   ex=[f"assert_eq({r(2)}.ilog2(), 1);", f"assert_eq({T}::MAX.ilog2(), {b - 2 if s else b - 1});"])
    if name == "ilog10":
        return doc("Returns the base 10 logarithm of the number, rounded down.",
                   panics=f"This function will panic if `self` is {nonpos}.",
                   ex=[f"assert_eq({r(10)}.ilog10(), 1);", f"assert_eq({r(99)}.ilog10(), 1);",
                       f"assert_eq({T}::MAX.ilog10(), {len(str(c.max)) - 1});"])
    if name == "isqrt":
        if s:
            return doc("Returns the square root of the number, rounded down.",
                       panics="This function will panic if `self` is negative.",
                       ex=[f"assert_eq({r(10)}.isqrt(), 3);", f"assert_eq({T}::MAX.isqrt(), {isqrt(c.max)});"])
        return doc("Returns the square root of the number, rounded down.",
                   ex=[f"assert_eq({r(10)}.isqrt(), 3);", f"assert_eq({T}::MAX.isqrt(), {isqrt(c.max)});"])

    # Bits.
    if name == "count_ones":
        return doc("Returns the number of ones in the binary representation of `self`.",
                   ex=[f"let n = {r('0b01001100')};", "assert_eq(n.count_ones(), 3);", "",
                       f"assert_eq({T}::MAX.count_ones(), {b - 1 if s else b});"] +
                      ([f"assert_eq({r(-1)}.count_ones(), {b});"] if s else []))
    if name == "count_zeros":
        return doc("Returns the number of zeros in the binary representation of `self`.",
                   ex=([f"assert_eq({T}::MAX.count_zeros(), 1);", f"assert_eq({r(-1)}.count_zeros(), 0);"] if s else
                       [f"assert_eq({r(0)}.count_zeros(), {b});", f"assert_eq({T}::MAX.count_zeros(), 0);"]))
    if name == "leading_zeros":
        return doc("Returns the number of leading zeros in the binary representation of `self`.",
                   "Depending on what you're doing with the value, you might also be interested in the `ilog2` "
                   "function which returns a consistent number, even if the type widens.",
                   ex=([f"assert_eq({r(-1)}.leading_zeros(), 0);", f"assert_eq({r(1)}.leading_zeros(), {b - 1});",
                        f"assert_eq({r(0)}.leading_zeros(), {b});"] if s else
                       [f"let n = {T}::MAX >> 2;", "assert_eq(n.leading_zeros(), 2);", "",
                        f"assert_eq({r(0)}.leading_zeros(), {b});", f"assert_eq({T}::MAX.leading_zeros(), 0);"]))
    if name == "trailing_zeros":
        return doc("Returns the number of trailing zeros in the binary representation of `self`.",
                   ex=([f"assert_eq({r(-4)}.trailing_zeros(), 2);", f"assert_eq({r(0)}.trailing_zeros(), {b});"] if s else
                       [f"let n = {r('0b0101000')};", "assert_eq(n.trailing_zeros(), 3);", "",
                        f"assert_eq({r(0)}.trailing_zeros(), {b});", f"assert_eq({T}::MAX.trailing_zeros(), 0);"]))
    if name == "leading_ones":
        return doc("Returns the number of leading ones in the binary representation of `self`.",
                   ex=([f"assert_eq({r(-1)}.leading_ones(), {b});", f"assert_eq({T}::MIN.leading_ones(), 1);",
                        f"assert_eq({r(1)}.leading_ones(), 0);"] if s else
                       [f"let n = !({T}::MAX >> 2);", "assert_eq(n.leading_ones(), 2);", "",
                        f"assert_eq({r(0)}.leading_ones(), 0);", f"assert_eq({T}::MAX.leading_ones(), {b});"]))
    if name == "trailing_ones":
        return doc("Returns the number of trailing ones in the binary representation of `self`.",
                   ex=([f"assert_eq({r(3)}.trailing_ones(), 2);", f"assert_eq({r(-1)}.trailing_ones(), {b});",
                        f"assert_eq({T}::MIN.trailing_ones(), 0);"] if s else
                       [f"let n = {r('0b1010111')};", "assert_eq(n.trailing_ones(), 3);", "",
                        f"assert_eq({r(0)}.trailing_ones(), 0);", f"assert_eq({T}::MAX.trailing_ones(), {b});"]))
    if name in ("rotate_left", "rotate_right"):
        rot = {8: 2, 16: 4, 32: 8, 64: 12, 128: 16}[b]
        op = {8: 0x82, 16: 0xa003, 32: 0x10000b3, 64: 0xaa00000000006e1,
              128: 0x13f40000000000000000000000004f76}[b]
        res = rotl(op, rot, b)
        if name == "rotate_right":
            op, res = res, op
        dirn = "left" if name == "rotate_left" else "right"
        what = ("wrapping the truncated bits to the end of the resulting integer" if dirn == "left" else
                "wrapping the truncated bits to the beginning of the resulting integer")
        opname = "<<" if dirn == "left" else ">>"
        return doc(f"Shifts the bits to the {dirn} by a specified amount, `n`, {what}.",
                   f"Please note this isn't the same operation as the `{opname}` shifting operator!",
                   ex=[f"let n = {c.hexv(op, True)};", f"let m = {c.hexv(res)};", "",
                       f"assert_eq(n.{name}({rot}), m);"])
    if name == "swap_bytes":
        v, _ = c.pattern()
        return doc("Reverses the byte order of the integer.",
                   ex=[f"let n = {c.hexv(v, True)};", "let m = n.swap_bytes();", "",
                       f"assert_eq(m, {c.hexv(swap(v, c.nbytes))});"])
    if name == "reverse_bits":
        v, _ = c.pattern()
        return doc("Reverses the order of bits in the integer. The least significant bit becomes the most "
                   "significant bit, second least-significant bit becomes second most-significant bit, etc.",
                   ex=[f"let n = {c.hexv(v, True)};", "let m = n.reverse_bits();", "",
                       f"assert_eq(m, {c.hexv(revbits(v, b))});",
                       f"assert_eq({r(0)}.reverse_bits(), 0);"])

    # Byte order.
    le = "navi's targets are all little endian"
    if name == "from_be":
        return doc("Converts an integer from big endian to the target's endianness.",
                   f"On big endian this is a no-op. On little endian the bytes are swapped ({le}).",
                   ex=[f"let n = {r('0x1A')};", "", f"assert_eq({T}::from_be(n), n.swap_bytes());"])
    if name == "from_le":
        return doc("Converts an integer from little endian to the target's endianness.",
                   f"On little endian this is a no-op ({le}). On big endian the bytes are swapped.",
                   ex=[f"let n = {r('0x1A')};", "", f"assert_eq({T}::from_le(n), n);"])
    if name == "to_be":
        return doc("Converts `self` to big endian from the target's endianness.",
                   f"On big endian this is a no-op. On little endian the bytes are swapped ({le}).",
                   ex=[f"let n = {r('0x1A')};", "", "assert_eq(n.to_be(), n.swap_bytes());"])
    if name == "to_le":
        return doc("Converts `self` to little endian from the target's endianness.",
                   f"On little endian this is a no-op ({le}). On big endian the bytes are swapped.",
                   ex=[f"let n = {r('0x1A')};", "", "assert_eq(n.to_le(), n);"])
    if name in ("to_be_bytes", "to_le_bytes", "to_ne_bytes"):
        v, bs = c.pattern()
        kind = name[3:5]
        if kind != "be":
            bs = bs[::-1]
        arr = byte_array(bs)
        summary = {"be": "Returns the memory representation of this integer as a byte array in big-endian "
                         "(network) byte order.",
                   "le": "Returns the memory representation of this integer as a byte array in little-endian "
                         "byte order.",
                   "ne": "Returns the memory representation of this integer as a byte array in native byte order."}[kind]
        details = None
        if kind == "ne":
            details = ("As the target platform's native endianness is used, portable code should use `to_be_bytes` "
                       f"or `to_le_bytes`, as appropriate, instead. ({le}, so this is `to_le_bytes`.)")
        ex = [f"let bytes = {c.hexv(v, True)}.{name}();"]
        if len(arr) == 1:
            ex.append(f"assert_eq(bytes, {arr[0]});")
        else:
            ex.append("assert_eq(")
            ex.append("    bytes,")
            ex += ["    " + l for l in arr[:-1]] + ["    " + arr[-1] + ","]
            ex.append(");")
        return doc(summary, details, ex=ex)
    if name in ("from_be_bytes", "from_le_bytes", "from_ne_bytes"):
        v, bs = c.pattern()
        kind = name[5:7]
        if kind != "be":
            bs = bs[::-1]
        arr = byte_array(bs)
        summary = {"be": "Creates an integer value from its representation as a byte array in big endian.",
                   "le": "Creates an integer value from its representation as a byte array in little endian.",
                   "ne": "Creates an integer value from its memory representation as a byte array in native "
                         "endianness."}[kind]
        details = None
        if kind == "ne":
            details = ("As the target platform's native endianness is used, portable code likely wants to use "
                       f"`from_be_bytes` or `from_le_bytes`, as appropriate instead. ({le}, so this is "
                       "`from_le_bytes`.)")
        if len(arr) == 1:
            ex = [f"let value = {T}::{name}({arr[0]});"]
        else:
            ex = [f"let value = {T}::{name}("] + ["    " + l for l in arr[:-1]] + ["    " + arr[-1] + ","] + [");"]
        ex.append(f"assert_eq(value, {c.hexv(v)});")
        return doc(summary, details, ex=ex)
    if name == "from_str_radix":
        details = [("The string is expected to be an optional `+` or `-` sign followed by only digits." if s else
                    "The string is expected to be an optional `+` sign followed by only digits.") +
                   " Leading and trailing non-digit characters (including whitespace) represent an error. "
                   "Underscores (which can appear in literals) also represent an error.",
                   "Digits are a subset of these characters, depending on `radix`: `0-9`, `a-z`, `A-Z`."]
        ex = [f'assert_eq({T}::from_str_radix("A", 16), Ok(10));']
        if s:
            ex.append(f'assert_eq({T}::from_str_radix("-1111", 2), Ok(-15));')
        else:
            ex.append(f'assert_eq({T}::from_str_radix("+1111", 2), Ok(15));')
        ex.append(f'assert({T}::from_str_radix("12 ", 10).is_err());')
        big = c.max + 1
        ex.append(f'assert({T}::from_str_radix("{big}", 10).is_err());')
        return doc("Parses an integer from a string slice with digits in the given base.", details,
                   panics="This function panics if `radix` is not in the range from 2 to 36.", ex=ex)

    # u64's bignum helpers.
    if name == "widening_mul":
        return doc("Calculates the complete product `self * rhs` without the possibility to overflow.",
                   "This returns the low-order (wrapping) bits and the high-order (overflow) bits of the result as "
                   "two separate values, in that order. (Rust's unstable `widening_mul`; one multiplication "
                   "instruction or two.)",
                   ex=["assert_eq(5u64.widening_mul(2), (10, 0));",
                       "assert_eq(u64::MAX.widening_mul(2), (u64::MAX - 1, 1));",
                       "assert_eq(u64::MAX.widening_mul(u64::MAX), (1, u64::MAX - 1));"])
    if name == "carrying_add":
        return doc("Calculates `self + rhs + carry` and returns a tuple containing the sum and the output carry.",
                   "Performs \"ternary addition\" of two integer operands and a carry-in bit, and returns an output "
                   "integer and a carry-out bit. This allows chaining together multiple additions to create a wider "
                   "addition, and can be useful for bignum addition. (Rust's unstable `carrying_add`; one "
                   "add-with-carry instruction.)",
                   ex=["assert_eq(5u64.carrying_add(2, false), (7, false));",
                       "assert_eq(5u64.carrying_add(2, true), (8, false));",
                       "assert_eq(u64::MAX.carrying_add(1, false), (0, true));",
                       "assert_eq(u64::MAX.carrying_add(u64::MAX, true), (u64::MAX, true));"])
    if name == "borrowing_sub":
        return doc("Calculates `self - rhs - borrow` and returns a tuple containing the difference and the output "
                   "borrow.",
                   "Performs \"ternary subtraction\" by subtracting both an integer operand and a borrow-in bit from "
                   "`self`, and returns an output integer and a borrow-out bit. This allows chaining together "
                   "multiple subtractions to create a wider subtraction, and can be useful for bignum subtraction. "
                   "(Rust's unstable `borrowing_sub`; one subtract-with-borrow instruction.)",
                   ex=["assert_eq(5u64.borrowing_sub(2, false), (3, false));",
                       "assert_eq(5u64.borrowing_sub(2, true), (2, false));",
                       "assert_eq(0u64.borrowing_sub(1, false), (u64::MAX, true));",
                       "assert_eq(0u64.borrowing_sub(1, true), (u64::MAX - 1, true));"])
    return None


def isqrt(n):
    x = int(n ** 0.5)
    while x * x > n:
        x -= 1
    while (x + 1) * (x + 1) <= n:
        x += 1
    return x


# --- Float templates --------------------------------------------------------

def float_doc(c, name):
    F = c.T
    f64 = F == "f64"
    tol = "1e-10" if f64 else "1e-6"
    consts = f"std::{F}::consts"
    via = ("" if f64 else " navi computes it in `f64` and rounds the result to `f32`.")
    L = lambda v: f"{v}{F}"

    def approx(expr, want, t=None):
        return [f"let abs_difference = ({expr} - {want}).abs();", "", f"assert(abs_difference < {t or tol});"]

    # Constants.
    if name == "RADIX":
        return doc(f"The radix or base of the internal representation of `{F}`.", ex=[f"assert_eq({F}::RADIX, 2);"])
    if name == "MANTISSA_DIGITS":
        return doc("Number of significant digits in base 2.",
                   ex=[f"assert_eq({F}::MANTISSA_DIGITS, {53 if f64 else 24});"])
    if name == "DIGITS":
        return doc("Approximate number of significant digits in base 10.",
                   f"This is the maximum *x* such that any decimal number with *x* significant digits can be "
                   f"converted to `{F}` and back without loss.",
                   ex=[f"assert_eq({F}::DIGITS, {15 if f64 else 6});"])
    if name == "EPSILON":
        return doc(f"[Machine epsilon] value for `{F}`.",
                   ["This is the difference between `1.0` and the next larger representable number.",
                    "Equal to 2<sup>1 − MANTISSA_DIGITS</sup>.".replace("<sup>1 − MANTISSA_DIGITS</sup>",
                                                                       "^(1 − MANTISSA_DIGITS)"),
                    "[Machine epsilon]: https://en.wikipedia.org/wiki/Machine_epsilon"],
                   ex=[f"assert_eq({F}::EPSILON, {L('2.0')}.powi({-52 if f64 else -23}));",
                       f"assert(1.0 + {F}::EPSILON > 1.0);",
                       f"assert_eq(1.0 + {F}::EPSILON / 2.0, 1.0);"])
    if name == "MIN":
        e = f"1.7976931348623157e308" if f64 else "3.40282347e38"
        return doc(f"Smallest finite `{F}` value.", "Equal to −`MAX`.",
                   ex=[f"assert_eq({F}::MIN, -{e});", f"assert_eq({F}::MIN, -{F}::MAX);"])
    if name == "MAX":
        e = f"1.7976931348623157e308" if f64 else "3.40282347e38"
        m = 53 if f64 else 24
        x = 1024 if f64 else 128
        return doc(f"Largest finite `{F}` value.", f"Equal to (1 − 2{sup(-m)}) 2{sup(x)}.",
                   ex=[f"assert_eq({F}::MAX, {e});", f"assert({F}::MAX * 2.0 == {F}::INFINITY);"])
    if name == "MIN_POSITIVE":
        e = "2.2250738585072014e-308" if f64 else "1.17549435e-38"
        return doc(f"Smallest positive normal `{F}` value.", "Equal to 2^(MIN_EXP − 1).",
                   ex=[f"assert_eq({F}::MIN_POSITIVE, {e});",
                       f"assert({F}::MIN_POSITIVE.is_normal());",
                       f"assert(({F}::MIN_POSITIVE / 2.0).is_subnormal());"])
    if name == "MIN_EXP":
        return doc("One greater than the minimum possible *normal* power of 2 exponent for a significand bounded by "
                   "1 ≤ x < 2 (i.e. the IEEE definition).",
                   "This corresponds to the exact minimum possible *normal* power of 2 exponent for a significand "
                   "bounded by 0.5 ≤ x < 1 (i.e. the C definition). In other words, all normal numbers "
                   "representable by this type are greater than or equal to 0.5 × 2^MIN_EXP.",
                   ex=[f"assert_eq({F}::MIN_EXP, {-1021 if f64 else -125});",
                       f"assert_eq({F}::MIN_POSITIVE, 0.5 * {L('2.0')}.powi({F}::MIN_EXP));"])
    if name == "MAX_EXP":
        return doc("One greater than the maximum possible power of 2 exponent for a significand bounded by "
                   "1 ≤ x < 2 (i.e. the IEEE definition).",
                   "This corresponds to the exact maximum possible power of 2 exponent for a significand bounded by "
                   "0.5 ≤ x < 1 (i.e. the C definition). In other words, all numbers representable by this type are "
                   "strictly less than 2^MAX_EXP.",
                   ex=[f"assert_eq({F}::MAX_EXP, {1024 if f64 else 128});",
                       f"assert({L('2.0')}.powi({F}::MAX_EXP) == {F}::INFINITY);"])
    if name == "MIN_10_EXP":
        return doc("Minimum *x* for which 10^*x* is normal.", "Equal to ceil(log₁₀ `MIN_POSITIVE`).",
                   ex=[f"assert_eq({F}::MIN_10_EXP, {-307 if f64 else -37});",
                       f"assert({L('10.0')}.powi({F}::MIN_10_EXP).is_normal());"])
    if name == "MAX_10_EXP":
        return doc("Maximum *x* for which 10^*x* is normal.", "Equal to floor(log₁₀ `MAX`).",
                   ex=[f"assert_eq({F}::MAX_10_EXP, {308 if f64 else 38});",
                       f"assert({L('10.0')}.powi({F}::MAX_10_EXP).is_normal());"])
    if name == "NAN":
        bits = "0x7ff8000000000000" if f64 else "0x7fc00000"
        return doc("Not a Number (NaN).",
                   ["Note that IEEE 754 doesn't define just a single NaN value; a plethora of bit patterns are "
                    "considered to be NaN. Furthermore, the standard makes a difference between a \"signaling\" and "
                    "a \"quiet\" NaN, and allows inspecting its \"payload\" (the unspecified bits in the bit "
                    "pattern). In navi this constant is the quiet NaN with a positive sign and no payload, "
                    f"`{bits}`; the NaNs that arithmetic produces may have other bit patterns.",
                    "A NaN compares unequal to everything, itself included: test for it with `is_nan`."],
                   ex=[f"let nan = {F}::NAN;", "", "assert(nan.is_nan());", "assert(nan != nan);",
                       f"assert_eq(nan.to_bits(), {bits});"])
    if name == "INFINITY":
        return doc("Infinity (∞).",
                   ex=[f"let inf = {F}::INFINITY;", "", "assert(inf.is_infinite());", f"assert(inf > {F}::MAX);",
                       f"assert_eq({L('1.0')} / 0.0, inf);"])
    if name == "NEG_INFINITY":
        return doc("Negative infinity (−∞).",
                   ex=[f"let neg_inf = {F}::NEG_INFINITY;", "", "assert(neg_inf.is_infinite());",
                       f"assert(neg_inf < {F}::MIN);", f"assert_eq({L('-1.0')} / 0.0, neg_inf);"])

    # Classification.
    if name == "is_nan":
        return doc("Returns `true` if this value is NaN.",
                   ex=[f"let nan = {F}::NAN;", f"let f = {L('7.0')};", "", "assert(nan.is_nan());",
                       "assert(!f.is_nan());"])
    if name == "is_infinite":
        return doc("Returns `true` if this value is positive infinity or negative infinity, and `false` otherwise.",
                   ex=[f"let f = {L('7.0')};", f"let inf = {F}::INFINITY;", f"let neg_inf = {F}::NEG_INFINITY;",
                       f"let nan = {F}::NAN;", "", "assert(!f.is_infinite());", "assert(!nan.is_infinite());", "",
                       "assert(inf.is_infinite());", "assert(neg_inf.is_infinite());"])
    if name == "is_finite":
        return doc("Returns `true` if this number is neither infinite nor NaN.",
                   ex=[f"let f = {L('7.0')};", f"let inf = {F}::INFINITY;", f"let neg_inf = {F}::NEG_INFINITY;",
                       f"let nan = {F}::NAN;", "", "assert(f.is_finite());", "", "assert(!nan.is_finite());",
                       "assert(!inf.is_finite());", "assert(!neg_inf.is_finite());"])
    sub_lit = "1.0e-308f64" if f64 else "1.0e-40f32"
    if name == "is_normal":
        return doc("Returns `true` if the number is neither zero, infinite, [subnormal], or NaN.",
                   "[subnormal]: https://en.wikipedia.org/wiki/Denormal_number",
                   ex=[f"let min = {F}::MIN_POSITIVE; // {'2.2250738585072014e-308f64' if f64 else '1.17549435e-38f32'}",
                       f"let max = {F}::MAX;", f"let lower_than_min = {sub_lit};", f"let zero = {L('0.0')};", "",
                       "assert(min.is_normal());", "assert(max.is_normal());", "", "assert(!zero.is_normal());",
                       f"assert(!{F}::NAN.is_normal());", f"assert(!{F}::INFINITY.is_normal());",
                       "// Values between `0` and `min` are subnormal.", "assert(!lower_than_min.is_normal());"])
    if name == "is_subnormal":
        return doc("Returns `true` if the number is [subnormal].",
                   "[subnormal]: https://en.wikipedia.org/wiki/Denormal_number",
                   ex=[f"let min = {F}::MIN_POSITIVE; // {'2.2250738585072014e-308f64' if f64 else '1.17549435e-38f32'}",
                       f"let max = {F}::MAX;", f"let lower_than_min = {sub_lit};", f"let zero = {L('0.0')};", "",
                       "assert(!min.is_subnormal());", "assert(!max.is_subnormal());", "",
                       "assert(!zero.is_subnormal());", f"assert(!{F}::NAN.is_subnormal());",
                       f"assert(!{F}::INFINITY.is_subnormal());", "// Values between `0` and `min` are subnormal.",
                       "assert(lower_than_min.is_subnormal());"])
    if name == "classify":
        return doc("Returns the floating point category of the number. If only one property is going to be tested, "
                   "it is generally faster to use the specific predicate instead.",
                   ex=["use std::num::FpCategory;", "", f"let num = {L('12.4')};", f"let inf = {F}::INFINITY;", "",
                       "assert_eq(num.classify(), FpCategory::Normal);",
                       "assert_eq(inf.classify(), FpCategory::Infinite);",
                       f"assert_eq({L('0.0')}.classify(), FpCategory::Zero);",
                       f"assert_eq({F}::NAN.classify(), FpCategory::Nan);",
                       f"assert_eq(({F}::MIN_POSITIVE / 2.0).classify(), FpCategory::Subnormal);"])
    if name == "is_sign_positive":
        return doc("Returns `true` if `self` has a positive sign, including `+0.0`, NaNs with positive sign bit and "
                   "positive infinity.",
                   "Note that IEEE 754 doesn't assign any meaning to the sign bit in case of a NaN, and the sign of a "
                   "NaN that arithmetic produces is not specified.",
                   ex=[f"let f = {L('7.0')};", f"let g = {L('-7.0')};", "", "assert(f.is_sign_positive());",
                       "assert(!g.is_sign_positive());", f"assert({L('0.0')}.is_sign_positive());"])
    if name == "is_sign_negative":
        return doc("Returns `true` if `self` has a negative sign, including `-0.0`, NaNs with negative sign bit and "
                   "negative infinity.",
                   "Note that IEEE 754 doesn't assign any meaning to the sign bit in case of a NaN, and the sign of a "
                   "NaN that arithmetic produces is not specified.",
                   ex=[f"let f = {L('7.0')};", f"let g = {L('-7.0')};", "", "assert(!f.is_sign_negative());",
                       "assert(g.is_sign_negative());", f"assert(({L('-0.0')}).is_sign_negative());"])

    # Rounding and the like.
    if name == "abs":
        return doc("Computes the absolute value of `self`.", "This function always returns the precise result.",
                   ex=[f"let x = {L('3.5')};", f"let y = {L('-3.5')};", "", "assert_eq(x.abs(), x);",
                       "assert_eq(y.abs(), -y);", "", f"assert({F}::NAN.abs().is_nan());"])
    if name == "floor":
        return doc("Returns the largest integer less than or equal to `self`.",
                   "This function always returns the precise result.",
                   ex=[f"let f = {L('3.7')};", f"let g = {L('3.0')};", f"let h = {L('-3.7')};", "",
                       "assert_eq(f.floor(), 3.0);", "assert_eq(g.floor(), 3.0);", "assert_eq(h.floor(), -4.0);"])
    if name == "ceil":
        return doc("Returns the smallest integer greater than or equal to `self`.",
                   "This function always returns the precise result.",
                   ex=[f"let f = {L('3.01')};", f"let g = {L('4.0')};", "", "assert_eq(f.ceil(), 4.0);",
                       "assert_eq(g.ceil(), 4.0);"])
    if name == "round":
        return doc("Returns the nearest integer to `self`. If a value is half-way between two integers, round away "
                   "from `0.0`.",
                   "This function always returns the precise result.",
                   ex=[f"let f = {L('3.3')};", f"let g = {L('-3.3')};", f"let h = {L('-3.7')};",
                       f"let i = {L('3.5')};", f"let j = {L('4.5')};", "", "assert_eq(f.round(), 3.0);",
                       "assert_eq(g.round(), -3.0);", "assert_eq(h.round(), -4.0);", "assert_eq(i.round(), 4.0);",
                       "assert_eq(j.round(), 5.0);"])
    if name == "round_ties_even":
        return doc("Returns the nearest integer to a number. Rounds half-way cases to the number with an even least "
                   "significant digit.",
                   "This function always returns the precise result.",
                   ex=[f"let f = {L('3.3')};", f"let g = {L('-3.3')};", f"let h = {L('3.5')};",
                       f"let i = {L('4.5')};", "", "assert_eq(f.round_ties_even(), 3.0);",
                       "assert_eq(g.round_ties_even(), -3.0);", "assert_eq(h.round_ties_even(), 4.0);",
                       "assert_eq(i.round_ties_even(), 4.0);"])
    if name == "trunc":
        return doc("Returns the integer part of `self`. This means that non-integer numbers are always truncated "
                   "towards zero.",
                   "This function always returns the precise result.",
                   ex=[f"let f = {L('3.7')};", f"let g = {L('3.0')};", f"let h = {L('-3.7')};", "",
                       "assert_eq(f.trunc(), 3.0);", "assert_eq(g.trunc(), 3.0);", "assert_eq(h.trunc(), -3.0);"])
    if name == "fract":
        t = "1e-10" if f64 else f"{F}::EPSILON"
        return doc("Returns the fractional part of `self`.",
                   "This function always returns the precise result.",
                   ex=[f"let x = {L('3.6')};", f"let y = {L('-3.6')};",
                       "let abs_difference_x = (x.fract() - 0.6).abs();",
                       "let abs_difference_y = (y.fract() - (-0.6)).abs();", "",
                       f"assert(abs_difference_x <= {t});", f"assert(abs_difference_y <= {t});"])
    if name == "mul_add":
        return doc("Fused multiply-add. Computes `(self * a) + b` with only one rounding error, yielding a more "
                   "accurate result than an unfused multiply-add." +
                   ("" if f64 else " navi computes it with `f64`'s and rounds the result to `f32`, which can round "
                                   "twice."),
                   ex=[f"let m = {L('10.0')};", f"let x = {L('4.0')};", f"let b = {L('60.0')};", "",
                       "assert_eq(m.mul_add(x, b), 100.0);", "assert_eq(m * x + b, 100.0);"])
    if name == "div_euclid":
        return doc("Calculates Euclidean division, the matching method for `rem_euclid`.",
                   "This computes the integer `n` such that `self = n * rhs + self.rem_euclid(rhs)`. In other "
                   "words, the result is `self / rhs` rounded to the integer `n` such that `self >= n * rhs`.",
                   ex=[f"let a = {L('7.0')};", "let b = 4.0;",
                       "assert_eq(a.div_euclid(b), 1.0); // 7.0 > 4.0 * 1.0",
                       "assert_eq((-a).div_euclid(b), -2.0); // -7.0 >= 4.0 * -2.0",
                       "assert_eq(a.div_euclid(-b), -1.0); // 7.0 >= -4.0 * -1.0",
                       "assert_eq((-a).div_euclid(-b), 2.0); // -7.0 >= -4.0 * 2.0"])
    if name == "rem_euclid":
        return doc("Calculates the least nonnegative remainder of `self (mod rhs)`.",
                   "In particular, the return value `r` satisfies `0.0 <= r < rhs.abs()` in most cases. However, due "
                   "to a floating point round-off error it can result in `r == rhs.abs()`, violating the "
                   "mathematical definition, if `self` is much smaller than `rhs.abs()` in magnitude and "
                   "`self < 0.0`. This result is not an element of the function's codomain, but it is the closest "
                   "floating point number in the real numbers and thus fulfills the property "
                   "`self == self.div_euclid(rhs) * rhs + self.rem_euclid(rhs)` approximately.",
                   ex=[f"let a = {L('7.0')};", "let b = 4.0;", "assert_eq(a.rem_euclid(b), 3.0);",
                       "assert_eq((-a).rem_euclid(b), 1.0);", "assert_eq(a.rem_euclid(-b), 3.0);",
                       "assert_eq((-a).rem_euclid(-b), 1.0);", "// limitation due to round-off error",
                       f"assert((-{F}::EPSILON).rem_euclid(3.0) != 0.0);"])
    if name == "powi":
        return doc("Raises a number to an integer power.",
                   "It is computed by repeated squaring, as Rust's (so the result is the same to the last bit).",
                   ex=[f"let x = {L('2.0')};", "let abs_difference = (x.powi(2) - (x * x)).abs();", "",
                       f"assert(abs_difference <= {F}::EPSILON);", "assert_eq(x.powi(-2), 0.25);",
                       f"assert_eq({F}::NAN.powi(0), 1.0);"])
    if name == "powf":
        return doc("Raises a number to a floating point power." + via,
                   ex=[f"let x = {L('2.0')};", "let abs_difference = (x.powf(2.0) - (x * x)).abs();", "",
                       f"assert(abs_difference <= {F}::EPSILON);", f"assert_eq({L('1.0')}.powf({F}::NAN), 1.0);"])
    if name == "sqrt":
        return doc("Returns the square root of a number.",
                   ["Returns NaN if `self` is a negative number other than `-0.0`.",
                    "The result is correctly rounded (as IEEE 754 requires)."],
                   ex=[f"let positive = {L('4.0')};", f"let negative = {L('-4.0')};",
                       f"let negative_zero = {L('-0.0')};", "", "assert_eq(positive.sqrt(), 2.0);",
                       "assert(negative.sqrt().is_nan());", "assert(negative_zero.sqrt() == negative_zero);"])
    if name == "exp":
        return doc("Returns `e^(self)`, (the exponential function)." + via,
                   ex=[f"let one = {L('1.0')};", "// e^1", "let e = one.exp();", "", "// ln(e) - 1 == 0"] +
                   approx("e.ln()", "1.0"))
    if name == "exp2":
        return doc("Returns `2^(self)`." + via, ex=[f"let f = {L('2.0')};", "", "// 2^2 - 4 == 0"] +
                   approx("f.exp2()", "4.0"))
    if name == "exp_m1":
        x, t = ("1e-16", "1e-20") if f64 else ("1e-8", "1e-10")
        return doc("Returns `e^(self) - 1` in a way that is accurate even if the number is close to zero." + via,
                   ex=[f"let x = {x}{F};", "", "// for very small x, e^x is approximately 1 + x + x^2 / 2",
                       "let approx = x + x * x / 2.0;"] + approx("x.exp_m1()", "approx", t))
    if name == "ln":
        return doc("Returns the natural logarithm of the number." + via,
                   "This returns NaN when the number is negative, and negative infinity when number is zero.",
                   ex=[f"let one = {L('1.0')};", "// e^1", "let e = one.exp();", "", "// ln(e) - 1 == 0"] +
                   approx("e.ln()", "1.0") + ["", f"assert_eq({L('0.0')}.ln(), {F}::NEG_INFINITY);",
                                              f"assert(({L('-1.0')}).ln().is_nan());"])
    if name == "ln_1p":
        x, t = ("1e-16", "1e-20") if f64 else ("1e-8", "1e-10")
        return doc("Returns `ln(1+n)` (natural logarithm) more accurately than if the operations were performed "
                   "separately." + via,
                   "This returns NaN when `n < -1.0`, and negative infinity when `n == -1.0`.",
                   ex=[f"let x = {x}{F};", "", "// for very small x, ln(1 + x) is approximately x - x^2 / 2",
                       "let approx = x - x * x / 2.0;"] + approx("x.ln_1p()", "approx", t))
    if name == "log":
        return doc("Returns the logarithm of the number with respect to an arbitrary base.",
                   ["The result might not be correctly rounded owing to implementation details; `self.log2()` can "
                    "produce more accurate results for base 2, and `self.log10()` can produce more accurate results "
                    "for base 10.",
                    "This returns NaN when the number is negative, and negative infinity when number is zero."],
                   ex=[f"let five = {L('5.0')};", "", "// log5(5) - 1 == 0"] + approx("five.log(5.0)", "1.0"))
    if name == "log2":
        return doc("Returns the base 2 logarithm of the number." + via,
                   "This returns NaN when the number is negative, and negative infinity when number is zero.",
                   ex=[f"let two = {L('2.0')};", "", "// log2(2) - 1 == 0"] + approx("two.log2()", "1.0"))
    if name == "log10":
        return doc("Returns the base 10 logarithm of the number." + via,
                   "This returns NaN when the number is negative, and negative infinity when number is zero.",
                   ex=[f"let ten = {L('10.0')};", "", "// log10(10) - 1 == 0"] + approx("ten.log10()", "1.0"))
    if name == "cbrt":
        return doc("Returns the cube root of a number." + via,
                   ex=[f"let x = {L('8.0')};", "", "// x^(1/3) - 2 == 0"] + approx("x.cbrt()", "2.0"))
    if name == "hypot":
        return doc("Compute the distance between the origin and a point (`x`, `y`) on the Euclidean plane. "
                   "Equivalently, compute the length of the hypotenuse of a right-angle triangle with other sides "
                   "having length `x.abs()` and `y.abs()`." + via,
                   ex=[f"let x = {L('2.0')};", f"let y = {L('3.0')};", "", "// sqrt(x^2 + y^2)"] +
                   approx("x.hypot(y)", "(x.powi(2) + y.powi(2)).sqrt()"))
    if name == "sin":
        return doc("Computes the sine of a number (in radians)." + via,
                   ex=[f"let x = {consts}::FRAC_PI_2;", ""] + approx("x.sin()", "1.0"))
    if name == "cos":
        return doc("Computes the cosine of a number (in radians)." + via,
                   ex=[f"let x = 2.0 * {consts}::PI;", ""] + approx("x.cos()", "1.0"))
    if name == "tan":
        return doc("Computes the tangent of a number (in radians)." + via,
                   ex=[f"let x = {consts}::FRAC_PI_4;"] + approx("x.tan()", "1.0", "1e-14" if f64 else None))
    if name == "asin":
        return doc("Computes the arcsine of a number. Return value is in radians in the range [-pi/2, pi/2] or NaN "
                   "if the number is outside the range [-1, 1]." + via,
                   ex=[f"let f = {consts}::FRAC_PI_2;", "", "// asin(sin(pi/2))"] +
                   approx("f.sin().asin()", f"{consts}::FRAC_PI_2"))
    if name == "acos":
        return doc("Computes the arccosine of a number. Return value is in radians in the range [0, pi] or NaN if "
                   "the number is outside the range [-1, 1]." + via,
                   ex=[f"let f = {consts}::FRAC_PI_4;", "", "// acos(cos(pi/4))"] +
                   approx("f.cos().acos()", f"{consts}::FRAC_PI_4"))
    if name == "atan":
        return doc("Computes the arctangent of a number. Return value is in radians in the range [-pi/2, pi/2]." + via,
                   ex=[f"let f = {L('1.0')};", "", "// atan(tan(1))"] + approx("f.tan().atan()", "1.0"))
    if name == "atan2":
        return doc("Computes the four quadrant arctangent of `self` (`y`) and `other` (`x`) in radians." + via,
                   "- `x = 0`, `y = 0`: `0`\n- `x >= 0`: `arctan(y/x)` -> `[-pi/2, pi/2]`\n"
                   "- `y >= 0`: `arctan(y/x) + pi` -> `(pi/2, pi]`\n- `y < 0`: `arctan(y/x) - pi` -> `(-pi, -pi/2)`",
                   ex=["// Positive angles measured counter-clockwise", "// from positive x axis",
                       "// -pi/4 radians (45 deg clockwise)", f"let x1 = {L('3.0')};", f"let y1 = {L('-3.0')};", "",
                       "// 3pi/4 radians (135 deg counter-clockwise)", f"let x2 = {L('-3.0')};",
                       f"let y2 = {L('3.0')};", "",
                       f"let abs_difference_1 = (y1.atan2(x1) - (-{consts}::FRAC_PI_4)).abs();",
                       f"let abs_difference_2 = (y2.atan2(x2) - (3.0 * {consts}::FRAC_PI_4)).abs();", "",
                       f"assert(abs_difference_1 < {tol});", f"assert(abs_difference_2 < {tol});"])
    if name == "sin_cos":
        return doc("Simultaneously computes the sine and cosine of the number, `x`. Returns `(sin(x), cos(x))`." + via,
                   ex=[f"let x = {consts}::FRAC_PI_4;", "let f = x.sin_cos();", "",
                       "let abs_difference_0 = (f.0 - x.sin()).abs();",
                       "let abs_difference_1 = (f.1 - x.cos()).abs();", "",
                       f"assert(abs_difference_0 < {tol});", f"assert(abs_difference_1 < {tol});"])
    if name == "sinh":
        return doc("Hyperbolic sine function." + via,
                   ex=[f"let e = {consts}::E;", f"let x = {L('1.0')};", "", "let f = x.sinh();",
                       "// Solving sinh() at 1 gives `(e^2-1)/(2e)`", "let g = ((e * e) - 1.0) / (2.0 * e);"] +
                   approx("f", "g"))
    if name == "cosh":
        return doc("Hyperbolic cosine function." + via,
                   ex=[f"let e = {consts}::E;", f"let x = {L('1.0')};", "let f = x.cosh();",
                       "// Solving cosh() at 1 gives this result", "let g = ((e * e) + 1.0) / (2.0 * e);"] +
                   approx("f", "g"))
    if name == "tanh":
        return doc("Hyperbolic tangent function." + via,
                   ex=[f"let e = {consts}::E;", f"let x = {L('1.0')};", "", "let f = x.tanh();",
                       "// Solving tanh() at 1 gives `(1 - e^(-2))/(1 + e^(-2))`",
                       "let g = (1.0 - e.powi(-2)) / (1.0 + e.powi(-2));"] + approx("f", "g"))
    if name == "asinh":
        return doc("Inverse hyperbolic sine function." + via,
                   ex=[f"let x = {L('1.0')};", "let f = x.sinh().asinh();", ""] + approx("f", "x"))
    if name == "acosh":
        return doc("Inverse hyperbolic cosine function." + via,
                   ex=[f"let x = {L('1.0')};", "let f = x.cosh().acosh();", ""] + approx("f", "x"))
    if name == "atanh":
        return doc("Inverse hyperbolic tangent function." + via,
                   ex=[f"let e = {consts}::E;", "let f = e.tanh().atanh();", ""] +
                   approx("f", "e", "1e-10" if f64 else "1e-5"))
    if name == "to_degrees":
        return doc("Converts radians to degrees.",
                   ex=[f"let angle = {consts}::PI;", ""] + approx("angle.to_degrees()", "180.0"))
    if name == "to_radians":
        return doc("Converts degrees to radians.",
                   ex=[f"let angle = {L('180.0')};", ""] + approx("angle.to_radians()", f"{consts}::PI"))
    if name in ("max", "min"):
        big = name == "max"
        return doc(f"Returns the {'maximum' if big else 'minimum'} of the two numbers, ignoring NaN.",
                   "If one of the arguments is NaN, then the other argument is returned.",
                   ex=[f"let x = {L('1.0')};", f"let y = {L('2.0')};", "",
                       f"assert_eq(x.{name}(y), {'y' if big else 'x'});", f"assert_eq(x.{name}({F}::NAN), x);",
                       f"assert_eq({F}::NAN.{name}(y), y);"])
    if name == "clamp":
        return doc("Restrict a value to a certain interval unless it is NaN.",
                   "Returns `max` if `self` is greater than `max`, and `min` if `self` is less than `min`. Otherwise "
                   "this returns `self`. Note that this function returns NaN if the initial value was NaN as well.",
                   panics="Panics if `min > max`, `min` is NaN, or `max` is NaN.",
                   ex=[f"assert(({L('-3.0')}).clamp(-2.0, 1.0) == -2.0);",
                       f"assert({L('0.0')}.clamp(-2.0, 1.0) == 0.0);",
                       f"assert({L('2.0')}.clamp(-2.0, 1.0) == 1.0);",
                       f"assert({F}::NAN.clamp(-2.0, 1.0).is_nan());"])
    if name == "copysign":
        return doc("Returns a number composed of the magnitude of `self` and the sign of `sign`.",
                   "Equal to `self` if the sign of `self` and `sign` are the same, otherwise equal to `-self`. If "
                   "`self` is a NaN, then a NaN with the same payload as `self` and the sign bit of `sign` is "
                   "returned.",
                   ex=[f"let f = {L('3.5')};", "", "assert_eq(f.copysign(0.42), 3.5);",
                       "assert_eq(f.copysign(-0.42), -3.5);", "assert_eq((-f).copysign(0.42), 3.5);",
                       "assert_eq((-f).copysign(-0.42), -3.5);", "", f"assert({F}::NAN.copysign(1.0).is_nan());"])
    if name == "signum":
        return doc("Returns a number that represents the sign of `self`.",
                   "- `1.0` if the number is positive, `+0.0` or `INFINITY`\n"
                   "- `-1.0` if the number is negative, `-0.0` or `NEG_INFINITY`\n- NaN if the number is NaN",
                   ex=[f"let f = {L('3.5')};", "", "assert_eq(f.signum(), 1.0);",
                       f"assert_eq({F}::NEG_INFINITY.signum(), -1.0);", "", f"assert({F}::NAN.signum().is_nan());"])
    if name == "recip":
        return doc("Takes the reciprocal (inverse) of a number, `1/x`.",
                   ex=[f"let x = {L('2.0')};", "let abs_difference = (x.recip() - (1.0 / x)).abs();", "",
                       f"assert(abs_difference < {tol});"])
    if name == "total_cmp":
        return doc("Returns the ordering between `self` and `other`.",
                   ["Unlike the standard partial comparison between floating point numbers, this comparison always "
                    "produces an ordering in accordance to the `totalOrder` predicate as defined in the IEEE 754 "
                    "(2008 revision) floating point standard. The values are ordered in the following sequence:",
                    "- negative quiet NaN\n- negative signaling NaN\n- negative infinity\n- negative numbers\n"
                    "- negative subnormal numbers\n- negative zero\n- positive zero\n- positive subnormal numbers\n"
                    "- positive numbers\n- positive infinity\n- positive signaling NaN\n- positive quiet NaN.",
                    f"The ordering established by this function does not always agree with the `PartialOrd` and "
                    f"`PartialEq` implementations of `{F}`. In particular, they regard negative and positive zero as "
                    "equal, while `total_cmp` doesn't."],
                   ex=["use std::cmp::Ordering;", "",
                       f"assert_eq(({L('-0.0')}).total_cmp(&0.0), Ordering::Less);",
                       f"assert_eq({L('1.0')}.total_cmp(&1.0), Ordering::Equal);",
                       f"assert_eq({F}::NAN.total_cmp(&{F}::INFINITY), Ordering::Greater);",
                       f"assert_eq((-{F}::NAN).total_cmp(&{F}::NEG_INFINITY), Ordering::Less);"])
    bits12 = 0x4029000000000000 if f64 else 0x41480000
    nb = 8 if f64 else 4
    if name == "to_bits":
        return doc(f"Returns the raw IEEE 754 bits of `self` as a `{c.U}`.",
                   "Note that this function is distinct from `as` casting, which attempts to preserve the *numeric* "
                   "value, and not the bitwise value.",
                   ex=[f"assert_ne({L('1.0')}.to_bits(), {L('1.0')} as {c.U}); // to_bits() is not casting!",
                       f"assert_eq({L('12.5')}.to_bits(), 0x{bits12:x});"])
    if name == "from_bits":
        return doc(f"Makes a `{F}` of the raw IEEE 754 bits `bits`.",
                   ["Note that this function is distinct from `as` casting, which attempts to preserve the *numeric* "
                    "value, and not the bitwise value.",
                    "It is the inverse of `to_bits`: `from_bits(x.to_bits())` is `x`, NaNs' bit patterns included."],
                   ex=[f"let v = {F}::from_bits(0x{bits12:x});", "assert_eq(v, 12.5);"])
    if name in ("to_be_bytes", "to_le_bytes", "to_ne_bytes"):
        kind = name[3:5]
        bs = [f"{(bits12 >> (8 * (nb - 1 - i))) & 255:02x}" for i in range(nb)]
        if kind != "be":
            bs = bs[::-1]
        summary = {"be": "Returns the memory representation of this floating point number as a byte array in "
                         "big-endian (network) byte order.",
                   "le": "Returns the memory representation of this floating point number as a byte array in "
                         "little-endian byte order.",
                   "ne": "Returns the memory representation of this floating point number as a byte array in "
                         "native byte order."}[kind]
        details = ["See `to_bits` for some discussion of the portability of this operation (there are almost no "
                   "issues)."]
        if kind == "ne":
            details.append("As the target platform's native endianness is used, portable code should use "
                           "`to_be_bytes` or `to_le_bytes`, as appropriate, instead. (navi's targets are all little "
                           "endian, so this is `to_le_bytes`.)")
        return doc(summary, details, ex=[f"let bytes = {L('12.5')}.{name}();",
                                         f"assert_eq(bytes, {byte_array(bs)[0]});"])
    if name in ("from_be_bytes", "from_le_bytes", "from_ne_bytes"):
        kind = name[5:7]
        bs = [f"{(bits12 >> (8 * (nb - 1 - i))) & 255:02x}" for i in range(nb)]
        if kind != "be":
            bs = bs[::-1]
        summary = {"be": "Creates a floating point value from its representation as a byte array in big endian.",
                   "le": "Creates a floating point value from its representation as a byte array in little endian.",
                   "ne": "Creates a floating point value from its representation as a byte array in native "
                         "endian."}[kind]
        details = ["See `from_bits` for some discussion of the portability of this operation (there are almost no "
                   "issues)."]
        if kind == "ne":
            details.append("As the target platform's native endianness is used, portable code likely wants to use "
                           "`from_be_bytes` or `from_le_bytes`, as appropriate instead. (navi's targets are all "
                           "little endian, so this is `from_le_bytes`.)")
        return doc(summary, details, ex=[f"let value = {F}::{name}({byte_array(bs)[0]});",
                                         "assert_eq(value, 12.5);"])
    return None


# --- Other items in ext.navi ------------------------------------------------

OTHER = {
    "pub struct TryFromIntError": doc(
        "The error type returned when a checked integral type conversion fails.",
        ex=["use std::convert::TryFrom;", "", "let x = u8::try_from(300i32);", "assert(x.is_err());",
            "assert_eq(u8::try_from(200i32), Ok(200));"]),
    "pub enum FpCategory": doc(
        "A classification of floating point numbers.",
        "This `enum` is used as the return type for `f32::classify` and `f64::classify`. See their documentation "
        "for more.",
        ex=["use std::num::FpCategory;", "", "let num = 12.4f32;", "let inf = f32::INFINITY;", "let zero = 0f32;",
            "let sub = 1.1754942e-38f32;", "let nan = f32::NAN;", "",
            "assert_eq(num.classify(), FpCategory::Normal);", "assert_eq(inf.classify(), FpCategory::Infinite);",
            "assert_eq(zero.classify(), FpCategory::Zero);", "assert_eq(sub.classify(), FpCategory::Subnormal);",
            "assert_eq(nan.classify(), FpCategory::Nan);"]),
    "    Nan,": "NaN (not a number): this value results from calculations like `(-1.0).sqrt()`.",
    "    Infinite,": "Positive or negative infinity, which often results from dividing a nonzero number by zero.",
    "    Zero,": "Positive or negative zero.",
    "    Subnormal,": ("“Subnormal” or “denormal” floating point representation (less precise, relative to their "
                      "magnitude, than `Normal`).\n\nSubnormal numbers are larger in magnitude than `Zero` but "
                      "smaller in magnitude than all `Normal` numbers."),
    "    Normal,": "A regular floating point number, not any of the exceptional categories.",
}


# --- Writing ----------------------------------------------------------------

# navi fmt's comment_width: whole lines of prose end by column 80.
WIDTH = 80


def render(md, indent):
    """The doc comment lines of markdown md, prose wrapped as navi fmt wraps
    comments (lines of at most WIDTH columns)."""
    out = []
    in_code = False
    para = []
    pre = indent + "///"
    width = WIDTH - len(pre) - 1

    def flush():
        if not para:
            return
        text = " ".join(para)
        para.clear()
        m = re.match(r"^(- )(.*)$", text)
        if m:
            lines = textwrap.wrap(m.group(2), width - 2, break_long_words=False, break_on_hyphens=False)
            out.append(f"{pre} - {lines[0]}")
            out.extend([f"{pre}   {l}" for l in lines[1:]])
            return
        for l in textwrap.wrap(text, width, break_long_words=False, break_on_hyphens=False):
            out.append(f"{pre} {l}")

    for line in md.split("\n"):
        if in_code:
            out.append(f"{pre} {line}" if line else pre)
            if line.startswith("```"):
                in_code = False
            continue
        if line.startswith("```"):
            flush()
            out.append(f"{pre} {line}")
            in_code = True
        elif line == "":
            flush()
            out.append(pre)
        elif line.startswith("# ") or line.startswith("[") and "]: " in line:
            flush()
            out.append(f"{pre} {line}")
        elif line.startswith("- "):
            flush()
            para.append(line)
        else:
            para.append(line)
    flush()
    return out


def process(path_in, path_out, stats):
    lines = open(path_in).read().split("\n")
    out = []
    cur = None
    for i, line in enumerate(lines):
        m = re.match(r"^impl (\w+) \{$", line)
        if m and m.group(1) in INTS + FLOATS:
            cur = m.group(1)
        elif line.startswith("}"):
            cur = None
        md = None
        indent = "    "
        m = re.match(r"^    pub (?:fn|const) (\w+)", line)
        if cur and m:
            name = m.group(1)
            c = C(cur)
            md = float_doc(c, name) if c.float else int_doc(c, name)
            if md is None:
                stats.setdefault("missing", []).append(f"{cur}::{name}")
        else:
            for k, v in OTHER.items():
                if line == k or line.startswith(k + " ") or line.startswith(k + "("):
                    md = v
                    indent = "    " if k.startswith("    ") else ""
        if md is None:
            out.append(line)
            continue
        # The item's attributes and docs above it: drop its old docs, put the
        # new ones above the attributes.
        j = len(out)
        while j > 0 and re.match(r"^\s*(#\[|///)", out[j - 1]):
            j -= 1
        head = [l for l in out[j:] if not re.match(r"^\s*///", l)]
        del out[j:]
        if cur and out and out[-1].strip() not in ("", "{") and not out[-1].endswith("{"):
            out.append("")
        out += render(md, indent)
        out += head
        out.append(line)
        stats["n"] = stats.get("n", 0) + 1
    open(path_out, "w").write("\n".join(out))


def main():
    if len(sys.argv) == 1:
        here = __import__("os").path.dirname(__import__("os").path.abspath(__file__))
        sys.argv.append(here + "/../lib/std/src")
    src = sys.argv[1].rstrip("/")
    dst = sys.argv[2].rstrip("/") if len(sys.argv) > 2 else src
    if src.endswith("/src"):
        # lib/std/src: num.navi and num/ext.navi in place.
        stats = {}
        process(f"{src}/num.navi", f"{dst}/num.navi", stats)
        process(f"{src}/num/ext.navi", f"{dst}/num/ext.navi", stats)
        print("documented:", stats.get("n"))
        for m in stats.get("missing", []):
            print("no template:", m)
        return
    stats = {}
    for f in ("num.navi", "ext.navi"):
        process(f"{src}/{f}", f"{dst}/{f}", stats)
    print("documented:", stats.get("n"))
    for m in stats.get("missing", []):
        print("no template:", m)


if __name__ == "__main__":
    main()
