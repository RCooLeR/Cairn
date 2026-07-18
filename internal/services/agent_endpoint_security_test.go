package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestCanonicalAgentEndpointAllowsLiteralLoopbackOnly(t *testing.T) {
	t.Parallel()

	allowed := []struct {
		name string
		raw  string
		want string
	}{
		{name: "IPv4", raw: "http://127.0.0.1:11434", want: "http://127.0.0.1:11434"},
		{name: "IPv4 loopback range", raw: "https://127.12.34.56:443/", want: "https://127.12.34.56:443"},
		{name: "IPv6", raw: "http://[::1]:11434", want: "http://[::1]:11434"},
		{name: "canonicalizes IPv6", raw: "HTTPS://[0:0:0:0:0:0:0:1]:8443/", want: "https://[::1]:8443"},
	}
	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonicalAgentEndpoint(tt.raw)
			if err != nil {
				t.Fatalf("canonicalAgentEndpoint(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("canonicalAgentEndpoint(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCanonicalAgentEndpointRejectsSSRFAndAmbiguousSurfaces(t *testing.T) {
	t.Parallel()

	blocked := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "relative", raw: "127.0.0.1:11434"},
		{name: "localhost", raw: "http://localhost:11434"},
		{name: "localhost trailing dot", raw: "http://localhost.:11434"},
		{name: "DNS resolving to loopback", raw: "http://127.0.0.1.nip.io:11434"},
		{name: "DNS rebinding name", raw: "http://agent.internal.example:11434"},
		{name: "integer IPv4", raw: "http://2130706433:11434"},
		{name: "hex IPv4", raw: "http://0x7f000001:11434"},
		{name: "private 10", raw: "http://10.0.0.1:11434"},
		{name: "private 172", raw: "http://172.16.0.1:11434"},
		{name: "private 192", raw: "http://192.168.1.1:11434"},
		{name: "link local IPv4", raw: "http://169.254.1.2:11434"},
		{name: "cloud metadata IPv4", raw: "http://169.254.169.254:80"},
		{name: "container metadata IPv4", raw: "http://169.254.170.2:80"},
		{name: "Alibaba metadata IPv4", raw: "http://100.100.100.200:80"},
		{name: "public IPv4", raw: "https://8.8.8.8:443"},
		{name: "unspecified IPv4", raw: "http://0.0.0.0:11434"},
		{name: "link local IPv6", raw: "http://[fe80::1]:11434"},
		{name: "ULA IPv6", raw: "http://[fd00::1]:11434"},
		{name: "unspecified IPv6", raw: "http://[::]:11434"},
		{name: "IPv4 mapped IPv6", raw: "http://[::ffff:127.0.0.1]:11434"},
		{name: "IPv6 zone", raw: "http://[::1%25loopback]:11434"},
		{name: "userinfo", raw: "http://user:secret@127.0.0.1:11434"},
		{name: "query", raw: "http://127.0.0.1:11434?target=metadata"},
		{name: "empty query", raw: "http://127.0.0.1:11434?"},
		{name: "fragment", raw: "http://127.0.0.1:11434#collector"},
		{name: "empty fragment", raw: "http://127.0.0.1:11434#"},
		{name: "API path", raw: "http://127.0.0.1:11434/v1"},
		{name: "encoded path", raw: "http://127.0.0.1:11434/%2e"},
		{name: "missing port", raw: "http://127.0.0.1"},
		{name: "port zero", raw: "http://127.0.0.1:0"},
		{name: "port overflow", raw: "http://127.0.0.1:65536"},
		{name: "leading-zero port", raw: "http://127.0.0.1:011434"},
		{name: "signed port", raw: "http://127.0.0.1:+11434"},
		{name: "negative port", raw: "http://127.0.0.1:-1"},
		{name: "file scheme", raw: "file://127.0.0.1:11434/etc/passwd"},
		{name: "gopher scheme", raw: "gopher://127.0.0.1:11434"},
		{name: "unix scheme", raw: "unix://127.0.0.1:11434"},
		{name: "opaque URL", raw: "http:127.0.0.1:11434"},
		{name: "overlong", raw: "http://127.0.0.1:11434" + strings.Repeat("/", maxAgentEndpointBytes)},
	}
	for _, tt := range blocked {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := canonicalAgentEndpoint(tt.raw); err == nil {
				t.Fatalf("canonicalAgentEndpoint(%q) = %q, want rejection", tt.raw, got)
			}
		})
	}
}

