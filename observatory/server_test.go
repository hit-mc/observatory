package observatory

import (
	"github.com/hit-mc/observatory/protocol"
	"math/rand"
	"testing"
	"time"
)

func Fuzz_shrink(f *testing.F) {
	f.Fuzz(func(t *testing.T, seed int64) {
		s := rand.NewSource(seed)
		n := s.Int63() % 1023
		ss := make([]protocol.TargetSourceObservation, n)
		nonzero := protocol.TimeStamp(time.Unix(10000, 10000))
		n2 := 0
		for i := int64(0); i < n; i++ {
			if s.Int63()%17 < 12 {
				ss[i].Time = nonzero
				n2++
			}
		}
		ss2 := shrink(ss)
		if len(ss2) != n2 {
			t.Fatal("invalid length ", len(ss2), " expected", n2)
		}
		for i := range ss2 {
			if time.Time(ss2[i].Time).IsZero() {
				t.Fatal("invalid time at ", i)
			}
		}
	})
}
