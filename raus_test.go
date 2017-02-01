package raus_test

import (
	"testing"

	"github.com/fujiwara/raus"
)

var invalidMinMaxSet = [][]int{
	[]int{-1, 1},
	[]int{0, 0},
	[]int{2, 1},
}

func TestNew(t *testing.T) {
	for _, s := range invalidMinMaxSet {
		_, err := raus.New("localhost:6379", s[0], s[1])
		if err != nil {
			t.Logf("[min max]=%v returns error:%s", s, err)
		} else {
			t.Errorf("[min max]=%v must return error", s)
		}
	}
}