func TestCanonicalAgentRequestTargetAllowsOnlyExactAgentRoutes(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"/api/tags", "/api/chat", "/v1/models", "/v1/chat/completions"} {
		target := "http://127.0.0.1:11434" + route
		if got, err := canonicalAgentRequestTarget(target); err != nil || got != target {
			t.Fatalf("canonicalAgentRequestTarget(%q) = %q, %v", target, got, err)
		}
	}
	for _, target := range []string{
		"http://127.0.0.1:11434/",
		"http://127.0.0.1:11434/api/tags/",
		"http://127.0.0.1:11434//api/tags",
		"http://127.0.0.1:11434/api/%74ags",
		"http://127.0.0.1:11434/api/tags?next=/api/chat",
		"http://127.0.0.1:11434/api/tags#fragment",
	} {
		if _, err := canonicalAgentRequestTarget(target); !apperror.IsCode(err, apperror.ProviderNotReady) {
			t.Fatalf("canonicalAgentRequestTarget(%q) error = %v, want fail-closed rejection", target, err)
		}
	}
}

func TestAgentServiceUsesApprovedProductionLoopbackTransport(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"gemma4:12b-it-q8_0"}]}`)
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL+"/"); err != nil {
		t.Fatal(err)
	}

	status, err := (&AgentService{Settings: db.Settings()}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || !status.Reachable || status.Endpoint != server.URL {
		t.Fatalf("Status() = %#v, want reachable canonical endpoint %q", status, server.URL)
	}
}

func TestAgentServiceUsesApprovedIPv6LoopbackEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback listener unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"name":"gemma4:12b-it-q8_0"}]}`)
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	ctx := context.Background()
	db := openServiceTestStore(t)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatal(err)
	}
	status, err := (&AgentService{Settings: db.Settings(), Client: server.Client()}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || !status.Reachable {
		t.Fatalf("Status() = %#v, want reachable IPv6 loopback endpoint", status)
	}
}

func TestAgentServiceNeverFollowsAgentRedirects(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		statusCode := statusCode
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			ctx := context.Background()
			db := openServiceTestStore(t)
			var collectorCalls atomic.Int32
			var collectorBody atomic.Value
			collectorBody.Store("")
			collector := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				collectorCalls.Add(1)
				raw, _ := io.ReadAll(r.Body)
				collectorBody.Store(string(raw))
			}))
			t.Cleanup(collector.Close)

			var source *httptest.Server
			source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/tags":
					_, _ = io.WriteString(w, `{"models":[{"name":"gemma4:12b-it-q8_0"}]}`)
				case "/api/chat":
					w.Header().Set("Location", collector.URL+"/collect")
					w.WriteHeader(statusCode)
				case "/manual-redirect":
					w.Header().Set("Location", collector.URL+"/manual")
					w.WriteHeader(http.StatusFound)
				default:
					t.Fatalf("unexpected source path %q", r.URL.Path)
				}
			}))
			t.Cleanup(source.Close)
			if err := db.Settings().SetString(ctx, "agent.endpoint", source.URL); err != nil {
				t.Fatal(err)
			}

			client := source.Client()
			var originalRedirectCalls atomic.Int32
			client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				originalRedirectCalls.Add(1)
				return nil
			}
			secret := "agent-context-must-not-cross-origin"
			_, err := (&AgentService{Settings: db.Settings(), Client: client}).Chat(ctx, models.AgentChatRequest{
				Prompt:  secret,
				ToolIDs: []string{},
			})
			if err == nil {
				t.Fatal("Chat() succeeded through a redirect, want fail-closed error")
			}
			if collectorCalls.Load() != 0 || strings.Contains(collectorBody.Load().(string), secret) {
				t.Fatalf("redirect collector received %d requests and body %q", collectorCalls.Load(), collectorBody.Load())
			}
			if originalRedirectCalls.Load() != 0 {
				t.Fatalf("injected client's redirect policy ran %d times during Agent request", originalRedirectCalls.Load())
			}

			resp, manualErr := client.Get(source.URL + "/manual-redirect")
			if manualErr != nil {
				t.Fatalf("original injected client was mutated: %v", manualErr)
			}
			_ = resp.Body.Close()
			if originalRedirectCalls.Load() != 1 || collectorCalls.Load() != 1 {
				t.Fatalf("original client redirect calls = %d, collector calls = %d; want 1 each", originalRedirectCalls.Load(), collectorCalls.Load())
			}
		})
	}
}

