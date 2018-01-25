// Package dfc provides distributed file-based cache with Amazon and Google Cloud backends._test
/*
 * Copyright (c) 2017, NVIDIA CORPORATION. All rights reserved.
 *
 */
package dfc_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof" // profile
	"os"
	"regexp"
	"strconv"
	"sync"
	"testing"

	"github.com/NVIDIA/dfcpub/dfc"
)

// commandline examples:
// # go test -v -run=down -args -numfiles=10
// # go test -v -run=down -args -bucket=mybucket
// # go test -v -run=down -args -bucket=mybucket -numworkers 5
// # go test -v -run=list
// # go test -v -run=xxx -bench . -count 10

const (
	LocalRootDir    = "/tmp/iocopy"           // client-side download destination
	ProxyURL        = "http://localhost:8080" // assuming local proxy is listening on 8080
	TargetURL       = "http://localhost:8081" // assuming local target is listening on 8081
	RestAPIGet      = ProxyURL + "/v1/files"  // version = 1, resource = files
	RestAPIProxyPut = ProxyURL + "/v1/files"  // version = 1, resource = files
	RestAPITgtPut   = TargetURL + "/v1/files" // version = 1, resource = files
	TestFile        = "/tmp/xattrfile"        // Test file for setting and getting xattr.
)

const (
	roleproxy  = "proxy"
	roletarget = "target"
)

// globals
var (
	clibucket  string
	numfiles   int
	numworkers int
	match      string
	role       string
)

// worker's result
type workres struct {
	totfiles int
	totbytes int64
}

func init() {
	flag.StringVar(&clibucket, "bucket", "shri-new", "AWS or GCP bucket")
	flag.IntVar(&numfiles, "numfiles", 100, "Number of the files to download")
	flag.IntVar(&numworkers, "numworkers", 10, "Number of the workers")
	flag.StringVar(&match, "match", ".*", "regex match for the keyname")
	flag.StringVar(&role, "role", "proxy", "proxy or target")
}

func Test_download(t *testing.T) {
	flag.Parse()

	// Declare one channel per worker to pass the keyname
	keyname_chans := make([]chan string, numworkers)
	result_chans := make([]chan workres, numworkers)

	for i := 0; i < numworkers; i++ {
		// Allow a bunch of messages at a time to be written asynchronously to a channel
		keyname_chans[i] = make(chan string, 100)

		// Initialize number of files downloaded
		result_chans[i] = make(chan workres, 100)
	}

	// Start the worker pools
	errch := make(chan error, 100)

	var wg = &sync.WaitGroup{}
	// Get the workers started
	for i := 0; i < numworkers; i++ {
		wg.Add(1)
		// false: read the response and drop it, true: write it to a file
		go getAndCopyTmp(i, keyname_chans[i], t, wg, true, errch, result_chans[i], clibucket)
	}

	// list the bucket
	var msg = &dfc.GetMsg{}
	jsbytes, err := json.Marshal(msg)
	if err != nil {
		t.Errorf("Unexpected json-marshal failure, err: %v", err)
		return
	}
	reslist := listbucket(t, clibucket, jsbytes)
	if reslist == nil {
		return
	}
	re, rerr := regexp.Compile(match)
	if testfail(rerr, fmt.Sprintf("Invalid match expression %s", match), nil, nil, t) {
		return
	}
	// match
	var num int
	for _, entry := range reslist.Entries {
		name := entry.Name
		if !re.MatchString(name) {
			continue
		}
		keyname_chans[num%numworkers] <- name
		if num++; num >= numfiles {
			break
		}
	}
	t.Logf("Expecting to get %d files", num)

	// Close the channels after the reading is done
	for i := 0; i < numworkers; i++ {
		close(keyname_chans[i])
	}

	wg.Wait()

	// Now find the total number of files and data downloaed
	var sumtotfiles int = 0
	var sumtotbytes int64 = 0
	for i := 0; i < numworkers; i++ {
		res := <-result_chans[i]
		sumtotbytes += res.totbytes
		sumtotfiles += res.totfiles
		t.Logf("Worker #%d: %d files, size %.2f MB (%d B)",
			i, res.totfiles, float64(res.totbytes/1000/1000), res.totbytes)
	}
	t.Logf("\nSummary: %d workers, %d files, total size %.2f MB (%d B)",
		numworkers, sumtotfiles, float64(sumtotbytes/1000/1000), sumtotbytes)

	if sumtotfiles != num {
		s := fmt.Sprintf("Not all files downloaded. Expected: %d, Downloaded:%d", num, sumtotfiles)
		t.Error(s)
		if errch != nil {
			errch <- errors.New(s)
		}
	}
	select {
	case <-errch:
		t.Fail()
	default:
	}
}

