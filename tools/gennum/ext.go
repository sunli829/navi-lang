package main

import (
	"fmt"
	"strings"
)

// The 128-bit integers (navic only: stage0 has none). Their arithmetic
// lowers to std::rt::int128.
const int128 = `
#[cfg(feature = "navic")]
impl u128 {
    pub const MIN: u128 = 0;
    pub const MAX: u128 = !0;
    pub const BITS: u32 = 128;

    pub fn overflowing_add(self, rhs: u128) -> (u128, bool) { crate::rt::int128::overflowing_add_u(self, rhs) }
    pub fn overflowing_sub(self, rhs: u128) -> (u128, bool) { crate::rt::int128::overflowing_sub_u(self, rhs) }
    pub fn overflowing_mul(self, rhs: u128) -> (u128, bool) { crate::rt::int128::overflowing_mul_u(self, rhs) }
    pub fn wrapping_add(self, rhs: u128) -> u128 { self.overflowing_add(rhs).0 }
    pub fn wrapping_sub(self, rhs: u128) -> u128 { self.overflowing_sub(rhs).0 }
    pub fn wrapping_mul(self, rhs: u128) -> u128 { self.overflowing_mul(rhs).0 }
    pub fn checked_add(self, rhs: u128) -> Option<u128> { let (r, o) = self.overflowing_add(rhs); if o { None } else { Some(r) } }
    pub fn checked_sub(self, rhs: u128) -> Option<u128> { let (r, o) = self.overflowing_sub(rhs); if o { None } else { Some(r) } }
    pub fn checked_mul(self, rhs: u128) -> Option<u128> { let (r, o) = self.overflowing_mul(rhs); if o { None } else { Some(r) } }
    pub fn checked_div(self, rhs: u128) -> Option<u128> { if rhs == 0 { None } else { Some(self / rhs) } }
    pub fn saturating_add(self, rhs: u128) -> u128 { match self.checked_add(rhs) { Some(r) => r, None => u128::MAX } }
    pub fn saturating_sub(self, rhs: u128) -> u128 { match self.checked_sub(rhs) { Some(r) => r, None => 0 } }
    pub fn pow(self, exp: u32) -> u128 {
        if exp == 0 { return 1; }
        let mut base = self;
        let mut acc: u128 = 1;
        let mut e = exp;
        loop {
            if e & 1 == 1 {
                acc = acc * base;
                if e == 1 { return acc; }
            }
            e = e / 2;
            base = base * base;
        }
    }
    pub fn count_ones(self) -> u32 { crate::rt::int128::count_ones(self) }
    pub fn leading_zeros(self) -> u32 { crate::rt::int128::leading_zeros(self) }
    pub fn trailing_zeros(self) -> u32 { crate::rt::int128::trailing_zeros(self) }
    pub fn is_power_of_two(self) -> bool { self.count_ones() == 1 }
}

#[cfg(feature = "navic")]
impl i128 {
    pub const MIN: i128 = 1 << 127;
    pub const MAX: i128 = !(1 << 127);
    pub const BITS: u32 = 128;

    pub fn overflowing_add(self, rhs: i128) -> (i128, bool) { crate::rt::int128::overflowing_add_i(self, rhs) }
    pub fn overflowing_sub(self, rhs: i128) -> (i128, bool) { crate::rt::int128::overflowing_sub_i(self, rhs) }
    pub fn overflowing_mul(self, rhs: i128) -> (i128, bool) { crate::rt::int128::overflowing_mul_i(self, rhs) }
    pub fn wrapping_add(self, rhs: i128) -> i128 { self.overflowing_add(rhs).0 }
    pub fn wrapping_sub(self, rhs: i128) -> i128 { self.overflowing_sub(rhs).0 }
    pub fn wrapping_mul(self, rhs: i128) -> i128 { self.overflowing_mul(rhs).0 }
    pub fn wrapping_neg(self) -> i128 { (0 as i128).wrapping_sub(self) }
    pub fn checked_add(self, rhs: i128) -> Option<i128> { let (r, o) = self.overflowing_add(rhs); if o { None } else { Some(r) } }
    pub fn checked_sub(self, rhs: i128) -> Option<i128> { let (r, o) = self.overflowing_sub(rhs); if o { None } else { Some(r) } }
    pub fn checked_mul(self, rhs: i128) -> Option<i128> { let (r, o) = self.overflowing_mul(rhs); if o { None } else { Some(r) } }
    pub fn checked_div(self, rhs: i128) -> Option<i128> { if rhs == 0 || self == i128::MIN && rhs == -1 { None } else { Some(self / rhs) } }
    pub fn saturating_add(self, rhs: i128) -> i128 {
        match self.checked_add(rhs) { Some(r) => r, None => if rhs < 0 { i128::MIN } else { i128::MAX } }
    }
    pub fn saturating_sub(self, rhs: i128) -> i128 {
        match self.checked_sub(rhs) { Some(r) => r, None => if rhs > 0 { i128::MIN } else { i128::MAX } }
    }
    pub fn pow(self, exp: u32) -> i128 {
        if exp == 0 { return 1; }
        let mut base = self;
        let mut acc: i128 = 1;
        let mut e = exp;
        loop {
            if e & 1 == 1 {
                acc = acc * base;
                if e == 1 { return acc; }
            }
            e = e / 2;
            base = base * base;
        }
    }
    pub fn abs(self) -> i128 { if self < 0 { -self } else { self } }
    pub fn signum(self) -> i128 { if self < 0 { -1 } else if self > 0 { 1 } else { 0 } }
    pub fn is_negative(self) -> bool { self < 0 }
    pub fn is_positive(self) -> bool { self > 0 }
    pub fn count_ones(self) -> u32 { crate::rt::int128::count_ones(self as u128) }
    pub fn leading_zeros(self) -> u32 { crate::rt::int128::leading_zeros(self as u128) }
    pub fn trailing_zeros(self) -> u32 { crate::rt::int128::trailing_zeros(self as u128) }
}
`

