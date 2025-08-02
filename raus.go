package raus

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	stdlog "log"
	"log/slog"
	"math/rand"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/google/uuid"
)

type Raus struct {
	rand          *rand.Rand
	uuid          string
	id            uint
	min           uint
	max           uint
	redisOptions  *RedisOptions
	namespace     string
	pubSubChannel string
	channel       chan error
	logger        *slog.Logger
}

const (
	DefaultNamespace    = "raus"
	pubSubChannelSuffix = ":broadcast"
)

var (
	MaxCandidate     = 10
	LockExpires      = 60 * time.Second
	SubscribeTimeout = time.Second * 3
	CleanupTimeout   = time.Second * 30
	log              Logger
	defaultLogger    = slog.New(slog.NewTextHandler(os.Stderr, nil))
)

type Logger interface {
	Println(...interface{})
	Printf(string, ...interface{})
}

type fatal interface {
	isFatal() bool
}

func isFatal(err error) bool {
	fe, ok := err.(fatal)
	return ok && fe.isFatal()
}

type fatalError struct {
	error
}

func (e fatalError) isFatal() bool {
	return true
}

func init() {
	log = stdlog.New(os.Stderr, "", stdlog.LstdFlags) // default logger
}

func SetLogger(l Logger) {
	log = l
}

// SetDefaultSlogLogger sets the default slog.Logger for new Raus instances.
func SetDefaultSlogLogger(l *slog.Logger) {
	defaultLogger = l
}

// SetSlogLogger sets the slog.Logger for this Raus instance.
func (r *Raus) SetSlogLogger(l *slog.Logger) {
	r.logger = l
}

// New creates *Raus object.
func New(redisURI string, min, max uint) (*Raus, error) {
	var s int64
	if err := binary.Read(crand.Reader, binary.LittleEndian, &s); err != nil {
		s = time.Now().UnixNano()
	}
	if min >= max {
		return nil, fmt.Errorf("max should be greater than min")
	}
	op, ns, err := ParseRedisURI(redisURI)
	if err != nil {
		return nil, err
	}
	u, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	return &Raus{
		rand:          rand.New(rand.NewSource(s)),
		uuid:          u.String(),
		min:           min,
		max:           max,
		redisOptions:  op,
		namespace:     ns,
		pubSubChannel: ns + pubSubChannelSuffix,
		channel:       make(chan error),
		logger:        defaultLogger,
	}, nil
}

// ParseRedisURI parses uri for redis (redis://host:port/db?ns=namespace)
func ParseRedisURI(s string) (*RedisOptions, string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, "", err
	}
	op := &RedisOptions{}
	switch u.Scheme {
	case "redis":
		op.Cluster = false
	case "rediscluster":
		op.Cluster = true
	default:
		return nil, "", fmt.Errorf("invalid scheme %s", u.Scheme)
	}
	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		h = u.Host
		p = "6379"
	}
	op.Addrs = []string{h + ":" + p}

	if u.User != nil {
		if uname := u.User.Username(); uname != "" {
			op.Username = uname
		}
		if pass, ok := u.User.Password(); ok {
			op.Password = pass
		}
	}

	if u.Path == "" || u.Path == "/" {
		op.DB = 0
	} else if op.Cluster {
		return nil, "", fmt.Errorf("database is not supported for redis cluster")
	} else {
		ps := strings.Split(u.Path, "/")
		if len(ps) > 1 {
			i, err := strconv.Atoi(ps[1])
			if err != nil {
				return nil, "", fmt.Errorf("invalid database %s", ps[1])
			}
			op.DB = i
		} else {
			op.DB = 0
		}
	}
	ns := u.Query()["ns"]
	if len(ns) > 0 {
		return op, ns[0], nil
	} else {
		return op, DefaultNamespace, nil
	}
}

func (r *Raus) size() uint {
	return r.max - r.min
}

// Get gets unique id ranged between min and max.
func (r *Raus) Get(ctx context.Context) (uint, chan error, error) {
	if err := r.subscribe(ctx); err != nil {
		return 0, r.channel, err
	}
	go r.publish(ctx)
	return r.id, r.channel, nil
}

