package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/moby/moby/api/types/registry"
	dockerclient "github.com/moby/moby/client"
)

func TestPushImageSDKRegistryAuth(t *testing.T) {
	t.Parallel()
	authenticated := base64.URLEncoding.EncodeToString([]byte(`{"username":"test-user","password":"test-password"}`))
	for _, tt := range []struct {
		name string
		auth string
	}{
		{name: "anonymous"},
		{name: "authenticated", auth: authenticated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			headers := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1.47/images/localhost:5000/test/app/push" || r.URL.Query().Get("tag") != "1.0" {
					t.Errorf("unexpected push request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				header := r.Header.Get(registry.AuthHeader)
				headers <- header
				w.Header().Set("Content-Type", "application/json")
				if header == "" {
					// Match older Docker daemons' body fallback when no auth header
					// was sent. An empty SDK request body must not reach this path.
					var config registry.AuthConfig
					if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"message": "bad parameters and missing X-Registry-Auth: " + err.Error()})
						return
					}
				}
				if tt.auth == "" {
					_, _ = io.WriteString(w, `{"errorDetail":{"message":"unauthorized: authentication required"}}`+"\n")
					return
				}
				_, _ = io.WriteString(w, `{"status":"pushed"}`+"\n")
			}))
			defer server.Close()
			api, err := dockerclient.New(dockerclient.WithHost(server.URL), dockerclient.WithAPIVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := api.Close(); err != nil {
					t.Errorf("close Docker client: %v", err)
				}
			})
			client := New(fakeDockerProvider{}, nil)
			err = client.pushImage(context.Background(), api, "localhost:5000/test/app:1.0", "localhost:5000", "push-test", tt.auth)
			if tt.auth == "" {
				if !apperror.IsCode(err, apperror.RegistryAuth) {
					t.Fatalf("anonymous push error = %v, want %s", err, apperror.RegistryAuth)
				}
			} else if err != nil {
				t.Fatalf("authenticated push error = %v", err)
			}
			select {
			case got := <-headers:
				if tt.auth != "" {
					if got != tt.auth {
						t.Fatal("push did not forward the provided auth config unchanged")
					}
					return
				}
				raw, err := base64.URLEncoding.DecodeString(got)
				if err != nil {
					t.Fatalf("decode anonymous auth header: %v", err)
				}
				var config map[string]json.RawMessage
				if err := json.Unmarshal(raw, &config); err != nil || config == nil || len(config) != 0 {
					t.Fatalf("anonymous auth must be an empty JSON object: %q, error=%v", raw, err)
				}
			default:
				t.Fatal("SDK did not send a push request")
			}
		})
	}
}