func TestAgentServiceRejectsHTTPSDowngradeRedirect(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	var collectorCalls atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		collectorCalls.Add(1)
	}))
	t.Cleanup(collector.Close)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"gemma4:12b-it-q8_0"}]}`)
		case "/api/chat":
			w.Header().Set("Location", collector.URL+"/downgrade")
			w.WriteHeader(http.StatusTemporaryRedirect)
		}
	}))
	t.Cleanup(source.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", source.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := (&AgentService{Settings: db.Settings(), Client: source.Client()}).Chat(ctx, models.AgentChatRequest{Prompt: "sensitive", ToolIDs: []string{}}); err == nil {
		t.Fatal("Chat() followed an HTTPS-to-HTTP redirect")
	}
	if collectorCalls.Load() != 0 {
		t.Fatalf("downgrade collector calls = %d, want 0", collectorCalls.Load())
	}
}

func TestAgentHTTPResponseLimitDetectsLimitPlusOne(t *testing.T) {
	if payload, err := readAgentHTTPPayload(bytes.NewReader(make([]byte, maxAgentHTTPResponseBytes))); err != nil || len(payload) != maxAgentHTTPResponseBytes {
		t.Fatalf("read exact limit = (%d bytes, %v)", len(payload), err)
	}
	if payload, err := readAgentHTTPPayload(bytes.NewReader(make([]byte, maxAgentHTTPResponseBytes+1))); err == nil || payload != nil {
		t.Fatalf("read limit+1 = (%d bytes, %v), want overflow rejection", len(payload), err)
	}
}

func TestAgentServiceRejectsOversizedAndTrailingJSONResponses(t *testing.T) {
	t.Run("oversized model list", func(t *testing.T) {
		ctx := context.Background()
		db := openServiceTestStore(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxAgentHTTPResponseBytes+1))
		}))
		t.Cleanup(server.Close)
		if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
			t.Fatal(err)
		}
		status, err := (&AgentService{Settings: db.Settings(), Client: server.Client()}).Status(ctx)
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if status.Reachable || !strings.Contains(status.Error, "exceeds the safe size limit") {
			t.Fatalf("Status() = %#v, want explicit overflow error", status)
		}
	})

	t.Run("trailing model JSON", func(t *testing.T) {
		ctx := context.Background()
		db := openServiceTestStore(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"models":[]} {"second":true}`)
		}))
		t.Cleanup(server.Close)
		if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
			t.Fatal(err)
		}
		status, err := (&AgentService{Settings: db.Settings(), Client: server.Client()}).Status(ctx)
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if status.Reachable || !strings.Contains(status.Error, "Decode local agent model list failed") {
			t.Fatalf("Status() = %#v, want trailing JSON rejection", status)
		}
	})

	t.Run("trailing chat JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"message":{"content":"ok"}} {}`)
		}))
		t.Cleanup(server.Close)
		var decoded map[string]any
		err := (&AgentService{Client: server.Client()}).postJSON(
			context.Background(),
			server.URL+"/api/chat",
			map[string]string{"prompt": "safe"},
			&decoded,
		)
		if err == nil || !strings.Contains(err.Error(), "Decode local agent response failed") {
			t.Fatalf("postJSON trailing response error = %v", err)
		}
	})

	t.Run("decoder rejects a second value", func(t *testing.T) {
		var decoded map[string]any
		err := decodeSingleAgentJSON([]byte(`{"first":true} {"second":true}`), &decoded)
		if err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
			t.Fatalf("decodeSingleAgentJSON() error = %v", err)
		}
	})
}

