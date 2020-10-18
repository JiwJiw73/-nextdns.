package openwrt

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/nextdns/nextdns/config"
	"github.com/nextdns/nextdns/router/internal"
)

type Router struct {
	DNSMasqPath     string
	ListenPort      string
	ClientReporting bool
	CacheEnabled    bool
	SetPort0        bool

	savedForwarders string
}

func New() (*Router, bool) {
	if r, err := internal.ReadOsRelease(); err != nil || r["ID"] != "openwrt" {
		return nil, false
	}
	return &Router{
		DNSMasqPath: "/tmp/dnsmasq.d/nextdns.conf",
		ListenPort:  "5342",
	}, true
}

func (r *Router) Configure(c *config.Config) error {
	c.Listens = []string{"127.0.0.1:" + r.ListenPort}
	r.ClientReporting = c.ReportClientInfo
	if cs, _ := config.ParseBytes(c.CacheSize); cs > 0 {
		r.CacheEnabled = true
		c.Listens = []string{":53"}
		return r.setupDNSMasq() // Make dnsmasq stop listening on 53 before we do.
	}
	return nil
}

func (r *Router) Setup() (err error) {
	if !r.CacheEnabled {
		return r.setupDNSMasq()
	}
	return nil
}

func (r *Router) setupDNSMasq() (err error) {
	if r.CacheEnabled {
		// With cache enabled, we disable dns part of dnsmasq with port=0. Also,
		// dnsmasq won't start if port is redefined. If a custom dnsmasq is
		// setup, there is no need to set it to 0.
		port, err := uci("get", "dhcp.@dnsmasq[0].port")
		if err != nil {
			if errors.Is(err, uciErrEntryNotFound) {
				r.SetPort0 = true
			} else {
				return err
			}
		} else if port == "53" {
			// If it is set to 53 (the default), we remove it so port=0 doesn't
			// break.
			if _, err = uci("delete", "dhcp.@dnsmasq[0].port"); err != nil {
				return err
			}
			if _, err = uci("commit"); err != nil {
				return err
			}
		}
	} else {
		// No need change forwarders settings, we are going to replace dnsmasq
		// altogether.
		r.savedForwarders, err = uci("get", "dhcp.@dnsmasq[0].server")
		if err != nil {
			if !errors.Is(err, uciErrEntryNotFound) {
				return err
			}
		} else {
			if _, err = uci("delete", "dhcp.@dnsmasq[0].server"); err != nil {
				return err
			}
			if _, err = uci("commit"); err != nil {
				return err
			}
		}
	}

	if err := internal.WriteTemplate(r.DNSMasqPath, tmpl, r, 0644); err != nil {
		return err
	}

	// Restart dnsmasq service to apply changes.
	if err := exec.Command("/etc/init.d/dnsmasq", "restart").Run(); err != nil {
		return fmt.Errorf("dnsmasq restart: %v", err)
	}

	return nil
}

func (r *Router) Restore() error {
	// Restore forwarders
	if r.savedForwarders != "" {
		for _, f := range strings.Split(r.savedForwarders, " ") {
			if _, err := uci("add_list", "dhcp.@dnsmasq[0].server="+f); err != nil {
				return err
			}
		}
		if _, err := uci("commit"); err != nil {
			return err
		}
	}

	// Remove the custom dnsmasq config
	_ = os.Remove(r.DNSMasqPath)

	// Restart dnsmasq service to apply changes.
	if err := exec.Command("/etc/init.d/dnsmasq", "restart").Run(); err != nil {
		return fmt.Errorf("dnsmasq restart: %v", err)
	}
	return nil
}

var tmpl = `# Configuration generated by NextDNS
{{- if .CacheEnabled}}
# DNS is handled by NextDNS
{{- if .SetPort0}}
port=0
{{- end}}
{{- else}}
no-resolv
server=127.0.0.1#{{.ListenPort}}
{{- if .ClientReporting}}
add-mac
add-subnet=32,128
{{- end}}
{{- end}}
`
