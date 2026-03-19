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
			DB:    2,
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

func TestCandidatesSpreadAcrossRange(t *testing.T) {
	// Do not call t.Parallel(): this test mutates the package-level SubscribeTimeout
	// variable, which would race with other tests that read it concurrently.
	orig := raus.SubscribeTimeout
	raus.SubscribeTimeout = 0
	t.Cleanup(func() { raus.SubscribeTimeout = orig })

	const N = 20
	const rangeMax = uint(1023)
	// Use a dedicated namespace to avoid interference with other tests.
	spreadURL := redisURL + "?ns=candidate_spread"

	ids := make([]uint, 0, N)
	chs := make([]chan error, 0, N)
	cancels := make([]context.CancelFunc, 0, N)

	for range N {
		ctx, cancel := context.WithCancel(context.Background())
		r, err := raus.New(spreadURL, 0, rangeMax)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		id, ch, err := r.Get(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		ids = append(ids, id)
		chs = append(chs, ch)
		cancels = append(cancels, cancel)
	}

	// Compute max before cleanup.
	maxID := uint(0)
	for _, id := range ids {
		if id > maxID {
			maxID = id
		}
	}

	// Clean up all instances and wait for locks to be released.
	var wg sync.WaitGroup
	for i := range cancels {
		cancels[i]()
		wg.Add(1)
		go func(ch chan error) {
			defer wg.Done()
			err, more := <-ch
			if !more {
				return
			}
			t.Error(err)
		}(chs[i])
	}
	wg.Wait()

	// With random candidate offset, sequential allocations spread across the full range.
	// Without randomization, all N IDs fall in [0, N-1] since candidates always start
	// from min. For N=20 random draws from [0, 1023], P(max < 50) is negligible.
	if maxID < 50 {
		t.Errorf("max allocated ID is %d, expected > 50: "+
			"candidates may not be starting from a random offset (IDs: %v)", maxID, ids)
	}
}

func TestGetWithoutDiscovery(t *testing.T) {
	// Do not call t.Parallel(): this test mutates the package-level SubscribeTimeout
	// variable, which would race with other tests that read it concurrently.
	// Skip Discovery by setting SubscribeTimeout to 0.
	orig := raus.SubscribeTimeout
	raus.SubscribeTimeout = 0
	t.Cleanup(func() { raus.SubscribeTimeout = orig })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := raus.New(redisURL, 0, 3)
	if err != nil {
		t.Fatal(err)
	}

	// With Discovery skipped, Get() should complete well under SubscribeTimeout (3s default).
	start := time.Now()
	id, ch, err := r.Get(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("Get() took %v, expected < 2s (Discovery should be skipped)", elapsed)
	}
	t.Logf("Got id %d in %v", id, elapsed)

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
