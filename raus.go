package raus

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/satori/go.uuid"
	"gopkg.in/redis.v5"
)

type Raus struct {
	rand          *rand.Rand
	uuid          string
	id            int
	min           int
	max           int
	redisOptions  *redis.Options
	namespace     string
	pubSubChannel string
	channel       chan error
}

const (
	ErrorID             = -1
	DefaultNamespace    = "raus"
	pubSubChannelSuffix = ":broadcast"
)

var MaxCandidate = 10

// New creates *Raus object.
func New(redisURI string, min, max int) (*Raus, error) {
	var s int64
	if err := binary.Read(crand.Reader, binary.LittleEndian, &s); err != nil {
		s = time.Now().UnixNano()
	}
	if min < 0 {
		return nil, errors.New("min should be greater than or equal to 0")
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

func (r *Raus) size() int {
	return r.max - r.min
}

func (r *Raus) raiseError(err error) {
	r.channel <- err
}

// Get gets unique id ranged between min and max.
func (r *Raus) Get(ctx context.Context) (int, chan error, error) {
	go r.subscribe(ctx)
	err := <-r.channel
	if err != nil {
		return ErrorID, nil, err
	}
	go r.publish(ctx)
	return r.id, r.channel, nil
}

func (r *Raus) subscribe(ctx context.Context) {
	// table for looking up unused id
	usedIds := make(map[int]bool, r.size())

	c := redis.NewClient(r.redisOptions)
	defer c.Close()

	// subscribe to channel, and reading other's id (3 sec)
	pubsub, err := c.Subscribe(r.pubSubChannel)
	if err != nil {
		r.raiseError(err)
		return
	}
	timeout := 3 * time.Second
	start := time.Now()
LISTING:
	for time.Since(start) < timeout {
		select {
		case <-ctx.Done():
			r.raiseError(errors.New("canceled"))
			return
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
				r.raiseError(errors.New("duplicate uuid"))
				return
			}
			log.Printf("xuuid:%s xid:%d", xuuid, xid)
			usedIds[xid] = true
		case *redis.Subscription:
		default:
			r.raiseError(fmt.Errorf("unknown redis message: %#v", _msg))
			return
		}
	}

	pubsub.Unsubscribe()
	if ctx.Err() != nil {
		r.raiseError(errors.New("canceled"))
		return
	}

LOCKING:
	for {
		candidate := make([]int, 0, MaxCandidate)
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
			r.raiseError(errors.New("no more available id"))
			return
		}
		log.Printf("candidate ids: %v", candidate)
		// pick up randomly
		id := candidate[r.rand.Intn(len(candidate))]

		// try to lock by SET NX
		log.Println("trying to get lock key", r.candidateLockKey(id))
		res := c.SetNX(
			r.candidateLockKey(id), // key
			r.uuid,                 // value
			60*time.Second,         // expiration
		)
		if err := res.Err(); err != nil {
			r.raiseError(err)
			return
		}
		if res.Val() {
			log.Println("got lock for", id)
			r.id = id
			r.channel <- nil // success!
			break LOCKING
		} else {
			log.Println("could not get lock for", id)
			usedIds[id] = true
		}
	}

	pubsub, err = c.Subscribe(r.pubSubChannel)
	if err != nil {
		r.raiseError(err)
	}
WATCHING:
	for {
		msg, err := pubsub.ReceiveMessage()
		if err != nil {
			log.Println(err)
			continue WATCHING
		}
		xuuid, xid, err := parsePayload(msg.Payload)
		if err != nil {
			log.Println(err)
			continue WATCHING
		}
		if xid == r.id && xuuid != r.uuid {
			log.Printf("duplicate id %d from %s", xid, xuuid)
			r.raiseError(errors.New("duplicate id detected"))
			return
		}
	}
}

func parsePayload(payload string) (string, int, error) {
	s := strings.Split(payload, ":")
	if len(s) != 2 {
		return "", 0, fmt.Errorf("unexpected data %s", payload)
	}
	id, err := strconv.Atoi(s[1])
	if err != nil {
		return "", 0, fmt.Errorf("unexpected data %s", payload)
	}
	return s[0], id, nil
}

func newPayload(uuid string, id int) string {
	return fmt.Sprintf("%s:%d", uuid, id)
}

func (r *Raus) publish(ctx context.Context) {
	c := redis.NewClient(r.redisOptions)
	defer c.Close()

	ticker := time.NewTicker(1 * time.Second)
	payload := newPayload(r.uuid, r.id)
TICKER:
	for range ticker.C {
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
		default:
			err := c.Publish(r.pubSubChannel, payload).Err()
			if err != nil {
				log.Println(err)
				continue TICKER
			}
			// update expiration
			err = c.Set(r.lockKey(), r.uuid, 60*time.Second).Err()
			if err != nil {
				log.Println(err)
				continue TICKER
			}
		}
	}
}

func (r *Raus) lockKey() string {
	return fmt.Sprintf("%s:id:%d", r.namespace, r.id)
}

func (r *Raus) candidateLockKey(id int) string {
	return fmt.Sprintf("%s:id:%d", r.namespace, id)
}