func getAndCopyTmp(id int, keynames <-chan string, t *testing.T, wg *sync.WaitGroup, copy bool,
	errch chan error, resch chan workres, bucket string) {
	var md5sum, errstr string
	res := workres{0, 0}
	defer wg.Done()

	for keyname := range keynames {
		url := RestAPIGet + "/" + bucket + "/" + keyname
		t.Logf("Worker %2d: GET %q", id, url)
		r, err := http.Get(url)
		if testfail(err, fmt.Sprintf("Worker %2d: get key %s from bucket %s", id, keyname, bucket), r, errch, t) {
			return
		}
		defer func() {
			if r != nil {
				r.Body.Close()
			}
		}()
		if !copy {
			md5sum, errstr = dfc.CalculateMD5(r.Body)
			if errstr != "" {
				t.Errorf("Worker %2d: Failed to calculate MD5sum, err: %v", id, errstr)
				return
			}
			bufreader := bufio.NewReader(r.Body)
			bytes, err := dfc.ReadToNull(bufreader)
			if err != nil {
				t.Errorf("Worker %2d: Failed to read http response, err: %v", id, err)
				return
			}
			t.Logf("Worker %2d: Downloaded %q (size %.2f MB)", id, url, float64(bytes)/1000/1000)
			return
		}

		// alternatively, create a local copy
		fname := LocalRootDir + "/" + keyname
		written, err := dfc.ReceiveFile(fname, r.Body, md5sum)
		r.Body.Close()
		if err != nil {
			t.Errorf("Worker %2d: Failed to write to file, err: %v", id, err)
			return
		} else {
			res.totfiles += 1
			res.totbytes += written
		}
	}
	// Send information back
	resch <- res
	close(resch)
}

func testfail(err error, str string, r *http.Response, errch chan error, t *testing.T) bool {
	if err != nil {
		if match, _ := regexp.MatchString("connection refused", err.Error()); match {
			t.Fatalf("http connection refused - terminating")
		}
		s := fmt.Sprintf("Failed %s, err: %v", str, err)
		t.Error(s)
		if errch != nil {
			errch <- errors.New(s)
		}
		return true
	}
	if r != nil && r.StatusCode >= http.StatusBadRequest {
		s := fmt.Sprintf("Failed %s, http status %d", str, r.StatusCode)
		t.Error(s)
		if errch != nil {
			errch <- errors.New(s)
		}
		return true
	}
	return false
}

func Benchmark_one(b *testing.B) {
	var wg = &sync.WaitGroup{}
	errch := make(chan error, 100)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		keyname := "dir" + strconv.Itoa(i%3+1) + "/a" + strconv.Itoa(i)
		go get(keyname, b, wg, errch, clibucket)
	}
	wg.Wait()
	select {
	case <-errch:
		b.Fail()
	default:
	}
}

func get(keyname string, b *testing.B, wg *sync.WaitGroup, errch chan error, bucket string) {
	defer wg.Done()
	url := RestAPIGet + "/" + bucket + "/" + keyname
	r, err := http.Get(url)
	defer func() {
		if r != nil {
			r.Body.Close()
		}
	}()
	if r != nil && r.StatusCode >= http.StatusBadRequest {
		s := fmt.Sprintf("Failed to get key %s from bucket %s, http status %d", keyname, bucket, r.StatusCode)
		b.Error(s)
		if errch != nil {
			errch <- errors.New(s)
		}
		return
	}
	if err != nil {
		b.Error(err.Error())
		if errch != nil {
			errch <- err
		}
		return
	}
	bufreader := bufio.NewReader(r.Body)
	if _, err = dfc.ReadToNull(bufreader); err != nil {
		b.Errorf("Failed to read http response, err: %v", err)
	}
}

func Test_list(t *testing.T) {
	flag.Parse()

	// list the names, sizes, creation times and MD5 checksums
	var msg = &dfc.GetMsg{GetProps: dfc.GetPropsSize + ", " + dfc.GetPropsCtime + ", " + dfc.GetPropsChecksum}
	jsbytes, err := json.Marshal(msg)
	if err != nil {
		t.Errorf("Unexpected json-marshal failure, err: %v", err)
		return
	}
	bucket := clibucket
	var copy bool
	// copy = true

	reslist := listbucket(t, bucket, jsbytes)
	if reslist == nil {
		return
	}
	if !copy {
		for _, m := range reslist.Entries {
			fmt.Fprintf(os.Stdout, "%s %d %s %s\n", m.Name, m.Size, m.Ctime, m.Checksum[:8]+"...")
		}
		return
	}
	// alternatively, write to a local filename = bucket
	fname := LocalRootDir + "/" + bucket
	if err := dfc.CreateDir(LocalRootDir); err != nil {
		t.Errorf("Failed to create dir %s, err: %v", LocalRootDir, err)
		return
	}
	file, err := os.Create(fname)
	if err != nil {
		t.Errorf("Failed to create file %s, err: %v", fname, err)
		return
	}
	for _, m := range reslist.Entries {
		fmt.Fprintln(file, m)
	}
	t.Logf("ls bucket %s written to %s", bucket, fname)
}