func TestAgentServiceBoundsPromptAndOutboundRequestBeforeNetwork(t *testing.T) {
	t.Run("chat prompt", func(t *testing.T) {
		ctx := context.Background()
		db := openServiceTestStore(t)
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			calls.Add(1)
		}))
		t.Cleanup(server.Close)
		if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
			t.Fatal(err)
		}
		_, err := (&AgentService{Settings: db.Settings(), Client: server.Client()}).Chat(ctx, models.AgentChatRequest{
			Prompt: strings.Repeat("x", maxAgentPromptBytes+1),
		})
		if !apperror.IsCode(err, apperror.Conflict) || calls.Load() != 0 {
			t.Fatalf("Chat(oversized) error = %v, network calls = %d", err, calls.Load())
		}
	})

	t.Run("draft instruction", func(t *testing.T) {
		_, err := (&AgentService{}).DraftProjectFile(context.Background(), models.AgentDraftFileRequest{
			Instruction: strings.Repeat("x", maxAgentPromptBytes+1),
		})
		if !apperror.IsCode(err, apperror.Conflict) {
			t.Fatalf("DraftProjectFile(oversized) error = %v, want conflict", err)
		}
	})

	t.Run("marshaled request", func(t *testing.T) {
		var calls atomic.Int32
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("transport must not run")
		})}
		var decoded map[string]any
		err := (&AgentService{Client: client}).postJSON(
			context.Background(),
			"http://127.0.0.1:11434/api/chat",
			map[string]string{"prompt": strings.Repeat("x", maxAgentHTTPRequestBytes)},
			&decoded,
		)
		if !apperror.IsCode(err, apperror.Conflict) || calls.Load() != 0 {
			t.Fatalf("postJSON(oversized) error = %v, transport calls = %d", err, calls.Load())
		}
	})
}

func TestAgentServiceBoundsToolIDsBeforeNetwork(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatal(err)
	}
	service := &AgentService{Settings: db.Settings(), Client: server.Client()}

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "too many", ids: make([]string, maxAgentToolIDs+1)},
		{name: "oversized", ids: []string{strings.Repeat("x", maxAgentToolIDBytes+1)}},
		{name: "invalid UTF-8", ids: []string{string([]byte{0xff})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Chat(ctx, models.AgentChatRequest{Prompt: "hello", ToolIDs: tt.ids})
			if !apperror.IsCode(err, apperror.Conflict) {
				t.Fatalf("Chat() error = %v, want conflict", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("oversized tool selection made %d network calls", calls.Load())
	}
}

func TestCloneAgentHTTPClientEnforcesPolicyWithoutMutatingInjectedClient(t *testing.T) {
	var dialCalls atomic.Int32
	var proxyCalls atomic.Int32
	var protocolCalls atomic.Int32
	originalTransport := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			proxyCalls.Add(1)
			return url.Parse("http://203.0.113.1:8080")
		},
		Dial: func(string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("injected Dial must not run")
		},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("injected DialContext must not run")
		},
		DialTLS: func(string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("injected DialTLS must not run")
		},
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("injected DialTLSContext must not run")
		},
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{
			"cairn-exfil": func(string, *tls.Conn) http.RoundTripper {
				protocolCalls.Add(1)
				return roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("injected TLSNextProto must not run")
				})
			},
		},
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	original := &http.Client{
		Transport: originalTransport,
		Timeout:   0,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return nil
		},
	}
	cloned := cloneAgentHTTPClient(original)
	if cloned == original || cloned.Timeout != agentHTTPTimeout {
		t.Fatalf("clone = %#v, original = %#v", cloned, original)
	}
	clonedTransport, ok := cloned.Transport.(*http.Transport)
	if !ok || clonedTransport == originalTransport {
		t.Fatalf("cloned transport = %#v, want independent *http.Transport", cloned.Transport)
	}
	if clonedTransport.ResponseHeaderTimeout != agentResponseHeaderTimeout || clonedTransport.MaxResponseHeaderBytes != 64*1024 {
		t.Fatalf("cloned transport limits = %#v", clonedTransport)
	}
	if clonedTransport.Proxy != nil || clonedTransport.Dial != nil || clonedTransport.DialTLS != nil || clonedTransport.DialTLSContext != nil || clonedTransport.DialContext == nil {
		t.Fatalf("cloned transport retained an unsafe network hook: %#v", clonedTransport)
	}
	if _, retained := clonedTransport.TLSNextProto["cairn-exfil"]; retained || cloned.Jar != nil {
		t.Fatal("cloned client retained an injected TLSNextProto or cookie jar hook")
	}
	if original.Timeout != 0 || originalTransport.ResponseHeaderTimeout != 0 || originalTransport.MaxResponseHeaderBytes != 0 {
		t.Fatalf("injected client was mutated: client=%#v transport=%#v", original, originalTransport)
	}
	if originalTransport.Proxy == nil || originalTransport.Dial == nil || originalTransport.DialContext == nil || originalTransport.DialTLS == nil || originalTransport.DialTLSContext == nil {
		t.Fatal("injected transport hooks were mutated")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	conn, err := clonedTransport.DialContext(ctx, "tcp", "203.0.113.10:443")
	cancel()
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || dialCalls.Load() != 0 || proxyCalls.Load() != 0 || protocolCalls.Load() != 0 {
		t.Fatalf("hardened dial error = %v, injected dial calls = %d, proxy calls = %d, protocol calls = %d", err, dialCalls.Load(), proxyCalls.Load(), protocolCalls.Load())
	}
	if err := cloned.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy error = %v, want ErrUseLastResponse", err)
	}
}

