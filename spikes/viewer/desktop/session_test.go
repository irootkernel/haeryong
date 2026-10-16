package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type sessionRoundTripper func(*http.Request) (*http.Response, error)

func (f sessionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeSessionPreservesRememberedAccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		transport bool
		wantPair  bool
	}{
		{"valid", 200, false, false}, {"denied", 401, false, true}, {"unavailable", 503, false, false}, {"forbidden", 403, false, false}, {"transport", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			file := filepath.Join(state, "native-session")
			original := []byte("synthetic-native-owner\n")
			if err := os.WriteFile(file, original, 0600); err != nil {
				t.Fatal(err)
			}
			previous := http.DefaultClient
			defer func() { http.DefaultClient = previous }()
			var calls []string
			http.DefaultClient = &http.Client{Transport: sessionRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				code := tc.code
				if r.URL.Path == "/session" {
					if r.Header.Get("Authorization") != "Bearer synthetic-native-owner" {
						t.Fatal("remembered token changed")
					}
					if tc.transport {
						return nil, errors.New("synthetic transport failure")
					}
				} else {
					if r.URL.Path != "/pair" {
						t.Fatal("unexpected pairing continuation", r.URL.Path)
					}
					code = 503
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{"Retry-After": []string{"2"}}}, nil
			})}
			token, err := nativeSession("http://127.0.0.1:9270", state)
			if tc.code == 200 {
				if err != nil || token != "synthetic-native-owner" {
					t.Fatal("valid session not reused", err)
				}
			} else if err == nil {
				t.Fatal("failure not reported")
			}
			want := []string{"/session"}
			if tc.wantPair {
				want = append(want, "/pair")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("unexpected pairing", calls)
			}
			after, e := os.ReadFile(file)
			if e != nil || string(after) != string(original) {
				t.Fatal("saved credential changed", e)
			}
			info, e := os.Stat(file)
			if e != nil || info.Mode().Perm() != 0600 {
				t.Fatal("saved credential mode changed", e)
			}
		})
	}
}
