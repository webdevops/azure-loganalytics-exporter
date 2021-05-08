package main

import (
	"github.com/remeh/sizedwaitgroup"
	"net/http"
	"sync"
)

func NewWaitGroup() sync.WaitGroup {
	return sync.WaitGroup{}
}

func NewWaitGroupWithSize(r *http.Request) sizedwaitgroup.SizedWaitGroup {
	size := opts.Loganalytics.Parallel
	return sizedwaitgroup.New(size)
}
