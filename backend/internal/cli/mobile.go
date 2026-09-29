package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// Keep this wire mirror in the CLI: mobile controls remain loopback-only API
// calls, including on headless hosts. Never read mobile.json here.
type mobileEndpoint struct {
	Kind   string `json:"kind"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secure bool   `json:"secure"`
}

type mobileStatus struct {
	Enabled       bool             `json:"enabled"`
	Password      string           `json:"password"`
	HostID        string           `json:"hostId"`
	Endpoints     []mobileEndpoint `json:"endpoints"`
	SecurePairing struct {
		Active bool   `json:"active"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Reason string `json:"reason"`
	} `json:"securePairing"`
}

func newMobileCommand(ctx *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "mobile", Short: "Control Connect Mobile through the local daemon"}
	for _, action := range []string{"status", "enable", "disable", "regenerate"} {
		cmd := &cobra.Command{
			Use: action, Short: action + " Connect Mobile (JSON includes pairing credentials)", Args: noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				var res json.RawMessage
				if action == "status" {
					if err := ctx.getJSON(cmd.Context(), "mobile/status", &res); err != nil {
						return err
					}
				} else {
					// Enable is idempotent at the CLI boundary. The upstream enable
					// endpoint rotates credentials; reserve that for regenerate.
					if action == "enable" {
						if err := ctx.getJSON(cmd.Context(), "mobile/status", &res); err != nil {
							return err
						}
						var st mobileStatus
						if err := json.Unmarshal(res, &st); err != nil {
							return err
						}
						if st.Enabled {
							return writeJSON(cmd.OutOrStdout(), res)
						}
					}
					if err := ctx.postJSON(cmd.Context(), "mobile/"+action, struct{}{}, &res); err != nil {
						return err
					}
				}
				return writeJSON(cmd.OutOrStdout(), res)
			},
		}
		root.AddCommand(cmd)
	}
	root.AddCommand(&cobra.Command{
		Use: "secure-pairing <on|off>", Short: "Configure the private Tailscale HTTPS proxy", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "on" && args[0] != "off" {
				return usageError{fmt.Errorf("expected on or off")}
			}
			var res json.RawMessage
			if err := ctx.postJSON(cmd.Context(), "mobile/secure-pairing", map[string]bool{"enabled": args[0] == "on"}, &res); err != nil {
				return err
			}
			if err := writeJSON(cmd.OutOrStdout(), res); err != nil {
				return err
			}
			var st mobileStatus
			if err := json.Unmarshal(res, &st); err != nil {
				return err
			}
			if args[0] == "on" && !st.SecurePairing.Active {
				return fmt.Errorf("secure pairing is not active: %s (enable Connect Mobile and check tailscale serve)", st.SecurePairing.Reason)
			}
			return nil
		},
	})
	var name string
	var lan bool
	pair := &cobra.Command{
		Use: "pair", Short: "Print a mobile-app pairing link containing a secret", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st mobileStatus
			if err := ctx.getJSON(cmd.Context(), "mobile/status", &st); err != nil {
				return err
			}
			if name == "" {
				name, _ = os.Hostname()
			}
			link, err := mobilePairingLink(st, name, runtime.GOOS, lan)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), link)
			return err
		},
	}
	pair.Flags().StringVar(&name, "name", "", "Host display name")
	pair.Flags().BoolVar(&lan, "lan", false, "Pair using trusted local-network endpoints instead of private HTTPS")
	root.AddCommand(pair)
	return root
}

func mobilePairingLink(st mobileStatus, name, platform string, lan bool) (string, error) {
	if !st.Enabled || st.Password == "" || st.HostID == "" {
		return "", fmt.Errorf("connect mobile is disabled or its credentials/identity are missing")
	}
	var endpoints []mobileEndpoint
	if lan {
		for _, e := range st.Endpoints {
			if e.Kind == "lan" && e.Host != "" && e.Port > 0 {
				endpoints = append(endpoints, e)
			}
		}
	} else if sp := st.SecurePairing; sp.Active && sp.Host != "" && sp.Port > 0 {
		endpoints = []mobileEndpoint{{Kind: "tailscale", Host: sp.Host, Port: sp.Port, Secure: true}}
	}
	if len(endpoints) == 0 {
		return "", fmt.Errorf("no usable pairing endpoint; enable secure pairing for Tailscale, or use --lan on a trusted network")
	}
	offer := struct {
		V         int              `json:"v"`
		HostID    string           `json:"hostId"`
		Name      string           `json:"name"`
		Platform  string           `json:"platform"`
		Endpoints []mobileEndpoint `json:"endpoints"`
		Token     string           `json:"token"`
	}{2, st.HostID, name, platform, endpoints, st.Password}
	data, err := json.Marshal(offer)
	if err != nil {
		return "", err
	}
	return "aomobile://pair#" + base64.RawURLEncoding.EncodeToString(data), nil
}