var allInts = append(append([]intType{}, ints...), intType{"u128", 128, false}, intType{"i128", 128, true})

func unsignedOf(t intType) string {
	if t.signed {
		return "u" + t.name[1:]
	}
	return t.name
}

func intByName(n string) intType {
	for _, t := range allInts {
		if t.name == n {
			return t
		}
	}
	panic(n)
}

// The methods every integer type gets in num/ext.navi.
const extCommon = `
    pub fn checked_neg(self) -> Option<$T> { let (r, o) = self.overflowing_neg(); if o { None } else { Some(r) } }
    pub fn checked_pow(self, exp: u32) -> Option<$T> {
        if exp == 0 { return Some(1); }
        let mut base = self;
        let mut acc: $T = 1;
        let mut e = exp;
        loop {
            if e & 1 == 1 {
                acc = acc.checked_mul(base)?;
                if e == 1 { return Some(acc); }
            }
            e = e / 2;
            base = base.checked_mul(base)?;
        }
    }
    pub fn overflowing_pow(self, exp: u32) -> ($T, bool) {
        if exp == 0 { return (1, false); }
        let mut base = self;
        let mut acc: $T = 1;
        let mut e = exp;
        let mut over = false;
        loop {
            if e & 1 == 1 {
                let (r, o) = acc.overflowing_mul(base);
                acc = r;
                over = over || o;
                if e == 1 { return (acc, over); }
            }
            e = e / 2;
            let (b, o) = base.overflowing_mul(base);
            base = b;
            over = over || o;
        }
    }
    pub fn saturating_pow(self, exp: u32) -> $T {
        match self.checked_pow(exp) { Some(r) => r, None => if $NEG_ODD { $T::MIN } else { $T::MAX } }
    }
    pub fn checked_shl(self, rhs: u32) -> Option<$T> { if rhs < $BITS { Some(self << rhs) } else { None } }
    pub fn checked_shr(self, rhs: u32) -> Option<$T> { if rhs < $BITS { Some(self >> rhs) } else { None } }
    pub fn wrapping_shl(self, rhs: u32) -> $T { self << (rhs % $BITS) }
    pub fn wrapping_shr(self, rhs: u32) -> $T { self >> (rhs % $BITS) }
    pub fn overflowing_shl(self, rhs: u32) -> ($T, bool) { (self.wrapping_shl(rhs), rhs >= $BITS) }
    pub fn overflowing_shr(self, rhs: u32) -> ($T, bool) { (self.wrapping_shr(rhs), rhs >= $BITS) }
    pub fn wrapping_div(self, rhs: $T) -> $T { if $DIV_OVER { self } else { self / rhs } }
    pub fn wrapping_rem(self, rhs: $T) -> $T { if $DIV_OVER { 0 } else { self % rhs } }
    pub fn overflowing_div(self, rhs: $T) -> ($T, bool) { if $DIV_OVER { (self, true) } else { (self / rhs, false) } }
    pub fn overflowing_rem(self, rhs: $T) -> ($T, bool) { if $DIV_OVER { (0, true) } else { (self % rhs, false) } }
    pub fn saturating_div(self, rhs: $T) -> $T { if $DIV_OVER { $T::MAX } else { self / rhs } }
    pub fn checked_div_euclid(self, rhs: $T) -> Option<$T> { if rhs == 0 || $DIV_OVER { None } else { Some(self.div_euclid(rhs)) } }
    pub fn checked_rem_euclid(self, rhs: $T) -> Option<$T> { if rhs == 0 || $DIV_OVER { None } else { Some(self.rem_euclid(rhs)) } }
    pub fn wrapping_div_euclid(self, rhs: $T) -> $T { if $DIV_OVER { self } else { self.div_euclid(rhs) } }
    pub fn wrapping_rem_euclid(self, rhs: $T) -> $T { if $DIV_OVER { 0 } else { self.rem_euclid(rhs) } }
    pub fn reverse_bits(self) -> $T {
        let v = $SELFU;
        let mut r: $U = 0;
        let mut i = 0u32;
        while i < $BITS { r = (r << 1) | ((v >> i) & 1); i += 1; }
        $RESULTU
    }
    pub fn to_be(self) -> $T { self.swap_bytes() }
    pub fn to_le(self) -> $T { self }
    pub fn from_be(x: $T) -> $T { x.swap_bytes() }
    pub fn from_le(x: $T) -> $T { x }
    pub fn to_be_bytes(self) -> [u8; $BYTES] { self.swap_bytes().to_le_bytes() }
    pub fn from_be_bytes(bytes: [u8; $BYTES]) -> $T { $T::from_le_bytes(bytes).swap_bytes() }
    pub fn to_ne_bytes(self) -> [u8; $BYTES] { self.to_le_bytes() }
    pub fn from_ne_bytes(bytes: [u8; $BYTES]) -> $T { $T::from_le_bytes(bytes) }
    pub fn leading_ones(self) -> u32 { (!self).leading_zeros() }
    pub fn trailing_ones(self) -> u32 { (!self).trailing_zeros() }
    #[track_caller]
    pub fn ilog2(self) -> u32 {
        if $NONPOS { panic("argument of integer logarithm must be positive"); }
        $BITS - 1 - self.leading_zeros()
    }
    pub fn checked_ilog2(self) -> Option<u32> { if $NONPOS { None } else { Some($BITS - 1 - self.leading_zeros()) } }
    #[track_caller]
    pub fn ilog10(self) -> u32 {
        if $NONPOS { panic("argument of integer logarithm must be positive"); }
        let mut n = 0u32;
        let mut v = self;
        while v >= 10 { v = v / 10; n += 1; }
        n
    }
    pub fn checked_ilog10(self) -> Option<u32> { if $NONPOS { None } else { Some(self.ilog10()) } }
    #[track_caller]
    pub fn ilog(self, base: $T) -> u32 {
        if $NONPOS { panic("argument of integer logarithm must be positive"); }
        if base < 2 { panic("base of integer logarithm must be at least 2"); }
        let mut n = 0u32;
        let mut v = self;
        while v >= base { v = v / base; n += 1; }
        n
    }
    pub fn checked_ilog(self, base: $T) -> Option<u32> { if $NONPOS || base < 2 { None } else { Some(self.ilog(base)) } }
    #[track_caller]
    pub fn isqrt(self) -> $T {
        $ISQRT_NEG
        if self < 2 { return self; }
        let mut x = (self as f64).sqrt() as $T;
        while x > self / x { x -= 1; }
        while x + 1 <= self / (x + 1) { x += 1; }
        x
    }
`

