//go:build (386 && !appengine) || (amd64 && !appengine) || (arm && !appengine) || (arm64 && !appengine) || (ppc64le && !appengine) || (mipsle && !appengine) || (mips64le && !appengine) || (mips64p32le && !appengine) || (wasm && !appengine)
// +build 386,!appengine amd64,!appengine arm,!appengine arm64,!appengine ppc64le,!appengine mipsle,!appengine mips64le,!appengine mips64p32le,!appengine wasm,!appengine

package roaring

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Frozen views exist only where serialization_littleendian.go is built.
func init() {
	valueAndViews = append(valueAndViews, valueAndView{"frozen", func(t *testing.T, bm *Bitmap) *Bitmap {
		buf, err := bm.Freeze()
		require.NoError(t, err)
		out := New()
		require.NoError(t, out.FrozenView(buf))
		return out
	}})
}
