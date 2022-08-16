package raus

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

type RedisClient interface {
	Close() error
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
	TxPipeline() redis.Pipeliner
}

type RedisOptions struct {
	Cluster  bool
	Addrs    []string
	Username string
	Password string
	DB       int
}

func (o *RedisOptions) NewClient() RedisClient {
	if o.Cluster {
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    o.Addrs,
			Username: o.Username,
			Password: o.Password,
		})
	} else {
		return redis.NewClient(&redis.Options{
			Network:  "tcp",
			Addr:     o.Addrs[0],
			Username: o.Username,
			Password: o.Password,
			DB:       o.DB,
		})
	}
}