const extSigned = `
    pub fn overflowing_neg(self) -> ($T, bool) { if self == $T::MIN { (self, true) } else { (-self, false) } }
    pub fn checked_abs(self) -> Option<$T> { if self == $T::MIN { None } else { Some(self.abs()) } }
    pub fn overflowing_abs(self) -> ($T, bool) { (self.wrapping_abs(), self == $T::MIN) }
    pub fn saturating_abs(self) -> $T { if self == $T::MIN { $T::MAX } else { self.abs() } }
    pub fn saturating_neg(self) -> $T { if self == $T::MIN { $T::MAX } else { -self } }
    pub fn abs_diff(self, other: $T) -> $U {
        if self < other { (other as $U).wrapping_sub(self as $U) } else { (self as $U).wrapping_sub(other as $U) }
    }
    #[track_caller]
    pub fn div_euclid(self, rhs: $T) -> $T {
        let q = self / rhs;
        if self % rhs < 0 { if rhs > 0 { q - 1 } else { q + 1 } } else { q }
    }
    #[track_caller]
    pub fn rem_euclid(self, rhs: $T) -> $T {
        let r = self % rhs;
        if r < 0 { r.wrapping_add(rhs.wrapping_abs()) } else { r }
    }
    pub fn checked_isqrt(self) -> Option<$T> { if self < 0 { None } else { Some(self.isqrt()) } }
`

const extUnsigned = `
    pub fn overflowing_neg(self) -> ($T, bool) { (self.wrapping_neg(), self != 0) }
    #[track_caller]
    pub fn div_euclid(self, rhs: $T) -> $T { self / rhs }
    #[track_caller]
    pub fn rem_euclid(self, rhs: $T) -> $T { self % rhs }
    pub fn checked_next_power_of_two(self) -> Option<$T> {
        if self <= 1 { return Some(1); }
        let p = $BITS - (self - 1).leading_zeros();
        if p >= $BITS { None } else { Some((1 as $T) << p) }
    }
    #[track_caller]
    pub fn div_ceil(self, rhs: $T) -> $T {
        let q = self / rhs;
        if self % rhs > 0 { q + 1 } else { q }
    }
    #[track_caller]
    pub fn next_multiple_of(self, rhs: $T) -> $T {
        let r = self % rhs;
        if r == 0 { self } else { self + (rhs - r) }
    }
    pub fn checked_next_multiple_of(self, rhs: $T) -> Option<$T> {
        if rhs == 0 { return None; }
        let r = self % rhs;
        if r == 0 { Some(self) } else { self.checked_add(rhs - r) }
    }
    pub fn is_multiple_of(self, rhs: $T) -> bool { if rhs == 0 { self == 0 } else { self % rhs == 0 } }
`

