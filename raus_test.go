package raus_test

import (
	"context"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fujiwara/raus"
	"github.com/google/go-cmp/cmp"
	redistest "github.com/soh335/go-test-redisserver"
)

var invalidMinMaxSet = [][]uint{
	{0, 0},
	{2, 1},
}

var redisURL = "redis://localhost:26379"

func init() {
	if u := os.Getenv("REDIS_URL"); u != "" {
		log.Println("REDIS_URL=", u)
		redisURL = u
	}
}

type parseTest struct {
	URI       string
	Opt       *raus.RedisOptions
	Namespace string
}

var parseTestErrorSet = []string{
	"http://example.com",
	"redis:///var/tmp/test.sock",
	"localhost:6379",
	"localhost",
	"rediscluster://127.0.0.1/3",
}

var parseTestSet = []parseTest{
	{
		"redis://localhost:6379",
		&raus.RedisOptions{
			Addrs: []string{"localhost:6379"},
			DB:    0,
		},
		raus.DefaultNamespace,
	},
	{
		"redis://127.0.0.1/2?ns=foo",
		&raus.RedisOptions{
			Addrs: []string{"127.0.0.1:6379"},
			DB:    3,
		},
		"foo",
	},
	{
		"rediscluster://127.0.0.1/?ns=foo",
		&raus.RedisOptions{
			Cluster: true,
			Addrs:   []string{"127.0.0.1:6379"},
		},
		"foo",
	},
	{
		"rediscluster://127.0.0.1:6380/?ns=foo",
		&raus.RedisOptions{
			Cluster: true,
			Addrs:   []string{"127.0.0.1:6380"},
		},
		"foo",
	},
}

func TestMain(m *testing.M) {
	if os.Getenv("REDIS_URL") == "" {
		conf := redistest.Config{"port": "26379", "save": ""}
		s, err := redistest.NewServer(true, conf)
		if err != nil {
			panic(err)
		}
		code := m.Run()
		s.Stop()
		os.Exit(code)
	} else {
		os.Exit(m.Run())
	}
}

func TestParseRedisURI(t *testing.T) {
	for _, ts := range parseTestSet {
		opt, ns, err := raus.ParseRedisURI(ts.URI)
		t.Logf("uri %s parsed to %#v %s", ts.URI, opt, ns)
		if err != nil {
			t.Error(err)
		}
		if diff := cmp.Diff(opt, ts.Opt); diff != "" {
			t.Error("unexpected options", diff)
		}
		if ns != ts.Namespace {
			t.Errorf("invalid Namespace %s expected %s", ns, ts.Namespace)
		}
	}

	for _, ts := range parseTestErrorSet {
		opt, ns, err := raus.ParseRedisURI(ts)
		t.Logf("uri %s parsed to %#v %s", ts, opt, ns)
		if err == nil {
			t.Errorf("invalid uri %s should be parse error.", ts)
		}
		//	t.Logf("uri %s parse error: %s", s, err)
	}
}

func TestNew(t *testing.T) {
	for _, s := range invalidMinMaxSet {
		_, err := raus.New("redis://localhost:6379", s[0], s[1])
		if err != nil {
			t.Logf("[min max]=%v returns error:%s", s, err)
		} else {
			t.Errorf("[min max]=%v must return error", s)
		}
	}
}

func ExampleNew() {
	// prepere context
	ctx, cancel := context.WithCancel(context.Background())

	r, _ := raus.New(redisURL, 0, 3)
	id, ch, _ := r.Get(ctx)
	log.Printf("Got id %d", id)

	// watch error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err, more := <-ch
		if !more {
			// raus shutdown successfully
			return
		} else {
			// fatal error
			panic(err)
		}
	}()

	// Run your application code

	// notify shutdown
	cancel()

	wg.Wait()
}

func TestGet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := raus.New(redisURL, 0, 3)
	if err != nil {
		t.Error(err)
	}
	id, ch, err := r.Get(ctx)
	if err != nil {
		t.Error(err)
		t.Fail()
		return
	}
	log.Printf("Got id %d", id)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err, more := <-ch
		if !more {
			return
		}
		t.Error(err)
	}()
	time.Sleep(time.Second)
	cancel()
	wg.Wait()
}

func TestGetRace(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i <= 5; i++ {
		wg.Add(1)
		time.Sleep(500 * time.Millisecond)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			r, err := raus.New(redisURL, 0, 5)
			if err != nil {
				t.Error(err)
			}
			id, ch, err := r.Get(ctx)
			if err != nil {
				t.Error(err)
			}
			log.Printf("Got id %d", id)
			wg.Add(1)
			go func() {
				defer wg.Done()
				err, more := <-ch
				if !more {
					return
				}
				t.Error(err)
			}()
			time.Sleep(5 * time.Second)
			cancel()
		}(i)
	}
	wg.Wait()
}
