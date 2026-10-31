// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueuedValidationCanBeCancelled(t *testing.T) {
	a := testApp(t)
	a.validationSlot = make(chan struct{}, 1)
	a.validationSlot <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.validateConfig(ctx, []byte(testYAML), a.snapshot().Kernel); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued validation ignored cancellation: %v", err)
	}
	if len(a.validationSlot) != 1 {
		t.Fatal("cancelled waiter released another validator's slot")
	}
}

func TestSlowSubscriptionCanStopWithoutWaitingForNetwork(t *testing.T) {
	a := testApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	p, err := a.saveProfile(context.Background(), Profile{Name: "slow", URL: server.URL, UpdateHours: 24}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"id": p.ID})
	update, err := a.dispatch(context.Background(), "profiles.update", args)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if !a.op.TryLock() {
		t.Fatal("subscription download holds service lock")
	}
	a.op.Unlock()
	stop, err := a.dispatch(context.Background(), "service.stop", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if job := waitTestJob(t, a, stop); job.Status != "done" {
		t.Fatal(job.Message)
	}
	if job := waitTestJob(t, a, update); job.Status != "error" {
		t.Fatal("cancelled update was applied")
	}
	if a.snapshot().Enabled || findProfile(a.snapshot(), p.ID).UpdateFailures != 0 {
		t.Fatal("stop was undone or cancellation treated as retry failure")
	}
}

func TestPendingSubscriptionDoesNotResurrectDeletedProfile(t *testing.T) {
	a := testApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(testYAML))
	}))
	defer server.Close()
	p, err := a.saveProfile(context.Background(), Profile{Name: "pending", URL: server.URL}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"id": p.ID})
	job, err := a.dispatch(context.Background(), "profiles.update", args)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	_, deleteErr := a.dispatch(context.Background(), "profiles.delete", args)
	close(release)
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if waitTestJob(t, a, job).Status != "error" {
		t.Fatal("stale download accepted")
	}
	if findProfile(a.snapshot(), p.ID) != nil {
		t.Fatal("deleted profile resurrected")
	}
	if _, err := os.Stat(filepath.Join(a.root, "profiles", p.ID+".yaml")); !os.IsNotExist(err) {
		t.Fatal("deleted YAML recreated")
	}
}

func TestSubscriptionMetadataPreservedByAPIEditing(t *testing.T) {
	a := testApp(t)
	quota := "upload=1; download=2; total=100; expire=1900000000"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", quota)
		_, _ = w.Write([]byte(testYAML))
	}))
	defer server.Close()
	p, err := a.saveProfile(context.Background(), Profile{Name: "online", URL: server.URL, UpdateHours: 24}, "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"id": p.ID, "name": "renamed", "url": server.URL, "updateHours": 24, "content": testYAML, "subscriptionInfo": "forged"})
	result, err := a.dispatch(context.Background(), "profiles.save", data)
	if err != nil {
		t.Fatal(err)
	}
	if job := waitTestJob(t, a, result); job.Status != "done" {
		t.Fatal(job.Message)
	}
	if findProfile(a.snapshot(), p.ID).SubscriptionInfo != quota {
		t.Fatal("saved quota replaced/lost")
	}
	p.URL = "https://new.example.invalid/subscription"
	p.SubscriptionInfo = "forged"
	if _, err := a.saveProfile(context.Background(), p, testYAML); err != nil {
		t.Fatal(err)
	}
	if findProfile(a.snapshot(), p.ID).SubscriptionInfo != "" {
		t.Fatal("quota from another subscription retained")
	}
}

func TestValidationRejectsConcurrentSettingsChange(t *testing.T) {
	a := testApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(testYAML))
	}))
	defer server.Close()
	p, err := a.saveProfile(context.Background(), Profile{Name: "pending", URL: server.URL}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"id": p.ID})
	job, err := a.dispatch(context.Background(), "profiles.update", args)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	a.op.Lock()
	s := a.snapshot()
	s.Settings.Mode = "direct"
	err = a.persist(s)
	a.op.Unlock()
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	result := waitTestJob(t, a, job)
	if result.Status != "error" || !strings.Contains(result.Message, "已更改") {
		t.Fatal(result)
	}
	if a.snapshot().Settings.Mode != "direct" {
		t.Fatal("new settings overwritten")
	}
}