// What num.navi has for the 64-bit and narrower integers but not for the
// 128-bit ones.
const ext128 = `
    pub fn checked_rem(self, rhs: $T) -> Option<$T> { if rhs == 0 || $DIV_OVER { None } else { Some(self % rhs) } }
    pub fn saturating_mul(self, rhs: $T) -> $T {
        match self.checked_mul(rhs) { Some(r) => r, None => if $MUL_MIN { $T::MIN } else { $T::MAX } }
    }
    pub fn wrapping_pow(self, exp: u32) -> $T { self.overflowing_pow(exp).0 }
    pub fn count_zeros(self) -> u32 { 128 - self.count_ones() }
    pub fn rotate_left(self, n: u32) -> $T {
        let v = $SELF128;
        let k = n % 128;
        if k == 0 { return self; }
        $FROMU((v << k) | (v >> (128 - k)))
    }
    pub fn rotate_right(self, n: u32) -> $T {
        let k = n % 128;
        if k == 0 { return self; }
        self.rotate_left(128 - k)
    }
    pub fn to_le_bytes(self) -> [u8; 16] {
        let mut out = [0u8; 16];
        let lo = $SELF128 as u64;
        let hi = ($SELF128 >> 64) as u64;
        let mut i = 0;
        while i < 8 {
            out[i] = ((lo >> (8 * i as u32)) & 255) as u8;
            out[i + 8] = ((hi >> (8 * i as u32)) & 255) as u8;
            i += 1;
        }
        out
    }
    pub fn from_le_bytes(bytes: [u8; 16]) -> $T {
        let mut lo: u64 = 0;
        let mut hi: u64 = 0;
        let mut i = 8;
        while i > 0 {
            i -= 1;
            lo = (lo << 8) | bytes[i] as u64;
            hi = (hi << 8) | bytes[i + 8] as u64;
        }
        $FROMU(((hi as u128) << 64) | lo as u128)
    }
    pub fn swap_bytes(self) -> $T {
        let lo = $SELF128 as u64;
        let hi = ($SELF128 >> 64) as u64;
        $FROMU(((lo.swap_bytes() as u128) << 64) | hi.swap_bytes() as u128)
    }
    pub fn from_str_radix(src: &str, radix: u32) -> Result<$T, ParseIntError> {
        match parse_radix128(src, radix, $SIGNED) {
            Some((neg, mag)) => {
                let max = $MAX128;
                if neg {
                    if mag > max + 1 { return Err(ParseIntError); }
                    Ok($FROMU(0u128.wrapping_sub(mag)))
                } else {
                    if mag > max { return Err(ParseIntError); }
                    Ok($FROMU(mag))
                }
            }
            None => Err(ParseIntError),
        }
    }
`

const ext128Signed = `
    pub fn wrapping_abs(self) -> $T { if self < 0 { self.wrapping_neg() } else { self } }
    pub fn unsigned_abs(self) -> u128 { if self < 0 { (self as u128).wrapping_neg() } else { self as u128 } }
`

const ext128Unsigned = `
    pub fn wrapping_neg(self) -> $T { (0 as u128).wrapping_sub(self) }
    pub fn next_power_of_two(self) -> $T { match self.checked_next_power_of_two() { Some(p) => p, None => 0 } }
    pub fn abs_diff(self, other: $T) -> $T { if self > other { self - other } else { other - self } }
`

// swap_bytes for the narrower integers.
const extSwap = `
    pub fn swap_bytes(self) -> $T {
        let v = self as $U;
        let mut r: $U = 0;
        let mut i = 0u32;
        while i < $BYTES { r = (r << 8) | ((v >> (8 * i)) & 255); i += 1; }
        r as $T
    }
`

func extFor(t intType) string {
	signed := t.signed
	bits := t.bits
	r := strings.NewReplacer(
		"$T", t.name, "$U", unsignedOf(t), "$BITS", fmt.Sprint(bits), "$BYTES", fmt.Sprint(bits/8),
		"$SIGNED", boolStr(signed),
		"$NEG_ODD", map[bool]string{true: "self < 0 && exp % 2 == 1", false: "false"}[signed],
		"$DIV_OVER", map[bool]string{true: fmt.Sprintf("self == %s::MIN && rhs == -1", t.name), false: "false"}[signed],
		"$MUL_MIN", map[bool]string{true: "(self < 0) != (rhs < 0)", false: "false"}[signed],
		"$SELFU", map[bool]string{true: "self as " + unsignedOf(t), false: "self"}[signed],
		"$RESULTU", map[bool]string{true: "r as " + t.name, false: "r"}[signed],
		"$SELF128", map[bool]string{true: "(self as u128)", false: "self"}[signed],
		"$FROMU(", map[bool]string{true: "i128_of(", false: "("}[signed],
		"$MAX128", map[bool]string{true: "i128::MAX as u128", false: "u128::MAX"}[signed],
		"$NONPOS", map[bool]string{true: "self <= 0", false: "self == 0"}[signed],
		"$ISQRT_NEG", map[bool]string{true: `if self < 0 { panic("argument of integer square root cannot be negative"); }`, false: ""}[signed],
	)
	var b strings.Builder
	fmt.Fprintf(&b, "\nimpl %s {", t.name)
	b.WriteString(r.Replace(extCommon))
	if signed {
		b.WriteString(r.Replace(extSigned))
	} else {
		b.WriteString(r.Replace(extUnsigned))
	}
	if bits == 128 {
		b.WriteString(r.Replace(ext128))
		if signed {
			b.WriteString(r.Replace(ext128Signed))
		} else {
			b.WriteString(r.Replace(ext128Unsigned))
		}
	} else if bits == 8 {
		b.WriteString(r.Replace("\n    pub fn swap_bytes(self) -> $T { self }\n"))
	} else {
		b.WriteString(r.Replace(extSwap))
	}
	b.WriteString("}\n")
	// Remove the empty line left by an empty $ISQRT_NEG.
	return strings.ReplaceAll(b.String(), "\n        \n", "\n")
}

