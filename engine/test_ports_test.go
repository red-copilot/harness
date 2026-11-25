package engine

import (
	"os"
	"sync/atomic"
	"time"
)

type testClock struct{}

var testClockTick atomic.Int64

func (testClock) Now() time.Time {
	return time.Unix(1700000000, testClockTick.Add(1)*int64(time.Millisecond)).UTC()
}

type testSecrets struct{}

func (testSecrets) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }
