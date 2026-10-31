// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestManualSelectionPersistsPerProfileAndRollsBackFailedCommit(t *testing.T) {
	a := testApp(t)
	p, err := a.saveProfile(context.Background(), Profile{Name: "fixture"}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	now := "alpha"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxies/Route" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == "PUT" {
			var q map[string]string
			_ = json.NewDecoder(r.Body).Decode(&q)
			now = q["name"]
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"now": now})
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	s := a.snapshot()
	s.Settings.ControllerPort = port
	if err = a.persist(s); err != nil {
		t.Fatal(err)
	}
	a.core = &Core{running: true}
	if err = a.selectGroup(context.Background(), "Route", "DIRECT"); err != nil {
		t.Fatal(err)
	}
	if a.snapshot().Selections[p.ID]["Route"] != "DIRECT" || now != "DIRECT" {
		t.Fatal("selection was not saved with the profile")
	}
	path := filepath.Join(a.root, "state.json")
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = a.selectGroup(context.Background(), "Route", "alpha"); err == nil {
		t.Fatal("expected state commit failure")
	}
	if now != "DIRECT" || a.snapshot().Selections[p.ID]["Route"] != "DIRECT" {
		t.Fatal("failed save changed the selected route")
	}
}

func TestStoppedLastProfileCanBeDeletedWithoutDanglingSelections(t *testing.T) {
	a := testApp(t)
	p, err := a.saveProfile(context.Background(), Profile{Name: "temporary"}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	s := a.snapshot()
	s.Selections = map[string]map[string]string{p.ID: {"Route": "DIRECT"}}
	if err = a.persist(s); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"id": p.ID})
	if _, err = a.dispatch(context.Background(), "profiles.delete", data); err != nil {
		t.Fatal(err)
	}
	state := a.snapshot()
	if state.ActiveProfile != "" || state.Enabled || len(state.Profiles) != 0 || len(state.Selections) != 0 {
		t.Fatal("deletion left dangling state")
	}
	if _, err = os.Stat(filepath.Join(a.root, "profiles", p.ID+".yaml")); !os.IsNotExist(err) {
		t.Fatal("raw temporary profile was retained")
	}
}
