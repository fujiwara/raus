package raus

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	stdlog "log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/satori/go.uuid"
	"gopkg.in/redis.v5"
)

type Raus struct {
	rand          *rand.Rand
	uuid          string
	id            uint
	min           uint
	max           uint
	redisOptions  *redis.Options
	namespace     string
	pubSubChannel string
	channel       chan error
}

const (
	DefaultNamespace    = "raus"
	pubSubChannelSuffix = ":broadcast"
)

var (
	MaxCandidate = 10
	LockExpires  = 60 * time.Second
	log          Logger
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

// New creates *Raus object.
func New(redisURI string, min, max uint) (*Raus, error) {
	var s int64
	if err := binary.Read(crand.Reader, binary.LittleEndian, &s); err != nil {
		s = time.Now().UnixNano()
	}
	if min >= max {
		return nil, errors.New("max should be greater than min")
	}
	op, ns, err := ParseRedisURI(redisURI)
	if err != nil {
		return nil, err
	}

	return &Raus{
		rand:          rand.New(rand.NewSource(s)),
		uuid:          uuid.NewV4().String(),
		min:           min,
		max:           max,
		redisOptions:  op,
		namespace:     ns,
		pubSubChannel: ns + pubSubChannelSuffix,
		channel:       make(chan error, 0),
	}, nil
}

// ParseRedisURI parses uri for redis (redis://host:port/db?ns=namespace)
func ParseRedisURI(s string) (*redis.Options, string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "redis" {
		return nil, "", errors.New("invalid scheme")
	}
	op := &redis.Options{}
	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		h = u.Host
		p = "6379"
	}
	op.Network = "tcp"
	op.Addr = h + ":" + p
	if u.Path == "" || u.Path == "/" {
		op.DB = 0
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

	c := redis.NewClient(r.redisOptions)
	defer c.Close()

	// subscribe to channel, and reading other's id (3 sec)
	pubsub, err := c.Subscribe(r.pubSubChannel)
	if err != nil {
		return err
	}
	timeout := 3 * time.Second
	start := time.Now()
LISTING:
	for time.Since(start) < timeout {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_msg, err := pubsub.ReceiveTimeout(timeout)
		if err != nil {
			break LISTING
		}
		switch msg := _msg.(type) {
		case *redis.Message:
			xuuid, xid, err := parsePayload(msg.Payload)
			if err != nil {
				log.Println(err)
				break
			}
			if xuuid == r.uuid {
				// other's uuid is same to myself (X_X)
				return errors.New("duplicate uuid")
			}
			log.Printf("xuuid:%s xid:%d", xuuid, xid)
			usedIds[xid] = true
		case *redis.Subscription:
		default:
			return fmt.Errorf("unknown redis message: %#v", _msg)
		}
	}

	pubsub.Unsubscribe()

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
			return errors.New("no more available id")
		}
		log.Printf("candidate ids: %v", candidate)
		// pick up randomly
		id := candidate[uint(r.rand.Intn(len(candidate)))]

		// try to lock by SET NX
		log.Println("trying to get lock key", r.candidateLockKey(id))
		res := c.SetNX(
			r.candidateLockKey(id), // key
			r.uuid,                 // value
			LockExpires,            // expiration
		)
		if err := res.Err(); err != nil {
			return err
		}
		if res.Val() {
			log.Println("got lock for", id)
			r.id = id
			break LOCKING
		} else {
			log.Println("could not get lock for", id)
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
	c := redis.NewClient(r.redisOptions)
	defer close(r.channel)
	defer func() {
		c.Close()
	}()

	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down")
			// returns after releasing a held lock
			err := c.Del(r.lockKey()).Err()
			if err != nil {
				log.Println(err)
			} else {
				log.Printf("remove a lock key %s successfully", r.lockKey())
			}
			return
		case <-ticker.C:
			err := r.holdLock(c)
			if err != nil {
				log.Println(err)
				if isFatal(err) {
					r.channel <- err
					return
				}
				c.Close()
				c = redis.NewClient(r.redisOptions)
			}
		}
	}
}

func (r *Raus) holdLock(c *redis.Client) error {
	if err := c.Publish(r.pubSubChannel, newPayload(r.uuid, r.id)).Err(); err != nil {
		return errors.Wrap(err, "PUBLISH failed")
	}

	res, err := c.GetSet(r.lockKey(), r.uuid).Result()
	if err != nil {
		return errors.Wrap(err, "GETSET failed")
	}
	if res != r.uuid {
		return fatalError{fmt.Errorf("unexpected uuid got: %s", res)}
	}

	if err := c.Expire(r.lockKey(), LockExpires).Err(); err != nil {
		return errors.Wrap(err, "EXPIRE failed")
	}
	return nil
}

func (r *Raus) lockKey() string {
	return fmt.Sprintf("%s:id:%d", r.namespace, r.id)
}

func (r *Raus) candidateLockKey(id uint) string {
	return fmt.Sprintf("%s:id:%d", r.namespace, id)
}
