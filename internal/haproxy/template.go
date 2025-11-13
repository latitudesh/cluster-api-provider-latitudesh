/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package haproxy

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

// Backend represents a HAProxy backend server
type Backend struct {
	Name string
	IP   string
	Port int32
}

// Config represents the HAProxy configuration parameters
type Config struct {
	APIPort      int32
	StatsPort    int32
	Backends     []Backend
	EnableStats  bool
	UpdateScript bool // Include the update script in cloud-init
}

const cloudInitTemplate = `#cloud-config
package_update: true
packages:
  - haproxy
  - jq
  - curl

write_files:
  - path: /etc/haproxy/haproxy.cfg
    content: |
      global
          log /dev/log local0
          log /dev/log local1 notice
          chroot /var/lib/haproxy
          stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners
          stats timeout 30s
          user haproxy
          group haproxy
          daemon
          maxconn 4096

      defaults
          log     global
          mode    tcp
          option  tcplog
          option  dontlognull
          timeout connect 5s
          timeout client  50s
          timeout server  50s
          timeout tunnel  1h
          errorfile 400 /etc/haproxy/errors/400.http
          errorfile 403 /etc/haproxy/errors/403.http
          errorfile 408 /etc/haproxy/errors/408.http
          errorfile 500 /etc/haproxy/errors/500.http
          errorfile 502 /etc/haproxy/errors/502.http
          errorfile 503 /etc/haproxy/errors/503.http
          errorfile 504 /etc/haproxy/errors/504.http

      # Kubernetes API Frontend
      frontend kubernetes-api
          bind *:{{ .APIPort }}
          mode tcp
          option tcplog
          default_backend kubernetes-control-planes

      # Kubernetes API Backend
      backend kubernetes-control-planes
          mode tcp
          balance roundrobin
          option tcp-check

          # TCP health check with TLS handshake
          tcp-check connect port {{ .APIPort }}
          tcp-check send-binary 16030100
          tcp-check expect binary 160301

          # Backends will be added dynamically
          {{- range .Backends }}
          server {{ .Name }} {{ .IP }}:{{ .Port }} check inter 2s rise 2 fall 3
          {{- end }}
{{ if .EnableStats }}
      # HAProxy Stats Interface
      listen stats
          bind *:{{ .StatsPort }}
          mode http
          stats enable
          stats uri /
          stats refresh 10s
          stats show-legends
          stats show-node
          stats admin if TRUE
{{ end }}

  - path: /usr/local/bin/update-haproxy-backends.sh
    permissions: '0755'
    content: |
      #!/bin/bash
      # Script to update HAProxy backends dynamically
      # This script is called by the CAPL controller via SSH

      set -euo pipefail

      HAPROXY_CFG="/etc/haproxy/haproxy.cfg"
      HAPROXY_SOCKET="/run/haproxy/admin.sock"
      BACKEND_NAME="kubernetes-control-planes"

      usage() {
          echo "Usage: $0 {add|remove|list|reload} [server-name] [server-ip] [port]"
          exit 1
      }

      # Function to add a backend server
      add_backend() {
          local name="$1"
          local ip="$2"
          local port="${3:-6443}"

          # Check if server already exists in config
          if grep -q "server $name $ip:$port" "$HAPROXY_CFG"; then
              echo "Backend $name already exists"
              return 0
          fi

          # Add to config file
          sed -i "/# Backends will be added dynamically/a\\          server $name $ip:$port check inter 2s rise 2 fall 3" "$HAPROXY_CFG"

          # Reload HAProxy
          systemctl reload haproxy

          echo "Added backend: $name ($ip:$port)"
      }

      # Function to remove a backend server
      remove_backend() {
          local name="$1"

          # Remove from config file
          sed -i "/server $name /d" "$HAPROXY_CFG"

          # Reload HAProxy
          systemctl reload haproxy

          echo "Removed backend: $name"
      }

      # Function to list backends
      list_backends() {
          echo "=== Current HAProxy Backends ==="
          grep "server " "$HAPROXY_CFG" | grep -v "^#" || echo "No backends configured"
      }

      # Function to reload HAProxy
      reload_haproxy() {
          systemctl reload haproxy
          echo "HAProxy reloaded"
      }

      # Main logic
      case "${1:-}" in
          add)
              [[ $# -lt 3 ]] && usage
              add_backend "$2" "$3" "${4:-6443}"
              ;;
          remove)
              [[ $# -lt 2 ]] && usage
              remove_backend "$2"
              ;;
          list)
              list_backends
              ;;
          reload)
              reload_haproxy
              ;;
          *)
              usage
              ;;
      esac

runcmd:
  - systemctl enable haproxy
  - systemctl start haproxy
  - echo "HAProxy load balancer initialized successfully" | systemd-cat -t haproxy-init
`

const haproxyConfigTemplate = `global
    log /dev/log local0
    log /dev/log local1 notice
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners
    stats timeout 30s
    user haproxy
    group haproxy
    daemon
    maxconn 4096

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect 5s
    timeout client  50s
    timeout server  50s
    timeout tunnel  1h

# Kubernetes API Frontend
frontend kubernetes-api
    bind *:{{ .APIPort }}
    mode tcp
    option tcplog
    default_backend kubernetes-control-planes

# Kubernetes API Backend
backend kubernetes-control-planes
    mode tcp
    balance roundrobin
    option tcp-check

    # TCP health check with TLS handshake
    tcp-check connect port {{ .APIPort }}
    tcp-check send-binary 16030100
    tcp-check expect binary 160301

    # Backends will be added dynamically
    {{- range .Backends }}
    server {{ .Name }} {{ .IP }}:{{ .Port }} check inter 2s rise 2 fall 3
    {{- end }}
{{ if .EnableStats }}
# HAProxy Stats Interface
listen stats
    bind *:{{ .StatsPort }}
    mode http
    stats enable
    stats uri /
    stats refresh 10s
    stats show-legends
    stats show-node
    stats admin if TRUE
{{ end }}
`

// GenerateCloudInit generates cloud-init userdata for HAProxy
func GenerateCloudInit(cfg Config) (string, error) {
	tmpl, err := template.New("cloudinit").Parse(cloudInitTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse cloud-init template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("failed to execute cloud-init template: %w", err)
	}

	return buf.String(), nil
}

// GenerateCloudInitBase64 generates base64-encoded cloud-init userdata
func GenerateCloudInitBase64(cfg Config) (string, error) {
	cloudInit, err := GenerateCloudInit(cfg)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString([]byte(cloudInit)), nil
}

// GenerateHAProxyConfig generates HAProxy configuration file content
func GenerateHAProxyConfig(cfg Config) (string, error) {
	tmpl, err := template.New("haproxy").Parse(haproxyConfigTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse haproxy template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("failed to execute haproxy template: %w", err)
	}

	return buf.String(), nil
}