func TestAgentServiceOverridesInjectedCustomRoundTripper(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"name":"gemma4:12b-it-q8_0"}]}`)
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("custom RoundTripper must not run")
	})}
	status, err := (&AgentService{Settings: db.Settings(), Client: client}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || !status.Reachable || calls.Load() != 0 {
		t.Fatalf("Status() = %#v, custom RoundTripper calls = %d", status, calls.Load())
	}
}

func TestAgentServiceStatusDoesNotEchoInvalidLegacyEndpoint(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	legacy := "http://legacy-user:legacy-password@127.0.0.1:11434/private"
	if err := db.Settings().SetString(ctx, "agent.endpoint", legacy); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	status, err := (&AgentService{
		Settings: db.Settings(),
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("transport must not run")
		})},
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || status.Endpoint != "" || status.Reachable || calls.Load() != 0 {
		t.Fatalf("Status() = %#v, transport calls = %d", status, calls.Load())
	}
	if strings.Contains(status.Error, "legacy-user") || strings.Contains(status.Error, "legacy-password") || strings.Contains(status.Error, legacy) {
		t.Fatalf("Status() exposed legacy endpoint data: %#v", status)
	}
}

func TestAgentServiceOverridesInjectedDialAndTLSDialHooks(t *testing.T) {
	ctx := context.Background()
	db := openServiceTestStore(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"name":"gemma4:12b-it-q8_0"}]}`)
	}))
	t.Cleanup(server.Close)
	if err := db.Settings().SetString(ctx, "agent.endpoint", server.URL); err != nil {
		t.Fatal(err)
	}

	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.InsecureSkipVerify = true // Test-only trust for the generated loopback certificate.
	var dialCalls atomic.Int32
	var protocolCalls atomic.Int32
	transport.RegisterProtocol("https", roundTripFunc(func(*http.Request) (*http.Response, error) {
		protocolCalls.Add(1)
		return nil, errors.New("injected alternate protocol must not run")
	}))
	transport.Dial = func(string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, errors.New("injected Dial must not run")
	}
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, errors.New("injected DialContext must not run")
	}
	transport.DialTLS = func(string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, errors.New("injected DialTLS must not run")
	}
	transport.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, errors.New("injected DialTLSContext must not run")
	}

	injectedClient := &http.Client{Transport: transport}
	hardenedClient := cloneAgentHTTPClient(injectedClient)
	hardenedTransport := hardenedClient.Transport.(*http.Transport)
	if hardenedTransport.TLSClientConfig == nil || !hardenedTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("hardened transport did not preserve local test TLS trust: %#v", hardenedTransport.TLSClientConfig)
	}
	probe, err := hardenedClient.Get(server.URL + "/api/tags")
	if err != nil {
		t.Fatalf("hardened local TLS request failed: %v", err)
	}
	_ = probe.Body.Close()

	status, err := (&AgentService{
		Settings: db.Settings(),
		Client:   injectedClient,
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || !status.Reachable || dialCalls.Load() != 0 || protocolCalls.Load() != 0 {
		t.Fatalf("Status() = %#v, injected dial calls = %d, protocol calls = %d", status, dialCalls.Load(), protocolCalls.Load())
	}
}

func TestProductionAgentDialerRejectsDNSPrivateAndMetadataAddresses(t *testing.T) {
	transport := newAgentProductionTransport()
	for _, address := range []string{
		"localhost:11434",
		"10.0.0.1:11434",
		"169.254.169.254:80",
		"[fe80::1]:11434",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		conn, err := transport.DialContext(ctx, "tcp", address)
		cancel()
		if conn != nil {
			_ = conn.Close()
		}
		if err == nil {
			t.Fatalf("DialContext(%q) succeeded, want fail-closed rejection", address)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
