package roaring

import "math/bits"

// PreviousValueAnd returns the largest value <= target that is present in both rb and other, or -1 if there is
// none. It gives the same answer as And(rb, other).PreviousValue(target) without building the intersection.
//
// It walks both bitmaps' containers downwards from target's container, intersects only the pairs of containers
// that share a key, and stops at the first common value. It allocates nothing, and its cost depends on the
// containers it visits before that hit, not on the cardinality of either bitmap or of their intersection.
// Neither bitmap is modified, so it is safe on frozen views.
func (rb *Bitmap) PreviousValueAnd(other *Bitmap, target uint32) int64 {
	hb := highbits(target)
	ra, oa := &rb.highlowcontainer, &other.highlowcontainer
	i := lastKeyAtOrBelow(ra.keys, hb)
	j := lastKeyAtOrBelow(oa.keys, hb)
	for i >= 0 && j >= 0 {
		ki, kj := ra.keys[i], oa.keys[j]
		switch {
		case ki > kj:
			i--
		case kj > ki:
			j--
		default:
			limit := uint16(0xFFFF)
			if ki == hb {
				limit = lowbits(target)
			}
			if v := previousCommonValue(ra.containers[i], oa.containers[j], limit); v >= 0 {
				return int64(combineLoHi32(uint32(v), uint32(ki)))
			}
			i--
			j--
		}
	}
	return -1
}

// NextValueAnd returns the smallest value >= target that is present in both rb and other, or -1 if there is
// none. It gives the same answer as And(rb, other).NextValue(target) without building the intersection.
//
// It is the mirror image of PreviousValueAnd: it walks both bitmaps' containers upwards from target's container
// and stops at the first common value, allocating nothing.
func (rb *Bitmap) NextValueAnd(other *Bitmap, target uint32) int64 {
	hb := highbits(target)
	ra, oa := &rb.highlowcontainer, &other.highlowcontainer
	i := firstKeyAtOrAbove(ra.keys, hb)
	j := firstKeyAtOrAbove(oa.keys, hb)
	for i < len(ra.keys) && j < len(oa.keys) {
		ki, kj := ra.keys[i], oa.keys[j]
		switch {
		case ki < kj:
			i++
		case kj < ki:
			j++
		default:
			start := uint16(0)
			if ki == hb {
				start = lowbits(target)
			}
			if v := nextCommonValue(ra.containers[i], oa.containers[j], start); v >= 0 {
				return int64(combineLoHi32(uint32(v), uint32(ki)))
			}
			i++
			j++
		}
	}
	return -1
}

// lastKeyAtOrBelow returns the index of the last value <= v in the sorted slice a, or -1.
func lastKeyAtOrBelow(a []uint16, v uint16) int {
	return firstKeyAbove(a, v) - 1
}

// firstKeyAtOrAbove returns the index of the first value >= v in the sorted slice a, or len(a).
func firstKeyAtOrAbove(a []uint16, v uint16) int {
	if v == 0 {
		return 0
	}
	return firstKeyAbove(a, v-1)
}