// lossless reports whether Rust has From<from> for to.
func lossless(from, to intType) bool {
	if from.name == to.name {
		return false
	}
	if from.name == "usize" || from.name == "isize" {
		return false
	}
	switch to.name {
	case "usize":
		return from.name == "u8" || from.name == "u16"
	case "isize":
		return from.name == "u8" || from.name == "i8" || from.name == "i16"
	}
	if from.signed && !to.signed {
		return false
	}
	if from.signed == to.signed {
		return to.bits > from.bits
	}
	return to.bits > from.bits // unsigned to wider signed
}

// tryCheck is the condition under which value (a from) does not fit in to.
func tryCheck(from, to intType) string {
	f, t := from.name, to.name
	switch {
	case from.bits == 128 || to.bits == 128:
		if from.name == "u128" {
			return fmt.Sprintf("value > %s::MAX as u128", t)
		}
		if from.name == "i128" {
			if to.signed {
				return fmt.Sprintf("value < %s::MIN as i128 || value > %s::MAX as i128", t, t)
			}
			if to.name == "u128" {
				return "value < 0"
			}
			return fmt.Sprintf("value < 0 || value > %s::MAX as i128", t)
		}
		// From a narrower integer to u128 or i128.
		if to.name == "i128" {
			return "false"
		}
		return "value < 0"
	case !from.signed:
		return fmt.Sprintf("value as u64 > %s::MAX as u64", t)
	case to.signed:
		return fmt.Sprintf("(value as i64) < %s::MIN as i64 || value as i64 > %s::MAX as i64", t, t)
	default:
		_ = f
		return fmt.Sprintf("value < 0 || value as u64 > %s::MAX as u64", t)
	}
}

func conversions() string {
	var b strings.Builder
	b.WriteString("\n// Conversions between the integer types (Rust has them in core::convert::num).\n")
	for _, from := range allInts {
		for _, to := range allInts {
			if from.name == to.name {
				continue
			}
			if lossless(from, to) {
				fmt.Fprintf(&b, "impl From<%[1]s> for %[2]s { fn from(value: %[1]s) -> %[2]s { value as %[2]s } }\n", from.name, to.name)
				fmt.Fprintf(&b, "impl TryFrom<%[1]s> for %[2]s { type Error = Infallible; fn try_from(value: %[1]s) -> Result<%[2]s, Infallible> { Ok(value as %[2]s) } }\n", from.name, to.name)
			} else {
				cond := tryCheck(from, to)
				if !from.signed && strings.HasPrefix(cond, "value < 0") {
					// An unsigned source is never negative.
					cond = strings.TrimPrefix(strings.TrimPrefix(cond, "value < 0"), " || ")
				}
				if cond == "" {
					cond = "false"
				}
				fmt.Fprintf(&b, "impl TryFrom<%[1]s> for %[2]s {\n    type Error = TryFromIntError;\n    fn try_from(value: %[1]s) -> Result<%[2]s, TryFromIntError> { if %[3]s { Err(TryFromIntError(())) } else { Ok(value as %[2]s) } }\n}\n", from.name, to.name, cond)
			}
		}
	}
	for _, to := range allInts {
		fmt.Fprintf(&b, "impl From<bool> for %[1]s { fn from(value: bool) -> %[1]s { if value { 1 } else { 0 } } }\n", to.name)
	}
	for _, f := range []string{"i8", "i16", "i32", "u8", "u16", "u32"} {
		fmt.Fprintf(&b, "impl From<%[1]s> for f64 { fn from(value: %[1]s) -> f64 { value as f64 } }\n", f)
	}
	for _, f := range []string{"i8", "i16", "u8", "u16"} {
		fmt.Fprintf(&b, "impl From<%[1]s> for f32 { fn from(value: %[1]s) -> f32 { value as f32 } }\n", f)
	}
	b.WriteString("impl From<f32> for f64 { fn from(value: f32) -> f64 { value as f64 } }\n")
	b.WriteString("impl From<bool> for f64 { fn from(value: bool) -> f64 { if value { 1.0 } else { 0.0 } } }\n")
	b.WriteString("impl From<bool> for f32 { fn from(value: bool) -> f32 { if value { 1.0 } else { 0.0 } } }\n")
	return b.String()
}

