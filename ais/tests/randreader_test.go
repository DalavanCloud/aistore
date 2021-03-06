// Package ais_test contains AIS integration tests.
/*
 * Copyright (c) 2018, NVIDIA CORPORATION. All rights reserved.
 */
package ais_test

import (
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NVIDIA/aistore/api"
	"github.com/NVIDIA/aistore/tutils"
)

func TestRandomReaderPutStress(t *testing.T) {
	var (
		numworkers = 1000
		numobjects = 10 // NOTE: increase this number if need be ...
		bucket     = "RRTestBucket"
		proxyURL   = getPrimaryURL(t, proxyURLReadOnly)
		wg         = &sync.WaitGroup{}
		dir        = t.Name()
	)
	tutils.CreateFreshLocalBucket(t, proxyURL, bucket)
	for i := 0; i < numworkers; i++ {
		reader, err := tutils.NewRandReader(fileSize, true)
		tutils.CheckFatal(err, t)
		wg.Add(1)
		go func(workerId int) {
			putRR(t, workerId, proxyURL, reader, bucket, dir, numobjects)
			wg.Done()
		}(i)
	}
	wg.Wait()
	tutils.DestroyLocalBucket(t, proxyURL, bucket)
}

func putRR(t *testing.T, id int, proxyURL string, reader tutils.Reader, bucket, dir string, numobjects int) {
	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numobjects; i++ {
		fname := tutils.FastRandomFilename(random, fnlen)
		objname := filepath.Join(dir, fname)
		putArgs := api.PutObjectArgs{
			BaseParams: tutils.DefaultBaseAPIParams(t),
			Bucket:     bucket,
			Object:     objname,
			Hash:       reader.XXHash(),
			Reader:     reader,
		}
		err := api.PutObject(putArgs)
		tutils.CheckFatal(err, t)
	}
}