func listbucket(t *testing.T, bucket string, injson []byte) *dfc.BucketList {
	var (
		url     = RestAPIGet + "/" + bucket
		err     error
		request *http.Request
		r       *http.Response
	)
	t.Logf("LIST %q", url)
	if injson == nil || len(injson) == 0 {
		r, err = http.Get(url)
	} else {
		request, err = http.NewRequest("GET", url, bytes.NewBuffer(injson))
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
			r, err = http.DefaultClient.Do(request)
		}
	}
	if err != nil {
		t.Errorf("Failed to GET %s, err: %v", url, err)
		return nil
	}
	if testfail(err, fmt.Sprintf("list bucket %s", bucket), r, nil, t) {
		return nil
	}
	defer func() {
		if r != nil {
			r.Body.Close()
		}
	}()
	var reslist = &dfc.BucketList{}
	reslist.Entries = make([]*dfc.BucketEntry, 0, 1000)
	b, err := ioutil.ReadAll(r.Body)
	if err == nil {
		err = json.Unmarshal(b, reslist)
		if err != nil {
			t.Errorf("Failed to json-unmarshal, err: %v", err)
			return nil
		}
	} else {
		t.Errorf("Failed to read json, err: %v", err)
		return nil
	}
	return reslist
}

func Test_xattr(t *testing.T) {
	f := TestFile
	file, err := dfc.Createfile(f)
	if err != nil {
		t.Errorf("Failed to create file %s, err:%v", f, err)
		return
	}
	// Set objstate to valid
	errstr := dfc.Setxattr(f, dfc.Objstateattr, []byte(dfc.XAttrInvalid))
	if errstr != "" {
		t.Errorf("Unable to set xattr %s to file %s, err: %v",
			dfc.Objstateattr, f, errstr)
		_ = os.Remove(f)
		file.Close()
		return
	}
	// Check if xattr got set correctly.
	data, errstr := dfc.Getxattr(f, dfc.Objstateattr)
	if string(data) == dfc.XAttrInvalid && errstr == "" {
		t.Logf("Successfully got file %s attr %s value %v",
			f, dfc.Objstateattr, data)
	} else {
		t.Errorf("Failed to get file %s attr %s value %v, err %v",
			f, dfc.Objstateattr, data, errstr)
		_ = os.Remove(f)
		file.Close()
		return
	}
	t.Logf("Successfully set and retrieved xattr")
	_ = os.Remove(f)
	file.Close()
}
func Test_put(t *testing.T) {
	flag.Parse()
	var wg = &sync.WaitGroup{}
	errch := make(chan error, 100)
	for i := 0; i < numfiles; i++ {
		wg.Add(1)
		keyname := "dir" + strconv.Itoa(i%3+1) + "/a" + strconv.Itoa(i)
		fname := "/" + keyname
		go put(fname, role, clibucket, keyname, t, wg, errch)
	}
	wg.Wait()
	select {
	case <-errch:
		t.Fail()
	default:
	}
}

func put(fname, role, bucket string, keyname string, t *testing.T, wg *sync.WaitGroup, errch chan error) {
	var tgturl, errstr string
	defer wg.Done()
	if role == roletarget {
		tgturl = RestAPITgtPut + "/" + bucket + "/" + keyname
	} else {
		proxyurl := RestAPIProxyPut + "/" + bucket + "/" + keyname
		t.Logf("Proxy PUT %q", proxyurl)
		tgturl, errstr = sendhttprq(fname, proxyurl, role)
		if errstr != "" {
			goto puterr
		}
		role = roletarget
	}
	_, errstr = sendhttprq(fname, tgturl, role)
	if errstr != "" {
		goto puterr
	}
	return
puterr:
	t.Error(errstr)
	if errch != nil {
		errch <- errors.New(errstr)
	}
	return
}

func sendhttprq(fname string, rqurl string, role string) (url string, errstr string) {
	file, err := os.Open(fname)
	if err != nil {
		errstr = fmt.Sprintf("Failed to open file %s, err: %v", fname, err)
		return "", errstr
	}
	defer file.Close()
	client := &http.Client{}
	req, err := http.NewRequest(http.MethodPut, rqurl, file)
	if err != nil {
		errstr = fmt.Sprintf("Failed to create new http request, err: %v", err)
		return "", errstr
	}
	// Calculate MD5 only for target nodes
	if role == roletarget {
		md5, errstr1 := dfc.CalculateMD5(file)
		if errstr1 != "" {
			errstr = fmt.Sprintf("Failed to calculate MD5 sum for file %s", fname)
			return "", errstr
		}
		req.Header.Set("Content-MD5", md5)
		_, err = file.Seek(0, 0)
		if err != nil {
			errstr = fmt.Sprintf("Failed to seek file %s, err: %v", fname, err)
			return "", errstr
		}
	}
	r, err := client.Do(req)
	if r != nil {
		returl, err := ioutil.ReadAll(r.Body)
		if err != nil {
			errstr = fmt.Sprintf("Failed to read response content, err %v", err)
			url = ""
			r.Body.Close()
			return
		}
		r.Body.Close()
		if role == roleproxy {
			url = string(returl)
		} else {
			url = ""
		}
		errstr = ""
	} else {
		errstr = fmt.Sprintf("Failed to get proxy put response, err %v", err)
		url = ""
	}
	return url, errstr
}