// firstKeyAbove returns the index of the first value > v in the sorted slice a, or len(a).
func firstKeyAbove(a []uint16, v uint16) int {
	lo, hi := 0, len(a)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if a[mid] <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// previousCommonValue returns the largest value <= limit present in both containers, or -1.
func previousCommonValue(a, b container, limit uint16) int {
	switch x := a.(type) {
	case *bitmapContainer:
		switch y := b.(type) {
		case *bitmapContainer:
			return previousCommonBitmaps(x, y, limit)
		case *arrayContainer:
			return previousCommonArrayBitmap(y, x, limit)
		}
	case *arrayContainer:
		switch y := b.(type) {
		case *bitmapContainer:
			return previousCommonArrayBitmap(x, y, limit)
		case *arrayContainer:
			return previousCommonArrays(x, y, limit)
		}
	}
	return previousCommonLeapfrog(a, b, limit)
}

// previousCommonBitmaps ANDs the two bitmaps a word at a time from limit downwards.
func previousCommonBitmaps(a, b *bitmapContainer, limit uint16) int {
	mask := ^uint64(0) >> (63 - uint(limit&63))
	for w := int(limit >> 6); w >= 0; w-- {
		if x := a.bitmap[w] & b.bitmap[w] & mask; x != 0 {
			return w<<6 + 63 - bits.LeadingZeros64(x)
		}
		mask = ^uint64(0)
	}
	return -1
}

// previousCommonArrayBitmap walks the array downwards from limit and tests each value's bit.
func previousCommonArrayBitmap(a *arrayContainer, b *bitmapContainer, limit uint16) int {
	c := a.content
	for k := lastKeyAtOrBelow(c, limit); k >= 0; k-- {
		v := c[k]
		if b.bitmap[v>>6]&(uint64(1)<<(v&63)) != 0 {
			return int(v)
		}
	}
	return -1
}

// gallopThreshold is the size ratio above which two arrays are intersected by binary searches into the larger one
// rather than by a merge.
const gallopThreshold = 16

// previousCommonArrays merges the two sorted arrays backwards from limit, or, when one is much larger than the
// other, looks each value of the smaller one up in the larger.
func previousCommonArrays(a, b *arrayContainer, limit uint16) int {
	x, y := a.content, b.content
	i, j := lastKeyAtOrBelow(x, limit), lastKeyAtOrBelow(y, limit)
	if i < 0 || j < 0 {
		return -1
	}
	if i > j {
		x, y, i, j = y, x, j, i
	}
	if j+1 >= gallopThreshold*(i+1) {
		// y[:hi] holds the values of y that can still match: everything above the last lookup is gone.
		hi := j + 1
		for ; i >= 0; i-- {
			v := x[i]
			k := lastKeyAtOrBelow(y[:hi], v)
			if k < 0 {
				return -1
			}
			if y[k] == v {
				return int(v)
			}
			hi = k + 1
		}
		return -1
	}
	for i >= 0 && j >= 0 {
		switch {
		case x[i] == y[j]:
			return int(x[i])
		case x[i] > y[j]:
			i--
		default:
			j--
		}
	}
	return -1
}

// previousCommonLeapfrog handles run containers: each side jumps to the other's previous value, so a long run
// is crossed in one step.
func previousCommonLeapfrog(a, b container, limit uint16) int {
	x := a.previousValue(limit)
	for x >= 0 {
		y := b.previousValue(uint16(x))
		if y < 0 {
			return -1
		}
		if y == x {
			return x
		}
		x = a.previousValue(uint16(y))
	}
	return -1
}

// nextCommonValue returns the smallest value >= start present in both containers, or -1.
func nextCommonValue(a, b container, start uint16) int {
	switch x := a.(type) {
	case *bitmapContainer:
		switch y := b.(type) {
		case *bitmapContainer:
			return nextCommonBitmaps(x, y, start)
		case *arrayContainer:
			return nextCommonArrayBitmap(y, x, start)
		}
	case *arrayContainer:
		switch y := b.(type) {
		case *bitmapContainer:
			return nextCommonArrayBitmap(x, y, start)
		case *arrayContainer:
			return nextCommonArrays(x, y, start)
		}
	}
	return nextCommonLeapfrog(a, b, start)
}

// nextCommonBitmaps ANDs the two bitmaps a word at a time from start upwards.
func nextCommonBitmaps(a, b *bitmapContainer, start uint16) int {
	mask := ^uint64(0) << uint(start&63)
	for w := int(start >> 6); w < len(a.bitmap); w++ {
		if x := a.bitmap[w] & b.bitmap[w] & mask; x != 0 {
			return w<<6 + bits.TrailingZeros64(x)
		}
		mask = ^uint64(0)
	}
	return -1
}

// nextCommonArrayBitmap walks the array upwards from start and tests each value's bit.
func nextCommonArrayBitmap(a *arrayContainer, b *bitmapContainer, start uint16) int {
	c := a.content
	for k := firstKeyAtOrAbove(c, start); k < len(c); k++ {
		v := c[k]
		if b.bitmap[v>>6]&(uint64(1)<<(v&63)) != 0 {
			return int(v)
		}
	}
	return -1
}

// nextCommonArrays merges the two sorted arrays forwards from start, or, when one is much larger than the other,
// looks each value of the smaller one up in the larger.
func nextCommonArrays(a, b *arrayContainer, start uint16) int {
	x, y := a.content, b.content
	i, j := firstKeyAtOrAbove(x, start), firstKeyAtOrAbove(y, start)
	if i == len(x) || j == len(y) {
		return -1
	}
	if len(x)-i > len(y)-j {
		x, y, i, j = y, x, j, i
	}
	if len(y)-j >= gallopThreshold*(len(x)-i) {
		// y[lo:] holds the values of y that can still match: everything below the last lookup is gone.
		lo := j
		for ; i < len(x); i++ {
			v := x[i]
			k := lo + firstKeyAtOrAbove(y[lo:], v)
			if k == len(y) {
				return -1
			}
			if y[k] == v {
				return int(v)
			}
			lo = k
		}
		return -1
	}
	for i < len(x) && j < len(y) {
		switch {
		case x[i] == y[j]:
			return int(x[i])
		case x[i] < y[j]:
			i++
		default:
			j++
		}
	}
	return -1
}

// nextCommonLeapfrog handles run containers: each side jumps to the other's next value.
func nextCommonLeapfrog(a, b container, start uint16) int {
	x := a.nextValue(start)
	for x >= 0 {
		y := b.nextValue(uint16(x))
		if y < 0 {
			return -1
		}
		if y == x {
			return x
		}
		x = a.nextValue(uint16(y))
	}
	return -1
}
