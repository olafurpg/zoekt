package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestIndexArgs(t *testing.T) {
	minimal := indexArgs{
		Name:   "test/repo",
		Commit: "deadbeef",
	}
	want := []string{
		"-name", "test/repo",
		"-commit", "deadbeef",
		"-disable_ctags",
	}
	if got := minimal.CmdArgs(); !cmp.Equal(got, want) {
		t.Errorf("all mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}

	all := indexArgs{
		Name:              "test/repo",
		Commit:            "deadbeef",
		Incremental:       true,
		IndexDir:          "/data/index",
		Parallelism:       4,
		FileLimit:         123,
		Branch:            "HEAD",
		DownloadLimitMBPS: "1000",
		LargeFiles:        []string{"foo", "bar"},
		Symbols:           true,
	}
	want = []string{
		"-name", "test/repo",
		"-commit", "deadbeef",
		"-incremental",
		"-index", "/data/index",
		"-parallelism", "4",
		"-file_limit", "123",
		"-branch", "HEAD",
		"-download-limit-mbps", "1000",
		"-large_file", "foo",
		"-large_file", "bar",
		"-require_ctags",
	}
	if got := all.CmdArgs(); !cmp.Equal(got, want) {
		t.Errorf("all mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestServer_defaultArgs(t *testing.T) {
	s := &Server{
		IndexDir: "/testdata/index",
		CPUCount: 6,
	}
	want := indexArgs{
		IndexDir:          "/testdata/index",
		Parallelism:       6,
		Incremental:       true,
		Branch:            "HEAD",
		FileLimit:         1 << 20,
		DownloadLimitMBPS: "1000",
	}
	got := s.defaultArgs()
	if !cmp.Equal(got, want) {
		t.Errorf("mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestGetIndexOptions(t *testing.T) {
	var response []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(response)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]indexArgs{
		`{"Symbols": true, "LargeFiles": ["foo","bar"]}`: indexArgs{
			Symbols:    true,
			LargeFiles: []string{"foo", "bar"},
		},

		`{"Symbols": false, "LargeFiles": ["foo","bar"]}`: indexArgs{
			LargeFiles: []string{"foo", "bar"},
		},

		`{}`: indexArgs{},

		`{"Symbols": true}`: indexArgs{
			Symbols: true,
		},
	}

	for r, want := range cases {
		response = []byte(r)

		// Test we mix in the response
		want.Name = "test/repo"
		want.Commit = "deadbeef"
		got := indexArgs{
			Name:   "test/repo",
			Commit: "deadbeef",
		}

		if err := getIndexOptions(u, &got); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !cmp.Equal(got, want) {
			t.Log("response", r)
			t.Errorf("mismatch (-want +got):\n%s", cmp.Diff(want, got))
		}
	}
}

func TestListRepos(t *testing.T) {
	var gotBody string
	var gotURL *url.URL
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL

		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(b)

		_, err = w.Write([]byte(`[{"uri":"foo"}, {"uri":"bar"}, {"uri":"baz"}]`))
		if err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	gotRepos, err := listRepos("test-indexed-search-1", u)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"foo", "bar", "baz"}; !reflect.DeepEqual(gotRepos, want) {
		t.Fatalf("unexpected repos. got %v, want %v", gotRepos, want)
	}
	if want := `{"Hostname":"test-indexed-search-1","Enabled":true,"Index":true}`; gotBody != want {
		t.Fatalf("unexpected request body. got %q, want %q", gotBody, want)
	}
	if want := "/.internal/repos/list"; gotURL.Path != want {
		t.Fatalf("unexpected request path. got %q, want %q", gotURL.Path, want)
	}
}

func TestPing(t *testing.T) {
	var response []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.internal/ping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("service") != "gitserver" {
			http.Error(w, "expected service gitserver in request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(response)
	}))
	defer server.Close()

	root, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Ping fails
	response = []byte("hello")
	err = ping(root)
	if got, want := fmt.Sprintf("%v", err), "did not receive pong"; !strings.Contains(got, want) {
		t.Errorf("wanted ping to fail,\ngot:  %q\nwant: %q", got, want)
	}

	response = []byte("pong")
	err = ping(root)
	if err != nil {
		t.Errorf("wanted ping to succeed, got: %v", err)
	}

	// We expect waitForFrontend to just work now
	done := make(chan struct{})
	go func() {
		waitForFrontend(root)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForFrontend blocking")
	}
}