func (r *Raus) subscribe(ctx context.Context) error {
	// table for looking up unused id
	usedIds := make(map[uint]bool, r.size())

	c := r.redisOptions.NewClient()
	defer c.Close()

	// subscribe to channel, and reading other's id (3 sec)
	pubsub := c.Subscribe(ctx, r.pubSubChannel)
	start := time.Now()
LISTING:
	for time.Since(start) < SubscribeTimeout {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_msg, err := pubsub.ReceiveTimeout(ctx, SubscribeTimeout)
		if err != nil {
			break LISTING
		}
		switch msg := _msg.(type) {
		case *redis.Message:
			xuuid, xid, err := parsePayload(msg.Payload)
			if err != nil {
				r.logger.Warn("failed to parse payload", "error", err)
				break
			}
			if xuuid == r.uuid {
				// other's uuid is same to myself (X_X)
				return fmt.Errorf("duplicate uuid")
			}
			r.logger.Debug("discovered other instance", "uuid", xuuid, "machine_id", xid)
			usedIds[xid] = true
		case *redis.Subscription:
		default:
			return fmt.Errorf("unknown redis message: %#v", _msg)
		}
	}

	pubsub.Unsubscribe(ctx)

LOCKING:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		candidate := make([]uint, 0, MaxCandidate)
		for i := r.min; i <= r.max; i++ {
			if usedIds[i] {
				continue
			}
			candidate = append(candidate, i)
			if len(candidate) >= MaxCandidate {
				break
			}
		}
		if len(candidate) == 0 {
			return fmt.Errorf("no more available id")
		}
		r.logger.Debug("selecting candidate machine ids", "candidates", candidate, "available_count", len(candidate))
		// pick up randomly
		id := candidate[uint(r.rand.Intn(len(candidate)))]

		// try to lock by SET NX
		r.logger.Debug("attempting to acquire machine id lock", "machine_id", id, "lock_key", r.candidateLockKey(id))
		res := c.SetNX(
			ctx,
			r.candidateLockKey(id), // key
			r.uuid,                 // value
			LockExpires,            // expiration
		)
		if err := res.Err(); err != nil {
			return fmt.Errorf("failed to get lock by SET NX: %w", err)
		}
		if res.Val() {
			r.logger.Info("machine id allocated successfully", "machine_id", id, "uuid", r.uuid, "namespace", r.namespace)
			r.id = id
			break LOCKING
		} else {
			r.logger.Debug("machine id already in use", "machine_id", id)
			usedIds[id] = true
		}
	}
	return nil
}

func parsePayload(payload string) (string, uint, error) {
	s := strings.Split(payload, ":")
	if len(s) != 2 {
		return "", 0, fmt.Errorf("unexpected data %s", payload)
	}
	id, err := strconv.ParseUint(s[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("unexpected data %s", payload)
	}
	return s[0], uint(id), nil
}

func newPayload(uuid string, id uint) string {
	return fmt.Sprintf("%s:%d", uuid, id)
}

func (r *Raus) publish(ctx context.Context) {
	c := r.redisOptions.NewClient()
	defer close(r.channel)
	defer func() {
		c.Close()
	}()

	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("shutting down machine id coordination", "machine_id", r.id, "namespace", r.namespace)
			// returns after releasing a held lock
			ctx2, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
			defer cancel()
			err := c.Del(ctx2, r.lockKey()).Err()
			if err != nil {
				r.logger.Error("failed to release machine id lock", "error", err, "lock_key", r.lockKey())
			} else {
				r.logger.Info("machine id lock released successfully", "machine_id", r.id, "lock_key", r.lockKey())
			}
			return
		case <-ticker.C:
			err := r.holdLock(ctx, c)
			if err != nil {
				r.logger.Error("machine id coordination error", "error", err, "machine_id", r.id)
				if isFatal(err) {
					r.channel <- err
					return
				}
				c.Close()
				c = r.redisOptions.NewClient()
			}
		}
	}
}

func (r *Raus) holdLock(ctx context.Context, c RedisClient) error {
	if err := c.Publish(ctx, r.pubSubChannel, newPayload(r.uuid, r.id)).Err(); err != nil {
		return fmt.Errorf("PUBLISH failed: %w", err)
	}

	pipe := c.TxPipeline()
	getset := pipe.GetSet(ctx, r.lockKey(), r.uuid)
	pipe.Expire(ctx, r.lockKey(), LockExpires)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("GETSET or EXPIRE failed: %w", err)
	}
	if v := getset.Val(); v != r.uuid {
		return fatalError{fmt.Errorf("unexpected uuid got: %s", v)}
	}
	return nil
}

func (r *Raus) lockKey() string {
	return fmt.Sprintf("%s:id:%d", r.namespace, r.id)
}

func (r *Raus) candidateLockKey(id uint) string {
	return fmt.Sprintf("%s:id:%d", r.namespace, id)
}
