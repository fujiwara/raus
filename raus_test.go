package raus_test

import (
	"testing"

	"github.com/fujiwara/raus"
	"gopkg.in/redis.v5"
)

var invalidMinMaxSet = [][]int{
	[]int{-1, 1},
	[]int{0, 0},
	[]int{2, 1},
}

type parseTest struct {
	URI       string
	Opt       *redis.Options
	Namespace string
}

var parseTestErrorSet = []string{
	"http://example.com",
	"redis:///var/tmp/test.sock",
	"localhost:6379",
	"localhost",
}

var parseTestSet = []parseTest{
	parseTest{
		"redis://localhost:6379",
		&redis.Options{
			Addr: "localhost:6379",
			DB:   0,
		},
		raus.DefaultNamespace,
	},
	parseTest{
		"redis://127.0.0.1/2?ns=foo",
		&redis.Options{
			Addr: "127.0.0.1:6379",
			DB:   2,
		},
		"foo",
	},
}

func TestParseRedisURI(t *testing.T) {
	for _, ts := range parseTestSet {
		opt, ns, err := raus.ParseRedisURI(ts.URI)
		t.Logf("uri %s parsed to %#v %s", ts.URI, opt, ns)
		if err != nil {
			t.Error(err)
		}
		if opt.Addr != ts.Opt.Addr {
			t.Errorf("invalid Addr %s expected %s", opt.Addr, ts.Opt.Addr)
		}
		if opt.DB != ts.Opt.DB {
			t.Errorf("invalid DB %d expected %d", opt.DB, ts.Opt.DB)
		}
		if ns != ts.Namespace {
			t.Errorf("invalid Namespace %s expected %s", ns, ts.Namespace)
		}
	}

	for _, s := range parseTestErrorSet {
		_, _, err := raus.ParseRedisURI(s)
		if err == nil {
			t.Errorf("invalid uri %s should be parse error.", s)
		}
		t.Logf("uri %s parse error: %s", s, err)
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
