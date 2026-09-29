package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMobileCommandsUseLoopbackAPI(t *testing.T) {
	for _, action := range []string{"status", "enable", "disable", "regenerate", "secure-pairing"} {
		t.Run(action, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/telemetry/cli-invoked" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				calls = append(calls, r.Method+" "+r.URL.Path)
				if action == "secure-pairing" {
					var body map[string]bool
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body["enabled"] {
						t.Errorf("body = %v, err = %v", body, err)
					}
				}
				_, _ = w.Write([]byte(`{"enabled":false,"securePairing":{"active":true}}`))
			}))
			defer srv.Close()
			writeRunFileFor(t, cfg, srv)
			args := []string{"mobile", action}
			if action == "secure-pairing" {
				args = append(args, "on")
			}
			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
			if err != nil {
				t.Fatal(err)
			}
			want := "POST /api/v1/mobile/" + action
			if action == "status" {
				want = "GET /api/v1/mobile/status"
			}
			if len(calls) == 0 || calls[len(calls)-1] != want {
				t.Fatalf("calls = %v, want last %q", calls, want)
			}
		})
	}
}

func TestMobileEnablePreservesPairedPassword(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/telemetry/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/mobile/status" {
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"enabled":true,"password":"existing"}`))
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "mobile", "enable")
	if err != nil || !strings.Contains(out, "existing") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

func TestMobilePairingWireContract(t *testing.T) {
	st := mobileStatus{Enabled: true, Password: "secret", HostID: "thor-id", Endpoints: []mobileEndpoint{{Kind: "lan", Host: "192.168.1.2", Port: 3011}}}
	if _, err := mobilePairingLink(st, "Thor", "android", false); err == nil {
		t.Fatal("must not downgrade to cleartext")
	}
	st.SecurePairing.Active, st.SecurePairing.Host, st.SecurePairing.Port = true, "thor.example.ts.net", 443
	for _, lan := range []bool{false, true} {
		link, err := mobilePairingLink(st, "Thor", "android", lan)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "aomobile://pair#"))
		if err != nil {
			t.Fatal(err)
		}
		var offer struct {
			V         int              `json:"v"`
			HostID    string           `json:"hostId"`
			Token     string           `json:"token"`
			Endpoints []mobileEndpoint `json:"endpoints"`
		}
		if err := json.Unmarshal(payload, &offer); err != nil {
			t.Fatal(err)
		}
		if offer.V != 2 || offer.HostID != "thor-id" || offer.Token != "secret" || len(offer.Endpoints) != 1 || offer.Endpoints[0].Secure == lan {
			t.Fatalf("offer=%+v", offer)
		}
	}
	st.Enabled = false
	if _, err := mobilePairingLink(st, "Thor", "android", false); err == nil {
		t.Fatal("disabled bridge must not pair")
	}
}

func TestMobileErrorsAndUsage(t *testing.T) {
	for _, args := range [][]string{{"mobile", "secure-pairing", "yes"}, {"mobile", "status", "extra"}, {"mobile", "pair", "extra"}} {
		_, _, err := executeCLI(t, Deps{}, args...)
		if ExitCode(err) != 2 {
			t.Fatalf("%v: %v", args, err)
		}
	}
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/telemetry/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/api/v1/mobile/secure-pairing" {
			_, _ = w.Write([]byte(`{"securePairing":{"active":false,"reason":"no_cli"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"failed","code":"MOBILE_ENABLE","requestId":"req-1"}`))
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	for _, action := range [][]string{{"mobile", "status"}, {"mobile", "secure-pairing", "on"}} {
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, action...)
		if err == nil || ExitCode(err) != 1 {
			t.Fatalf("%v: %v", action, err)
		}
	}
}
