// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectivityHTTPIsNotUnlockProof(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		code                                int
		body, parser, state, unlock, region string
	}{
		{"normal", 200, "ok", "", "reachable", "", ""},
		{"blocked", 403, "blocked", "generic", "limited", "unknown", ""},
		{"proxy auth", 407, "auth", "", "limited", "", ""},
		{"rate limited", 429, "wait", "", "limited", "", ""},
		{"server error", 503, "error", "", "limited", "", ""},
		{"media homepage", 200, "<html>Welcome</html>", "generic", "reachable", "unknown", ""},
		{"region", 200, "Not available in your country", "disney", "reachable", "restricted", ""},
		{"captcha", 200, "verify you are human", "netflix", "limited", "unknown", ""},
		{"netflix signal", 200, `<meta property="og:video" content="sample"><script>{"requestCountry":{"id":"US"}}</script>`, "netflix", "reachable", "likely", "US"},
		{"premium signal", 200, `{"countryCode":"JP","purchaseButtonOverride":{}}`, "youtube", "reachable", "likely", "JP"},
		{"country alone", 200, `{"countryCode":"JP"}`, "youtube", "reachable", "unknown", "JP"},
		{"spotify country", 200, `{"geoCountry":"TW"}`, "spotify", "reachable", "unknown", "TW"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("probe sent credentials")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			group := "sites"
			if tc.parser != "" {
				group = "media"
			}
			result := probeHealth(context.Background(), server.Client(), HealthTarget{ID: "fixture", Group: group, URL: server.URL, Parser: tc.parser})
			if result.State != tc.state || result.Unlock != tc.unlock || result.Region != tc.region {
				t.Fatalf("unexpected result %+v", result)
			}
		})
	}
}
func TestConnectivityFallbackTraceAndBounds(t *testing.T) {
	gets := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(405)
			return
		}
		gets++
		_, _ = w.Write([]byte("ip=192.0.2.9\nloc=JP\nsecret=ignored\n"))
	}))
	defer server.Close()
	r := probeHealth(context.Background(), server.Client(), HealthTarget{URL: server.URL})
	if r.State != "reachable" || gets != 1 {
		t.Fatal(r, gets)
	}
	r = probeHealth(context.Background(), server.Client(), HealthTarget{URL: server.URL, Parser: "trace"})
	if r.IP != "192.0.2.9" || r.Region != "JP" {
		t.Fatal(r)
	}
	result := HealthResult{State: "reachable"}
	classifyMedia(&result, "netflix", "unknown changed page", "/", true)
	if result.Unlock != "unknown" || !strings.Contains(result.Detail, "不完整") {
		t.Fatal(result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = probeHealth(ctx, server.Client(), HealthTarget{URL: server.URL})
	if r.State != "failed" {
		t.Fatal(r)
	}
}
func TestHealthRedirectBoundaries(t *testing.T) {
	client := healthClient(nil)
	defer client.CloseIdleConnections()
	first, _ := http.NewRequest("GET", "https://www.netflix.com/title/test", nil)
	for _, tc := range []struct {
		address string
		allowed bool
	}{{"https://www.netflix.com/us/title/test", true}, {"https://netflix.com/title/test", true}, {"http://www.netflix.com/", false}, {"https://127.0.0.1/", false}, {"https://www.netflix.com.evil.test/", false}, {"https://user:pass@www.netflix.com/", false}} {
		req, _ := http.NewRequest("GET", tc.address, nil)
		err := client.CheckRedirect(req, []*http.Request{first})
		if (err == nil) != tc.allowed {
			t.Fatal(tc.address, err)
		}
	}
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct client inherited environment proxy")
	}
	proxy, _ := url.Parse("http://127.0.0.1:7890")
	routed := healthClient(proxy)
	defer routed.CloseIdleConnections()
	got, _ := routed.Transport.(*http.Transport).Proxy(first)
	if got.String() != proxy.String() {
		t.Fatal(got)
	}
}
func TestHealthStateCachingAndSingleFlight(t *testing.T) {
	a := testApp(t)
	signature := a.healthSignature()
	report := &HealthReport{Signature: signature, FinishedAt: time.Now().Format(time.RFC3339)}
	a.healthReports = map[string]*HealthReport{"basic": report}
	cached, err := a.beginConnectivity("basic")
	if err != nil || cached != report {
		t.Fatal("cache was not reused", err)
	}
	a.healthJobs = map[string]string{"basic": "fixture-job"}
	job, err := a.beginConnectivity("basic")
	if err != nil || job.(map[string]string)["jobId"] != "fixture-job" {
		t.Fatal(job, err)
	}
	if _, err = a.beginConnectivity("media"); err == nil {
		t.Fatal("cross-kind concurrent check allowed")
	}
	s := a.snapshot()
	s.Settings.Mode = "global"
	if err = a.persist(s); err != nil {
		t.Fatal(err)
	}
	if !a.connectivityState()["stale"].(map[string]bool)["basic"] {
		t.Fatal("old route remained green")
	}
	encoded, _ := json.Marshal(a.connectivityState())
	if strings.Contains(string(encoded), "https://") {
		t.Fatal("catalog leaked internal URL fields")
	}
	if healthError(&net.DNSError{Err: "fixture", Name: "private.test"}) != "DNS 解析失败" || strings.Contains(healthError(errors.New("secret")), "secret") {
		t.Fatal("error classification exposes raw input")
	}
}

func TestConnectivityWorkerLimitAndNoNetworkRuleChanges(t *testing.T) {
	var current, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := current.Add(1)
		defer current.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		w.WriteHeader(204)
	}))
	defer server.Close()
	old := siteTargets
	defer func() { siteTargets = old }()
	siteTargets = nil
	for i := 0; i < 12; i++ {
		siteTargets = append(siteTargets, HealthTarget{ID: "fixture", URL: server.URL, Group: "sites"})
	}
	a := testApp(t)
	report := a.runConnectivity(context.Background(), "basic", a.healthSignature())
	if peak.Load() > 4 || peak.Load() < 2 || len(report.Results) != 12 {
		t.Fatal("unexpected concurrency", peak.Load(), len(report.Results))
	}
	for _, result := range report.Results {
		if result.State != "reachable" {
			t.Fatal(result)
		}
	}
	if a.isRunning() || a.snapshot().Enabled || a.dns != nil {
		t.Fatal("network check changed service state")
	}
}