// Float methods: $F is the type, $UB its bits type; the libm functions
// take and return f64.
const extFloat = `
impl $F {
    pub fn is_sign_positive(self) -> bool { !self.is_sign_negative() }
    pub fn is_normal(self) -> bool { self.classify() == FpCategory::Normal }
    pub fn is_subnormal(self) -> bool { self.classify() == FpCategory::Subnormal }
    pub fn classify(self) -> FpCategory {
        let bits = self.to_bits();
        let exp = bits & $EXP_MASK;
        let man = bits & $MAN_MASK;
        if exp == 0 {
            if man == 0 { FpCategory::Zero } else { FpCategory::Subnormal }
        } else if exp == $EXP_MASK {
            if man == 0 { FpCategory::Infinite } else { FpCategory::Nan }
        } else {
            FpCategory::Normal
        }
    }
    pub fn fract(self) -> $F { self - self.trunc() }
    pub fn signum(self) -> $F { if self.is_nan() { $F::NAN } else { (1.0 as $F).copysign(self) } }
    pub fn copysign(self, sign: $F) -> $F { $F::from_bits((self.to_bits() & !$SIGN) | (sign.to_bits() & $SIGN)) }
    pub fn recip(self) -> $F { 1.0 / self }
    pub fn to_degrees(self) -> $F { self * (180.0 / crate::$F::consts::PI) }
    pub fn to_radians(self) -> $F { self * (crate::$F::consts::PI / 180.0) }
    #[track_caller]
    pub fn clamp(self, min: $F, max: $F) -> $F {
        if !(min <= max) { panic(` + "`min > max, or either was NaN. min = ${min:?}, max = ${max:?}`" + `); }
        let mut x = self;
        if x < min { x = min; }
        if x > max { x = max; }
        x
    }
    /// The IEEE 754 totalOrder of self and other (-NaN < -inf < ... <
    /// -0 < +0 < ... < inf < NaN).
    pub fn total_cmp(&self, other: &$F) -> crate::cmp::Ordering {
        let mut a = self.to_bits() as $I;
        let mut b = other.to_bits() as $I;
        a = a ^ ((((a >> ($BITS - 1)) as $UB) >> 1) as $I);
        b = b ^ ((((b >> ($BITS - 1)) as $UB) >> 1) as $I);
        a.cmp(&b)
    }
    pub fn mul_add(self, a: $F, b: $F) -> $F { unsafe { c_fma(self as f64, a as f64, b as f64) as $F } }
    pub fn div_euclid(self, rhs: $F) -> $F {
        let q = (self / rhs).trunc();
        if self % rhs < 0.0 { if rhs > 0.0 { q - 1.0 } else { q + 1.0 } } else { q }
    }
    pub fn rem_euclid(self, rhs: $F) -> $F {
        let r = self % rhs;
        if r < 0.0 { r + rhs.abs() } else { r }
    }
    pub fn powf(self, n: $F) -> $F { unsafe { c_pow(self as f64, n as f64) as $F } }
    pub fn exp(self) -> $F { unsafe { c_exp(self as f64) as $F } }
    pub fn exp2(self) -> $F { unsafe { c_exp2(self as f64) as $F } }
    pub fn exp_m1(self) -> $F { unsafe { c_expm1(self as f64) as $F } }
    pub fn ln(self) -> $F { unsafe { c_log(self as f64) as $F } }
    pub fn ln_1p(self) -> $F { unsafe { c_log1p(self as f64) as $F } }
    pub fn log(self, base: $F) -> $F { self.ln() / base.ln() }
    pub fn log2(self) -> $F { unsafe { c_log2(self as f64) as $F } }
    pub fn log10(self) -> $F { unsafe { c_log10(self as f64) as $F } }
    pub fn cbrt(self) -> $F { unsafe { c_cbrt(self as f64) as $F } }
    pub fn hypot(self, other: $F) -> $F { unsafe { c_hypot(self as f64, other as f64) as $F } }
    pub fn sin(self) -> $F { unsafe { c_sin(self as f64) as $F } }
    pub fn cos(self) -> $F { unsafe { c_cos(self as f64) as $F } }
    pub fn tan(self) -> $F { unsafe { c_tan(self as f64) as $F } }
    pub fn asin(self) -> $F { unsafe { c_asin(self as f64) as $F } }
    pub fn acos(self) -> $F { unsafe { c_acos(self as f64) as $F } }
    pub fn atan(self) -> $F { unsafe { c_atan(self as f64) as $F } }
    pub fn atan2(self, other: $F) -> $F { unsafe { c_atan2(self as f64, other as f64) as $F } }
    pub fn sin_cos(self) -> ($F, $F) { (self.sin(), self.cos()) }
    pub fn sinh(self) -> $F { unsafe { c_sinh(self as f64) as $F } }
    pub fn cosh(self) -> $F { unsafe { c_cosh(self as f64) as $F } }
    pub fn tanh(self) -> $F { unsafe { c_tanh(self as f64) as $F } }
    pub fn asinh(self) -> $F { unsafe { c_asinh(self as f64) as $F } }
    pub fn acosh(self) -> $F { unsafe { c_acosh(self as f64) as $F } }
    pub fn atanh(self) -> $F { unsafe { c_atanh(self as f64) as $F } }
    /// Rounds half-way cases to the even integer.
    pub fn round_ties_even(self) -> $F { unsafe { c_rint(self as f64) as $F } }
    pub fn to_le_bytes(self) -> [u8; $BYTES] { self.to_bits().to_le_bytes() }
    pub fn to_be_bytes(self) -> [u8; $BYTES] { self.to_bits().to_be_bytes() }
    pub fn to_ne_bytes(self) -> [u8; $BYTES] { self.to_bits().to_ne_bytes() }
    pub fn from_le_bytes(bytes: [u8; $BYTES]) -> $F { $F::from_bits($UB::from_le_bytes(bytes)) }
    pub fn from_be_bytes(bytes: [u8; $BYTES]) -> $F { $F::from_bits($UB::from_be_bytes(bytes)) }
    pub fn from_ne_bytes(bytes: [u8; $BYTES]) -> $F { $F::from_bits($UB::from_ne_bytes(bytes)) }
`

