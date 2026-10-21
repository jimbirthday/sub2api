package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestStartupCleanupPreservesLiveForeignProcessSlot(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	key := userSlotKeyPrefix + "42"
	now := time.Now().Unix()
	if err := client.ZAdd(ctx, key,
		redis.Z{Score: float64(now), Member: "other-instance:request-1"},
		redis.Z{Score: float64(now - 901), Member: "dead-instance:request-2"},
	).Err(); err != nil {
		t.Fatal(err)
	}

	removed, remaining, err := runScriptInt64Pair(ctx, client, startupCleanupSlotScript, []string{key}, 900)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || remaining != 1 {
		t.Fatalf("removed=%d remaining=%d", removed, remaining)
	}
	if score, err := client.ZScore(ctx, key, "other-instance:request-1").Result(); err != nil || score == 0 {
		t.Fatalf("live foreign slot was removed: score=%v err=%v", score, err)
	}
}
