// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorRevisionRejectsStaleAPIAndUpload(t *testing.T) {
	for _, upload := range []bool{false, true} {
		t.Run(map[bool]string{false: "api", true: "upload"}[upload], func(t *testing.T) {
			a := testApp(t)
			profile, err := a.saveProfile(context.Background(), Profile{Name: "original"}, testYAML)
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]string{"id": profile.ID})
			read, err := a.dispatch(context.Background(), "profiles.read", args)
			if err != nil {
				t.Fatal(err)
			}
			revision := read.(map[string]any)["revision"].(string)
			if len(revision) != 64 {
				t.Fatal("missing edit revision")
			}
			updated := profile
			updated.Name = "new metadata"
			if _, err = a.saveProfile(context.Background(), updated, testYAML); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"id": profile.ID, "name": "stale metadata", "revision": revision, "content": testYAML + "\n# old draft\n"}
			var result any
			if upload {
				content := payload["content"].(string)
				delete(payload, "content")
				begin, _ := json.Marshal(map[string]any{"kind": "profile", "name": "editor.yaml", "size": len(content), "metadata": payload})
				result, err = a.handleUpload(context.Background(), "upload.begin", begin)
				if err != nil {
					t.Fatal(err)
				}
				id := result.(map[string]any)["id"].(string)
				chunk, _ := json.Marshal(map[string]any{"id": id, "offset": 0, "data": base64.StdEncoding.EncodeToString([]byte(content))})
				if _, err = a.handleUpload(context.Background(), "upload.chunk", chunk); err != nil {
					t.Fatal(err)
				}
				finish, _ := json.Marshal(map[string]string{"id": id})
				result, err = a.handleUpload(context.Background(), "upload.finish", finish)
			} else {
				data, _ := json.Marshal(payload)
				result, err = a.dispatch(context.Background(), "profiles.save", data)
			}
			if err != nil {
				t.Fatal(err)
			}
			job := waitTestJob(t, a, result)
			if job.Status != "error" || !strings.Contains(job.Message, "编辑期间") {
				t.Fatal(job)
			}
			data, err := os.ReadFile(filepath.Join(a.root, "profiles", profile.ID+".yaml"))
			if err != nil || string(data) != testYAML || findProfile(a.snapshot(), profile.ID).Name != "new metadata" {
				t.Fatal("stale draft replaced current data")
			}
			fresh, err := a.dispatch(context.Background(), "profiles.read", args)
			if err != nil {
				t.Fatal(err)
			}
			revision = fresh.(map[string]any)["revision"].(string)
			if _, err = a.saveProfile(context.Background(), updated, testYAML+"\n# edited\n", revision); err != nil {
				t.Fatal(err)
			}
			if _, err = a.saveProfile(context.Background(), updated, testYAML, revision); err == nil {
				t.Fatal("stale bytes accepted")
			}
		})
	}
}

func TestEditorCopyDoesNotChangeActiveProfileOrSubscription(t *testing.T) {
	a := testApp(t)
	original, err := a.saveProfile(context.Background(), Profile{Name: "online", URL: "https://example.test/config", UpdateHours: 24}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.saveProfile(context.Background(), Profile{Name: "local copy"}, testYAML+"\n# local\n")
	if err != nil {
		t.Fatal(err)
	}
	s := a.snapshot()
	if copy.URL != "" || copy.UpdateHours != 0 || s.ActiveProfile != original.ID || len(s.Profiles) != 2 || findProfile(s, original.ID).URL != original.URL {
		t.Fatal("copy changed original profile")
	}
}
