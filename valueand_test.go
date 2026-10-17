package roaring

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// previousValueAndReference answers PreviousValueAnd by materialising the intersection.
func previousValueAndReference(a, b *Bitmap, target uint32) int64 {
	and := And(a, b)
	r := and.Rank(target)
	if r == 0 {
		return -1
	}
	v, err := and.Select(uint32(r - 1))
	if err != nil {
		panic(err)
	}
	return int64(v)
}

// nextValueAndReference answers NextValueAnd by materialising the intersection.
func nextValueAndReference(a, b *Bitmap, target uint32) int64 {
	and := And(a, b)
	var below uint64
	if target > 0 {
		below = and.Rank(target - 1)
	}
	if below == and.GetCardinality() {
		return -1
	}
	v, err := and.Select(uint32(below))
	if err != nil {
		panic(err)
	}
	return int64(v)
}

// randomValueAndBitmap builds a bitmap over a random window of containers that is made of array, bitmap or run
// containers, or a mix of them.
func randomValueAndBitmap(r *rand.Rand) *Bitmap {
	bm := New()
	// Some windows sit at the top of the 32-bit range so that the last container and math.MaxUint32 are covered.
	first := uint64(r.Intn(4))
	if r.Intn(5) == 0 {
		first = 0xFFFF - uint64(r.Intn(4))
	}
	span := uint64(1 + r.Intn(4))
	if first+span > 0x10000 {
		span = 0x10000 - first
	}
	lo, n := first<<16, span<<16
	value := func() uint32 { return uint32(lo + uint64(r.Int63n(int64(n)))) }
	switch r.Intn(6) {
	case 0: // empty
	case 5: // tiny: arrays a fraction of the size of the sparse ones, so intersections take the galloping path
		for k := 1 + r.Intn(40); k > 0; k-- {
			bm.Add(value())
		}
	case 1: // sparse: array containers
		for k := r.Intn(3000); k > 0; k-- {
			bm.Add(value())
		}
	case 2: // dense: bitmap containers
		for k := r.Intn(int(n)); k > 0; k-- {
			bm.Add(value())
		}
	case 3: // runs
		for k := 1 + r.Intn(100); k > 0; k-- {
			start := uint64(value())
			bm.AddRange(start, min(start+uint64(r.Intn(5000)), lo+n))
		}
		bm.RunOptimize()
	default: // a mix of all three
		for k := r.Intn(20000); k > 0; k-- {
			bm.Add(value())
		}
		start := uint64(value())
		bm.AddRange(start, min(start+70000, lo+n))
		bm.RunOptimize()
	}
	return bm
}

// sampleWithNoise returns a few values of bm mixed with values next to them that bm does not have, so that a lookup
// in bm often misses just above or below a hit.
func sampleWithNoise(r *rand.Rand, bm *Bitmap) *Bitmap {
	out := New()
	card := bm.GetCardinality()
	if card == 0 {
		return out
	}
	for k := 1 + r.Intn(60); k > 0; k-- {
		v, err := bm.Select(uint32(r.Int63n(int64(card))))
		if err != nil {
			panic(err)
		}
		out.Add(v)
		for _, near := range []uint32{v - 1, v + 1, v - 2, v + 2} {
			if !bm.Contains(near) {
				out.Add(near)
			}
		}
	}
	return out
}

// valueAndTargets returns random targets plus the edges where off-by-one errors live: container boundaries, the
// ends of the 32-bit range, and every common value and its neighbours.
func valueAndTargets(r *rand.Rand, a, b *Bitmap) []uint32 {
	targets := []uint32{0, 1, math.MaxUint32, math.MaxUint32 - 1}
	for _, k := range []uint32{1, 2, 3, 4, 0xFFFE, 0xFFFF} {
		targets = append(targets, k<<16-1, k<<16, k<<16+1, k<<16+0xFFFF)
	}
	for k := 0; k < 30; k++ {
		targets = append(targets, uint32(r.Int63n(5<<16)), uint32(r.Int63n(math.MaxUint32+1)))
	}
	it := And(a, b).Iterator()
	for k := 0; it.HasNext() && k < 30; k++ {
		v := it.Next()
		targets = append(targets, v-1, v, v+1)
	}
	return targets
}

