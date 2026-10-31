// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveInactiveAndUnchangedProfilesKeepsRunningCore(t *testing.T) {
	a := testApp(t)
	active, err := a.saveProfile(context.Background(), Profile{Name: "active"}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	core := &Core{running: true}
	a.core = core
	backup, err := a.saveProfile(context.Background(), Profile{Name: "backup"}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	if a.core != core || !a.isRunning() || a.snapshot().ActiveProfile != active.ID {
		t.Fatal("saving inactive profile disrupted active core")
	}
	active.Name = "renamed"
	if _, err := a.saveProfile(context.Background(), active, testYAML); err != nil {
		t.Fatal(err)
	}
	if a.core != core || !a.isRunning() {
		t.Fatal("identical active content restarted core")
	}
	// Even a failed inactive commit must leave the running core alone.
	statePath := filepath.Join(a.root, "state.json")
	if err := os.Rename(statePath, statePath+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveProfile(context.Background(), backup, testYAML+"# changed\n"); err == nil {
		t.Fatal("expected commit failure")
	}
	if a.core != core || !a.isRunning() {
		t.Fatal("inactive rollback stopped active core")
	}
	raw, _ := os.ReadFile(filepath.Join(a.root, "profiles", backup.ID+".yaml"))
	if string(raw) != testYAML {
		t.Fatal("failed inactive save did not restore original content")
	}
}

func TestUpdateBackoffAndSuccessReset(t *testing.T) {
	for i, minutes := range []int{5, 10, 20, 40, 80, 120, 120} {
		if updateBackoff(i+1) != time.Duration(minutes)*time.Minute {
			t.Fatal("bad backoff", i)
		}
	}
	a := testApp(t)
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(testYAML))
	}))
	defer server.Close()
	p, err := a.saveProfile(context.Background(), Profile{Name: "scheduled", URL: server.URL, UpdateHours: 24}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.updateProfile(context.Background(), p.ID); err == nil {
		t.Fatal("expected update failure")
	}
	failed := *findProfile(a.snapshot(), p.ID)
	if failed.UpdateError == "" || failed.UpdateFailures != 1 || time.Until(nextProfileUpdate(failed)) < 4*time.Minute {
		t.Fatalf("missing persisted retry status: %+v", failed)
	}
	loaded, err := New(a.root, a.run, "127.0.0.1:0", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if findProfile(loaded.snapshot(), p.ID).RetryAt != failed.RetryAt {
		t.Fatal("backoff lost on manager restart")
	}
	fail = false
	if err := a.updateProfile(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	success := *findProfile(a.snapshot(), p.ID)
	if success.UpdateError != "" || success.UpdateFailures != 0 || success.RetryAt != "" || time.Until(nextProfileUpdate(success)) < 23*time.Hour {
		t.Fatalf("success did not reset backoff: %+v", success)
	}
	success.UpdateHours = 0
	if !nextProfileUpdate(success).IsZero() {
		t.Fatal("manual profile scheduled")
	}
}
