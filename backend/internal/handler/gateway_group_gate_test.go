//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/stretchr/testify/require"
)

// groupGateConcurrencyCache counts group slots and always admits accounts.
type groupGateConcurrencyCache struct {
	testutil.StubConcurrencyCache
	groupCount map[int64]int
}

func (c *groupGateConcurrencyCache) AcquireGroupSlot(_ context.Context, groupID int64, maxConcurrency int, _ string) (bool, error) {
	if c.groupCount == nil {
		c.groupCount = map[int64]int{}
	}
	if c.groupCount[groupID] >= maxConcurrency {
		return false, nil
	}
	c.groupCount[groupID]++
	return true, nil
}

func (c *groupGateConcurrencyCache) ReleaseGroupSlot(_ context.Context, groupID int64, _ string) error {
	if c.groupCount[groupID] > 0 {
		c.groupCount[groupID]--
	}
	return nil
}

func TestTryAcquireGroupSlot_EnforcesCapAndReleases(t *testing.T) {
	cache := &groupGateConcurrencyCache{}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)

	release1, ok1, err := helper.TryAcquireGroupSlot(context.Background(), 10, 2)
	require.NoError(t, err)
	require.True(t, ok1)
	require.NotNil(t, release1)

	release2, ok2, err := helper.TryAcquireGroupSlot(context.Background(), 10, 2)
	require.NoError(t, err)
	require.True(t, ok2)

	_, ok3, err := helper.TryAcquireGroupSlot(context.Background(), 10, 2)
	require.NoError(t, err)
	require.False(t, ok3, "third slot must be rejected at max=2")

	release1()
	release1() // idempotent double release must be safe

	release4, ok4, err := helper.TryAcquireGroupSlot(context.Background(), 10, 2)
	require.NoError(t, err)
	require.True(t, ok4, "released slot must be reusable")
	release2()
	release4()
}

func TestTryAcquireGroupSlot_DisabledFailOpen(t *testing.T) {
	helper := NewConcurrencyHelper(service.NewConcurrencyService(&groupGateConcurrencyCache{}), SSEPingFormatNone, 0)

	for _, max := range []int{0, -1} {
		release, ok, err := helper.TryAcquireGroupSlot(context.Background(), 10, max)
		require.NoError(t, err)
		require.True(t, ok, "group gate must be fail-open when max<=0")
		release()
	}
	release, ok, err := helper.TryAcquireGroupSlot(context.Background(), 0, 5)
	require.NoError(t, err)
	require.True(t, ok, "group gate must be fail-open when groupID<=0")
	release()
}