const extF64 = `
    pub const RADIX: u32 = 2;
    pub const MANTISSA_DIGITS: u32 = 53;
    pub const DIGITS: u32 = 15;
    pub const MIN_POSITIVE: f64 = 2.2250738585072014e-308;
    pub const MIN_EXP: i32 = -1021;
    pub const MAX_EXP: i32 = 1024;
    pub const MIN_10_EXP: i32 = -307;
    pub const MAX_10_EXP: i32 = 308;
}
`

const extF32 = `
    pub const RADIX: u32 = 2;
    pub const MANTISSA_DIGITS: u32 = 24;
    pub const DIGITS: u32 = 6;
    pub const MAX: f32 = 3.40282347e38;
    pub const MIN: f32 = -3.40282347e38;
    pub const EPSILON: f32 = 1.19209290e-7;
    pub const MIN_POSITIVE: f32 = 1.17549435e-38;
    pub const MIN_EXP: i32 = -125;
    pub const MAX_EXP: i32 = 128;
    pub const MIN_10_EXP: i32 = -37;
    pub const MAX_10_EXP: i32 = 38;

    pub fn is_infinite(self) -> bool { self == f32::INFINITY || self == f32::NEG_INFINITY }
    pub fn is_finite(self) -> bool { !self.is_nan() && !self.is_infinite() }
    pub fn is_sign_negative(self) -> bool { self.to_bits() >> 31 == 1 }
    pub fn ceil(self) -> f32 { (self as f64).ceil() as f32 }
    pub fn trunc(self) -> f32 { (self as f64).trunc() as f32 }
    pub fn round(self) -> f32 { (self as f64).round() as f32 }
    pub fn powi(self, n: i32) -> f32 { (self as f64).powi(n) as f32 }
    pub fn max(self, other: f32) -> f32 { if self.is_nan() || other > self { other } else { self } }
    pub fn min(self, other: f32) -> f32 { if self.is_nan() || other < self { other } else { self } }
}
`

const extHeader = `//! More inherent methods of the integer and float types, conversions
//! between them, and parsing of the 128-bit integers (navic only: stage0
//! compiles std without this module).
//! Generated by tools/gennum (Rust writes these with macros); do not edit.

use crate::convert::{From, Infallible, TryFrom};
use crate::option::Option::{self, None, Some};
use crate::result::Result::{self, Err, Ok};
use crate::str::{FromStr, ParseIntError};

/// The error of a failed integer conversion (TryFrom).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TryFromIntError(());

impl crate::fmt::Display for TryFromIntError {
    fn fmt(&self, f: &mut crate::fmt::Formatter) -> crate::fmt::Result { f.pad("out of range integral type conversion attempted") }
}

/// The kinds of floating point numbers (f64::classify).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FpCategory {
    Nan,
    Infinite,
    Zero,
    Subnormal,
    Normal,
}

extern "C" {
    #[link_name = "fma"] fn c_fma(a: f64, b: f64, c: f64) -> f64;
    #[link_name = "pow"] fn c_pow(x: f64, y: f64) -> f64;
    #[link_name = "exp"] fn c_exp(x: f64) -> f64;
    #[link_name = "exp2"] fn c_exp2(x: f64) -> f64;
    #[link_name = "expm1"] fn c_expm1(x: f64) -> f64;
    #[link_name = "log"] fn c_log(x: f64) -> f64;
    #[link_name = "log1p"] fn c_log1p(x: f64) -> f64;
    #[link_name = "log2"] fn c_log2(x: f64) -> f64;
    #[link_name = "log10"] fn c_log10(x: f64) -> f64;
    #[link_name = "cbrt"] fn c_cbrt(x: f64) -> f64;
    #[link_name = "hypot"] fn c_hypot(x: f64, y: f64) -> f64;
    #[link_name = "sin"] fn c_sin(x: f64) -> f64;
    #[link_name = "cos"] fn c_cos(x: f64) -> f64;
    #[link_name = "tan"] fn c_tan(x: f64) -> f64;
    #[link_name = "asin"] fn c_asin(x: f64) -> f64;
    #[link_name = "acos"] fn c_acos(x: f64) -> f64;
    #[link_name = "atan"] fn c_atan(x: f64) -> f64;
    #[link_name = "atan2"] fn c_atan2(y: f64, x: f64) -> f64;
    #[link_name = "sinh"] fn c_sinh(x: f64) -> f64;
    #[link_name = "cosh"] fn c_cosh(x: f64) -> f64;
    #[link_name = "tanh"] fn c_tanh(x: f64) -> f64;
    #[link_name = "asinh"] fn c_asinh(x: f64) -> f64;
    #[link_name = "acosh"] fn c_acosh(x: f64) -> f64;
    #[link_name = "atanh"] fn c_atanh(x: f64) -> f64;
    #[link_name = "rint"] fn c_rint(x: f64) -> f64;
}

/// Parses an optionally signed integer in the given radix: the sign and
/// the magnitude (None on bad digits or u128 overflow).
fn parse_radix128(s: &str, radix: u32, signed: bool) -> Option<(bool, u128)> {
    let b = s.as_bytes();
    if b.len() == 0 { return None; }
    let mut i = 0;
    let mut neg = false;
    if b[0] == b'-' || b[0] == b'+' {
        neg = b[0] == b'-';
        if neg && !signed { return None; }
        i = 1;
        if b.len() == 1 { return None; }
    }
    let r = radix as u128;
    let mut v: u128 = 0;
    while i < b.len() {
        let d = match (b[i] as char).to_digit(radix) { Some(d) => d as u128, None => return None };
        v = v.checked_mul(r)?.checked_add(d)?;
        i += 1;
    }
    Some((neg, v))
}

fn i128_of(x: u128) -> i128 { x as i128 }

impl FromStr for i128 { type Err = ParseIntError; fn from_str(s: &str) -> Result<i128, ParseIntError> { i128::from_str_radix(s, 10) } }
impl FromStr for u128 { type Err = ParseIntError; fn from_str(s: &str) -> Result<u128, ParseIntError> { u128::from_str_radix(s, 10) } }
`

// The consts modules (std::f64::consts and std::f32::consts).
func constsModule(f string) string {
	type c struct{ name, val, doc string }
	cs := []c{
		{"PI", "3.14159265358979323846264338327950288", "Archimedes' constant (π)"},
		{"TAU", "6.28318530717958647692528676655900577", "The full circle constant (τ = 2π)"},
		{"FRAC_PI_2", "1.57079632679489661923132169163975144", "π/2"},
		{"FRAC_PI_3", "1.04719755119659774615421446109316763", "π/3"},
		{"FRAC_PI_4", "0.785398163397448309615660845819875721", "π/4"},
		{"FRAC_PI_6", "0.52359877559829887307710723054658381", "π/6"},
		{"FRAC_PI_8", "0.39269908169872415480783042290993786", "π/8"},
		{"FRAC_1_PI", "0.318309886183790671537767526745028724", "1/π"},
		{"FRAC_2_PI", "0.636619772367581343075535053490057448", "2/π"},
		{"FRAC_2_SQRT_PI", "1.12837916709551257389615890312154517", "2/sqrt(π)"},
		{"SQRT_2", "1.41421356237309504880168872420969808", "sqrt(2)"},
		{"FRAC_1_SQRT_2", "0.707106781186547524400844362104849039", "1/sqrt(2)"},
		{"E", "2.71828182845904523536028747135266250", "Euler's number (e)"},
		{"LOG2_10", "3.32192809488736234787031942948939018", "log2(10)"},
		{"LOG2_E", "1.44269504088896340735992468100189214", "log2(e)"},
		{"LOG10_2", "0.301029995663981195213738894724493027", "log10(2)"},
		{"LOG10_E", "0.434294481903251827651128918916605082", "log10(e)"},
		{"LN_2", "0.693147180559945309417232121458176568", "ln(2)"},
		{"LN_10", "2.30258509299404568401799145468436421", "ln(10)"},
	}
	var b strings.Builder
	fmt.Fprintf(&b, "//! Constants of the %[1]s type (std::%[1]s::consts; the other items of\n//! Rust's std::%[1]s module are associated constants of %[1]s).\n//! Generated by tools/gennum; do not edit.\n\npub mod consts {\n", f)
	for _, x := range cs {
		fmt.Fprintf(&b, "    /// %s\n    pub const %s: %s = %s;\n", x.doc, x.name, f, x.val)
	}
	b.WriteString("}\n")
	return b.String()
}

func extFile() string {
	var b strings.Builder
	b.WriteString(extHeader)
	for _, t := range allInts {
		b.WriteString(extFor(t))
	}
	for _, f := range []struct {
		name, bits, ibits string
		n                 int
		expMask, manMask  string
		sign              string
		tail              string
	}{
		{"f64", "u64", "i64", 64, "0x7ff0000000000000", "0x000fffffffffffff", "0x8000000000000000", extF64},
		{"f32", "u32", "i32", 32, "0x7f800000", "0x007fffff", "0x80000000", extF32},
	} {
		r := strings.NewReplacer("$F", f.name, "$UB", f.bits, "$I", f.ibits, "$BITS", fmt.Sprint(f.n), "$BYTES", fmt.Sprint(f.n/8),
			"$EXP_MASK", f.expMask, "$MAN_MASK", f.manMask, "$SIGN", f.sign)
		b.WriteString(r.Replace(extFloat))
		b.WriteString(r.Replace(f.tail))
	}
	b.WriteString(conversions())
	return b.String()
}