func TestPreviousAndNextValueAnd(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	views := []struct {
		name string
		view func(*Bitmap) *Bitmap
	}{
		{"plain", func(bm *Bitmap) *Bitmap { return bm }},
		{"frozen", func(bm *Bitmap) *Bitmap {
			buf, err := bm.Freeze()
			require.NoError(t, err)
			out := New()
			require.NoError(t, out.FrozenView(buf))
			return out
		}},
		{"buffer", func(bm *Bitmap) *Bitmap {
			buf, err := bm.ToBytes()
			require.NoError(t, err)
			out := New()
			_, err = out.FromBuffer(buf)
			require.NoError(t, err)
			return out
		}},
	}
	for iter := 0; iter < 300; iter++ {
		a, b := randomValueAndBitmap(r), randomValueAndBitmap(r)
		switch r.Intn(4) {
		case 0:
			b = a.Clone() // every value common
		case 1:
			b = sampleWithNoise(r, a) // a small part of a among values that are not in it
		}
		view := views[iter%len(views)]
		va, vb := view.view(a), view.view(b)
		for _, target := range valueAndTargets(r, a, b) {
			wantPrev := previousValueAndReference(a, b, target)
			wantNext := nextValueAndReference(a, b, target)
			assert.Equal(t, wantPrev, va.PreviousValueAnd(vb, target), "%s iter %d: PreviousValueAnd(%d)", view.name, iter, target)
			assert.Equal(t, wantPrev, vb.PreviousValueAnd(va, target), "%s iter %d: swapped PreviousValueAnd(%d)", view.name, iter, target)
			assert.Equal(t, wantNext, va.NextValueAnd(vb, target), "%s iter %d: NextValueAnd(%d)", view.name, iter, target)
			assert.Equal(t, wantNext, vb.NextValueAnd(va, target), "%s iter %d: swapped NextValueAnd(%d)", view.name, iter, target)
		}
		if t.Failed() {
			t.FailNow()
		}
	}
}

func TestPreviousAndNextValueAndSmall(t *testing.T) {
	a := BitmapOf(1, 5, 9, 70000, 70010, 1<<20)
	b := BitmapOf(5, 9, 70010, 1<<20, math.MaxUint32)
	empty := New()

	assert.EqualValues(t, -1, a.PreviousValueAnd(b, 4))
	assert.EqualValues(t, 5, a.PreviousValueAnd(b, 5))
	assert.EqualValues(t, 9, a.PreviousValueAnd(b, 70009))
	assert.EqualValues(t, 70010, a.PreviousValueAnd(b, 70010))
	assert.EqualValues(t, 1<<20, a.PreviousValueAnd(b, math.MaxUint32))
	assert.EqualValues(t, -1, a.PreviousValueAnd(empty, math.MaxUint32))
	assert.EqualValues(t, -1, empty.PreviousValueAnd(a, math.MaxUint32))

	assert.EqualValues(t, 5, a.NextValueAnd(b, 0))
	assert.EqualValues(t, 9, a.NextValueAnd(b, 6))
	assert.EqualValues(t, 70010, a.NextValueAnd(b, 10))
	assert.EqualValues(t, -1, a.NextValueAnd(b, 1<<20+1))
	assert.EqualValues(t, math.MaxUint32, b.NextValueAnd(b, math.MaxUint32))
	assert.EqualValues(t, -1, a.NextValueAnd(empty, 0))
}

func TestPreviousAndNextValueAndDoNotAllocate(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	a, b := New(), New()
	for k := 0; k < 100000; k++ {
		a.Add(uint32(r.Int63n(50 << 16)))
		b.Add(uint32(r.Int63n(50 << 16)))
	}
	b.AddRange(10<<16, 12<<16)
	b.RunOptimize()

	allocs := testing.AllocsPerRun(100, func() {
		a.PreviousValueAnd(b, 49<<16)
		a.NextValueAnd(b, 1<<16)
	})
	assert.Zero(t, allocs)
}

func BenchmarkPreviousValueAnd(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	busy, symbol := New(), New()
	for k := 0; k < 1000000; k++ {
		busy.Add(uint32(r.Int63n(70000000)))
	}
	for k := 0; k < 20000000; k++ {
		symbol.Add(uint32(r.Int63n(70000000)))
	}
	symbol.RunOptimize()
	target := uint32(69000000)

	b.Run("PreviousValueAnd", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			busy.PreviousValueAnd(symbol, target)
		}
	})
	b.Run("And+Rank+Select", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			previousValueAndReference(busy, symbol, target)
		}
	})
}
